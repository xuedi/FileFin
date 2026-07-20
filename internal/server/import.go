package server

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"filefin/internal/db"
	"filefin/internal/ffprobe"
	"filefin/internal/importer"
	"filefin/internal/library"
	"filefin/internal/logging"
	"filefin/internal/mediafmt"
	"filefin/internal/omdb"
	"filefin/internal/recognize"
)

// videoExts are the file extensions the folder scan treats as media.
var videoExts = map[string]bool{
	".mp4": true, ".webm": true, ".mkv": true, ".avi": true,
	".mov": true, ".m4v": true, ".ts": true, ".m2ts": true,
	".mpg": true, ".mpeg": true,
}

// categoryByID returns the category whose stored id matches, reading the filesystem
// (the source of truth).
func (s *Server) categoryByID(id int64) (library.Category, bool) {
	cats, err := library.List(s.dataDir())
	if err != nil {
		return library.Category{}, false
	}
	for _, c := range cats {
		if c.ID == id {
			return c, true
		}
	}
	return library.Category{}, false
}

// omdbClient returns an OMDb client when a key is configured, else nil.
func (s *Server) omdbClient() *omdb.Client {
	s.mu.RLock()
	key := ""
	if s.cfg != nil {
		key = s.cfg.OMDBKey
	}
	s.mu.RUnlock()
	if key == "" {
		return nil
	}
	return omdb.New(key)
}

// stageFolder clears a category's prior preCheck rows, scans root for video files, and
// inserts one preCheck row per file (recognised title/year/episode plus any sidecar
// subtitles). It is the staging core behind the upload source, which drops its files in a
// throwaway /tmp working dir and then walks it exactly like a folder. deleteAfter is stamped
// on each row so those working files are always cleaned up after a successful import; origin
// records which front stage produced the rows. OMDb enrichment is left to the dedicated agent.
func (s *Server) stageFolder(ctx context.Context, pool *sql.DB, cat library.Category, root string, deleteAfter bool, origin string) ([]db.Import, error) {
	if err := db.ClearStagedImports(ctx, pool); err != nil {
		return nil, err
	}
	files, err := scanVideos(root)
	if err != nil {
		return nil, err
	}
	failed := 0
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		p := recognize.FromPath(rel)
		subsJSON := ""
		if subs := importer.FindSidecarSubtitles(f); len(subs) > 0 {
			if b, err := json.Marshal(subs); err == nil {
				subsJSON = string(b)
			}
		}
		// A dropped insert means this file silently never imports, so the failures are
		// counted and surfaced rather than swallowed.
		if _, err := db.InsertImport(ctx, pool, db.Import{
			CategoryID: cat.ID, SourcePath: f, Filename: filepath.Base(f),
			Title: p.Title, Year: p.Year, Season: p.Season, Episode: p.Episode,
			Subtitles: subsJSON, Poster: importer.FindSidecarPoster(f),
			Status: db.StatusPreCheck, DeleteAfter: deleteAfter, Origin: origin,
		}); err != nil {
			failed++
		}
	}
	if failed > 0 {
		s.logger().For(logging.Import).Error("some files could not be staged for import",
			logging.Fields{"failed": failed, "scanned": len(files), "category": cat.Name})
	}
	return db.ListImports(ctx, pool, db.StatusPreCheck)
}

// videoFile is one scanned video with the size the import would copy.
type videoFile struct {
	path string
	size int64
}

// scanVideos walks root recursively, returning the path of every video file (skipping
// hidden files and directories).
func scanVideos(root string) ([]string, error) {
	files, err := scanVideoFiles(root)
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out, err
}

// scanVideoFiles is the walk behind scanVideos, carrying each file's size for the callers
// that report how much an entry holds.
func scanVideoFiles(root string) ([]videoFile, error) {
	var out []videoFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep walking
		}
		name := d.Name()
		if path != root && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && recognize.SkipDir(name) {
				return fs.SkipDir // extras, samples and cover scans are not the media
			}
			return nil
		}
		if isOptimizedSibling(name) || recognize.SkipFile(name) {
			return nil // never stage a derived copy, a teaser or a trailer for import
		}
		if videoExts[strings.ToLower(filepath.Ext(name))] {
			f := videoFile{path: path}
			if info, err := d.Info(); err == nil {
				f.size = info.Size()
			}
			out = append(out, f)
		}
		return nil
	})
	return out, err
}

// folderPoster returns the name of an existing "poster.*" file in dir, or "" when
// the folder has none yet. Used so a multi-episode show places its poster once.
func folderPoster(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(strings.ToLower(e.Name()), "poster.") {
			return e.Name()
		}
	}
	return ""
}

// handleListImports returns import rows, optionally filtered by ?status=.
func (s *Server) handleListImports(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	rows, err := db.ListImports(r.Context(), pool, r.URL.Query().Get("status"))
	if err != nil {
		http.Error(w, "could not list imports", http.StatusInternalServerError)
		return
	}
	s.markDuplicates(r.Context(), pool, rows)
	writeJSON(w, rows)
}

// handleUpdateImport edits a staged row's title/year/category and returns the row.
func (s *Server) handleUpdateImport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	req, err := decodeJSON[struct {
		Title      string `json:"title"`
		Year       int    `json:"year"`
		CategoryID int64  `json:"categoryId"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	prev, err := db.GetImport(ctx, pool, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Move the row to a different category when asked (the edit can re-target a whole
	// show); only a known category is honoured.
	if req.CategoryID > 0 && req.CategoryID != prev.CategoryID {
		if cat, ok := s.categoryByID(req.CategoryID); ok {
			if err := db.UpdateImportCategory(ctx, pool, id, cat.ID); err != nil {
				http.Error(w, "could not update category", http.StatusInternalServerError)
				return
			}
		}
	}
	title := strings.TrimSpace(req.Title)
	if err := db.UpdateImportFields(ctx, pool, id, title, req.Year); err != nil {
		http.Error(w, "could not update import", http.StatusInternalServerError)
		return
	}
	row, err := db.GetImport(ctx, pool, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// The edited title/year decide the match, so the warning is recomputed here rather
	// than carried over from the assessment.
	rows := []db.Import{row}
	s.markDuplicates(ctx, pool, rows)
	writeJSON(w, rows[0])
}

// handleDeleteImport drops a row and its temp poster.
func (s *Server) handleDeleteImport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	// Drop only the staged row; its source poster/subtitles belong to the user's source
	// folder (or a throwaway upload dir that is swept separately), so they are left alone.
	if err := db.DeleteImport(r.Context(), pool, id); err != nil {
		http.Error(w, "could not delete import", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleStartImport records the per-batch delete-after choice on the staged rows,
// then flips preCheck to import; the poller does the rest on its next tick.
func (s *Server) handleStartImport(w http.ResponseWriter, r *http.Request) {
	// The body is optional; default deleteAfter to false when absent.
	req, _ := decodeJSON[struct {
		DeleteAfter bool `json:"deleteAfter"`
	}](w, r)

	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	if err := db.SetDeleteAfterForStatus(ctx, pool, db.StatusPreCheck, req.DeleteAfter); err != nil {
		http.Error(w, "could not start import", http.StatusInternalServerError)
		return
	}
	n, err := db.SetImportStatus(ctx, pool, db.StatusPreCheck, db.StatusImport)
	if err != nil {
		http.Error(w, "could not start import", http.StatusInternalServerError)
		return
	}
	writeJSON(w, struct {
		Started int64 `json:"started"`
	}{n})
}

// handleActiveImports returns rows still importing (with live copy progress) for the
// Progress page.
func (s *Server) handleActiveImports(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	rows, err := db.ListActiveImports(r.Context(), pool)
	if err != nil {
		http.Error(w, "could not list active imports", http.StatusInternalServerError)
		return
	}
	// Overlay live byte counts held in memory (fresher than the periodic DB mirror).
	s.progMu.Lock()
	for i := range rows {
		if p, ok := s.progress[rows[i].ID]; ok {
			rows[i].Copied, rows[i].Total = p.copied, p.total
		}
	}
	s.progMu.Unlock()
	writeJSON(w, rows)
}

// mediaID is sha1 of the media folder's path relative to the data dir, hex, first 12
// chars. category is that relpath of the owning category ("Movies", "Movies/SciFi"), so
// category + "/" + folder is the media folder's relpath. Top-level ids are unchanged by
// nesting, so existing items keep their id and per-user state.
func mediaID(category, folder string) string {
	sum := sha1.Sum([]byte(category + "/" + folder))
	return hex.EncodeToString(sum[:])[:12]
}

// importOne performs a single import row end to end: copy the file into the
// canonical layout, probe it, write meta.json, place the poster, and insert the
// media rows. It records terminal status on the row.
func (s *Server) importOne(ctx context.Context, pool *sql.DB, row db.Import) {
	s.mu.RLock()
	dataDir, format, subLang, ffmpeg, ffprobeBin := "", "", "", "", ""
	if s.cfg != nil {
		dataDir, format = s.cfg.DataDir, s.cfg.MediaFormat
		subLang, ffmpeg, ffprobeBin = s.cfg.SubLang(), s.cfg.FFmpeg(), s.cfg.FFprobe()
	}
	s.mu.RUnlock()

	fail := func(msg string) {
		_ = db.UpdateImportProgress(ctx, pool, row.ID, db.StatusError, 0, 0, msg)
		s.clearProgress(row.ID)
		s.logger().For(logging.Import).Error("import failed for "+row.Filename,
			logging.Fields{"title": row.Title, "category": row.Category, "error": msg})
	}

	_ = db.UpdateImportProgress(ctx, pool, row.ID, db.StatusImporting, 0, 0, "")

	ext := strings.ToLower(filepath.Ext(row.SourcePath))
	folder := mediafmt.FolderName(format, row.Year, row.Title)
	dir := filepath.Join(dataDir, row.Category, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail("could not create media folder: " + err.Error())
		return
	}
	fileName := mediafmt.FileName(format, row.Year, row.Title, row.Season, row.Episode, row.Part, ext)
	target := filepath.Join(dir, fileName)

	// The live Progress page reads the in-memory map (updated every chunk), so the DB
	// mirror only needs to be coarse: throttle it to ~1/s to avoid a write storm that
	// would race admin requests into SQLITE_BUSY.
	var finalTotal int64
	var lastMirror time.Time
	err := importer.CopyFile(row.SourcePath, target, func(copied, total int64) {
		finalTotal = total
		s.setProgress(row.ID, copied, total)
		if now := time.Now(); now.Sub(lastMirror) >= time.Second {
			lastMirror = now
			s.bestEffort(db.UpdateImportProgress(ctx, pool, row.ID, db.StatusImporting, copied, total, ""), "import progress mirror")
		}
	})
	if err != nil {
		fail("copy failed: " + err.Error())
		return
	}

	// Subtitles ride along like the poster: place them beside the video, converting
	// non-SRT text formats to SRT. Best-effort - a subtitle failure never fails the import.
	if row.Subtitles != "" {
		var subs []importer.Subtitle
		if json.Unmarshal([]byte(row.Subtitles), &subs) == nil && len(subs) > 0 {
			importer.PlaceSubtitles(ctx, target, subLang, ffmpeg, subs)
		}
	}

	// Then externalise any embedded text subtitle tracks the player can show, skipping
	// languages already covered by the sidecars just placed. Best-effort like the above.
	if n := importer.ExtractEmbeddedSubtitles(ctx, target, ffmpeg, ffprobeBin); n > 0 {
		s.logger().For(logging.Import).Info("extracted embedded subtitles for "+row.Filename,
			logging.Fields{"count": n})
	}

	// meta.json is folder-wide; for a multi-episode show the first imported episode
	// writes it and the rest reuse what is already there. A row carrying a source
	// metadata blob (api_json, e.g. Plex) writes that verbatim and is marked enriched
	// so the OMDb agent skips it; an empty blob falls back to a stub the enricher fills.
	// The write goes through the shared per-folder lock so a playback event mid-import
	// cannot race the file.
	// Probe the real container + codecs once: the folder-wide meta.json technical block
	// (first episode only) and this file's cache format columns both come from it.
	tech := ffprobe.Probe(ctx, ffprobeBin, target)
	meta, err := s.metaMgr.Update(dir, func(cur importer.Meta) importer.Meta {
		if cur.Title != "" {
			return cur // an earlier episode already wrote this folder's meta
		}
		var nm importer.Meta
		if row.APIJSON != "" {
			if json.Unmarshal([]byte(row.APIJSON), &nm) != nil {
				nm = importer.StubMeta(row.Title, row.Year)
			}
		} else {
			nm = importer.StubMeta(row.Title, row.Year)
		}
		nm.Title, nm.Year = row.Title, row.Year // the row's title/year match the folder
		if !tech.Empty() {
			nm.Technical = &tech
		}
		nm.State = cur.State // preserve any state already on disk (defensive)
		return nm
	})
	if err != nil {
		fail("could not write meta.json: " + err.Error())
		return
	}

	// A poster.* beside the source rides along like the subtitles. An existing one is kept
	// (a multi-episode show places it once, and a re-import keeps what is there); when the
	// source carried none, the enrichment agent downloads one later.
	posterRel := folderPoster(dir)
	if posterRel == "" && row.Poster != "" {
		if rel, err := importer.PlacePoster(row.Poster, target); err == nil {
			posterRel = rel
		}
	}

	id := mediaID(row.Category, folder)
	if err := db.InsertMedia(ctx, pool, db.Media{
		ID: id, CategoryID: row.CategoryID, Path: dir,
		Year: row.Year, Title: row.Title, Description: meta.Description, Plot: meta.Plot,
		Poster: posterRel, Enriched: meta.Enriched,
		Language: meta.Metadata["language"], Country: meta.Metadata["origin"],
		Director: meta.Metadata["directedBy"], Writer: meta.Metadata["writtenBy"],
	}); err != nil {
		fail("could not write media row: " + err.Error())
		return
	}
	_ = db.ReplaceMediaFacets(ctx, pool, id, meta.Actors, meta.Tags)
	if len(meta.State) > 0 {
		us := make(map[string]db.UserStateRow, len(meta.State))
		for u, st := range meta.State {
			us[u] = userStateRow(st)
		}
		_ = db.ReplaceUserStateForMedia(ctx, pool, id, us)
	}
	idx, _ := db.CountMediaFiles(ctx, pool, id)
	_ = db.InsertMediaFile(ctx, pool, db.MediaFile{
		MediaID: id, Idx: idx, Path: target, Name: fileName, Season: row.Season, Episode: row.Episode, Ext: ext,
		Container: tech.Container, VideoCodec: tech.VideoCodec, AudioCodec: tech.AudioCodec,
	})

	// The import folder is a vacuum: once the copy and media row are committed, remove
	// the source if the row asked for it. Best-effort - the import already succeeded, so
	// a failed delete is logged but does not flip the row to error.
	if row.DeleteAfter {
		s.vacuumSource(row)
	}

	_ = db.UpdateImportProgress(ctx, pool, row.ID, db.StatusDone, finalTotal, finalTotal, "")
	s.clearProgress(row.ID)
	if row.DeleteAfter {
		// This row is now done; once no unfinished row points into the same upload /tmp
		// dir, the whole session folder (video, sidecars, dir) is removed.
		s.cleanupUploadDir(ctx, pool, row.SourcePath)
	}
	s.logger().For(logging.Import).Info(fmt.Sprintf("imported %s into %s", row.Title, row.Category),
		logging.Fields{"title": row.Title, "category": row.Category, "path": target,
			"bytes": finalTotal, "deletedSource": row.DeleteAfter})
}

// vacuumSource removes everything this row consumed from the source tree - the video, the
// sidecar subtitles that rode along, and a per-video poster - then prunes the folders the
// removal emptied, so an import folder that has been fully imported is left empty rather
// than littered with husk directories. Every step is best-effort: the import has already
// succeeded and a leftover file is not worth failing it over.
func (s *Server) vacuumSource(row db.Import) {
	if err := os.Remove(row.SourcePath); err != nil {
		s.logger().For(logging.Import).Error("could not remove source after import: "+row.Filename,
			logging.Fields{"source": row.SourcePath, "error": err.Error()})
	}
	for _, p := range sourceResidue(row) {
		_ = os.Remove(p)
	}
	s.pruneEmptyDirs(filepath.Dir(row.SourcePath))
}

// sourceResidue lists the files that came with the source video and now exist as copies in
// the media folder: the sidecar subtitles recorded on the row (both halves of a VobSub pair)
// and the poster, per-video or folder-level. Only files sitting in the video's own directory
// count, so a poster picked up further up the tree is never touched.
func sourceResidue(row db.Import) []string {
	dir := filepath.Dir(row.SourcePath)
	var out []string
	if row.Subtitles != "" {
		var subs []importer.Subtitle
		if json.Unmarshal([]byte(row.Subtitles), &subs) == nil {
			for _, sub := range subs {
				out = append(out, sub.Path)
				if ext := strings.ToLower(filepath.Ext(sub.Path)); ext == ".sub" || ext == ".idx" {
					base := strings.TrimSuffix(sub.Path, filepath.Ext(sub.Path))
					out = append(out, base+".sub", base+".idx")
				}
			}
		}
	}
	if row.Poster != "" {
		out = append(out, row.Poster)
	}
	kept := out[:0]
	for _, p := range out {
		if filepath.Dir(p) == dir {
			kept = append(kept, p)
		}
	}
	return kept
}

// pruneEmptyDirs walks up from dir removing each folder that the vacuum left empty, and
// stops at the first one that still holds something. The import folder itself is the floor
// and is never removed; a source outside it (an upload session dir) is left to its own
// cleanup. os.Remove refuses a non-empty directory, so nothing that still holds media -
// a not-yet-imported episode, an unstaged file - can be taken out from under it.
func (s *Server) pruneEmptyDirs(dir string) {
	root := filepath.Clean(s.importFolder())
	if root == "" || root == "." {
		return
	}
	for dir = filepath.Clean(dir); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			return
		}
	}
}

// importFolder returns the configured server path media is imported from.
func (s *Server) importFolder() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return ""
	}
	return s.cfg.ImportFolder
}
