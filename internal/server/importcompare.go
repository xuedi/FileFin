package server

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"filefin/internal/db"
	"filefin/internal/ffprobe"
	"filefin/internal/importer"
)

// dupSide is one half of a duplicate comparison: what the item holds in total, plus the
// probed details of the single file those numbers are illustrated with. It is the same
// shape for both sides so the page can render them next to each other and the admin can
// see which copy is worth keeping.
type dupSide struct {
	Title      string `json:"title"`
	Path       string `json:"path"`
	Category   string `json:"category,omitempty"`
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
	File       string `json:"file"`
	FileBytes  int64  `json:"fileBytes"`
	Modified   string `json:"modified,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Container  string `json:"container,omitempty"`
	VideoCodec string `json:"videoCodec,omitempty"`
	AudioCodec string `json:"audioCodec,omitempty"`
	Duration   int    `json:"duration,omitempty"`
}

// dupCompare answers "what exactly sits on both sides of this duplicate".
type dupCompare struct {
	MediaID  string  `json:"mediaId"`
	Incoming dupSide `json:"incoming"`
	Library  dupSide `json:"library"`
}

// applyTechnical folds a probed technical block onto a side.
func (d *dupSide) applyTechnical(t ffprobe.Technical) {
	d.Width, d.Height, d.Duration = t.Width, t.Height, t.Duration
	d.Container, d.VideoCodec, d.AudioCodec = t.Container, t.VideoCodec, t.AudioCodec
}

// fileSize is a file's size in bytes, 0 when it cannot be read.
func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

// fileModified renders a file's mtime in the canonical YYYY-MM-DD HH:MM:SS, "" when it
// cannot be read.
func fileModified(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmtUnix(fi.ModTime().Unix())
}

// relTo renders a path the way the admin reads it: relative to root, with forward slashes.
// An empty root (or a path outside it) yields the path itself, since a half-relative answer
// would be more confusing than the full one.
func relTo(root, path string) string {
	if root == "" {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// titleYear labels an item the way both tables show it.
func titleYear(title string, year int) string {
	if year > 0 {
		return title + " (" + strconv.Itoa(year) + ")"
	}
	return title
}

// incomingSide describes what an import would bring in. Only the first file is probed: a
// season pack must not fire an ffprobe per episode because a marker was hovered, so the
// file count says how much the numbers stand for.
func (s *Server) incomingSide(ctx context.Context, title, root string, paths []string) dupSide {
	side := dupSide{Title: title, Files: len(paths)}
	for _, p := range paths {
		side.Bytes += fileSize(p)
	}
	if len(paths) == 0 {
		return side
	}
	first := paths[0]
	side.Path = relTo(root, first)
	side.File = filepath.Base(first)
	side.FileBytes = fileSize(first)
	side.Modified = fileModified(first)
	side.applyTechnical(ffprobe.Probe(ctx, s.probeFFprobe(), first))
	return side
}

// librarySide describes the item already in the library, comparing against the file that
// matches the incoming season/episode where there is one (so replacing episode 3 is judged
// against episode 3, not against whichever file sorts first). ffprobe is the authority; when
// it is unavailable the cached format columns and meta.json's technical block stand in, so
// the panel still says something useful on a host without ffmpeg.
func (s *Server) librarySide(ctx context.Context, pool *sql.DB, mediaID, dataDir string, season, episode int) (dupSide, bool) {
	m, err := db.GetMedia(ctx, pool, mediaID)
	if err != nil {
		return dupSide{}, false
	}
	files, err := db.MediaFiles(ctx, pool, mediaID)
	if err != nil {
		return dupSide{}, false
	}
	side := dupSide{Title: titleYear(m.Title, m.Year), Files: len(files)}
	for _, f := range files {
		side.Bytes += fileSize(f.Path)
	}
	side.Category = relTo(dataDir, filepath.Dir(m.Path))
	if len(files) == 0 {
		return side, true
	}
	pick := files[0]
	for _, f := range files {
		if f.Season == season && f.Episode == episode {
			pick = f
			break
		}
	}
	side.Path = relTo(dataDir, pick.Path)
	side.File = pick.Name
	side.FileBytes = fileSize(pick.Path)
	side.Modified = fileModified(pick.Path)
	if tech := ffprobe.Probe(ctx, s.probeFFprobe(), pick.Path); !tech.Empty() {
		side.applyTechnical(tech)
		return side, true
	}
	side.Container, side.VideoCodec, side.AudioCodec = pick.Container, pick.VideoCodec, pick.AudioCodec
	if meta, err := importer.ReadMeta(m.Path); err == nil && meta.Technical != nil {
		side.Width, side.Height, side.Duration = meta.Technical.Width, meta.Technical.Height, meta.Technical.Duration
	}
	return side, true
}

// handleImportFolderDuplicate compares one import-folder item with the library item it
// duplicates. The page holds no filesystem paths (it hands rows back by id), so the item is
// resolved the same way the import itself resolves it: re-scan, match by id, re-run the
// duplicate check. A row that is no longer a duplicate is a 404 - the library moved on.
func (s *Server) handleImportFolderDuplicate(w http.ResponseWriter, r *http.Request) {
	folder := s.importFolder()
	if folder == "" {
		http.NotFound(w, r)
		return
	}
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	items, err := scanImportFolder(folder)
	if err != nil {
		http.Error(w, "could not read the import folder", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	s.markDuplicateItems(ctx, pool, items)
	id := r.PathValue("id")
	for _, it := range items {
		if it.ID != id {
			continue
		}
		if it.DuplicateID == "" {
			http.NotFound(w, r)
			return
		}
		season, episode := 0, 0
		if len(it.probes) > 0 {
			season, episode = it.probes[0].Season, it.probes[0].Episode
		}
		lib, ok := s.librarySide(ctx, pool, it.DuplicateID, s.dataDir(), season, episode)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, dupCompare{
			MediaID:  it.DuplicateID,
			Incoming: s.incomingSide(ctx, titleYear(it.Title, it.Year), folder, it.paths),
			Library:  lib,
		})
		return
	}
	http.NotFound(w, r)
}

// handleImportRowDuplicate is the same comparison for a staged preCheck row. The page
// collapses a show's episodes into one line, so the incoming side is the whole group of
// staged rows sharing that title and year, not just the row that was hovered.
func (s *Server) handleImportRowDuplicate(w http.ResponseWriter, r *http.Request) {
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
	ctx := r.Context()
	row, err := db.GetImport(ctx, pool, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rows := []db.Import{row}
	s.markDuplicates(ctx, pool, rows)
	if rows[0].DuplicateID == "" {
		http.NotFound(w, r)
		return
	}
	lib, ok := s.librarySide(ctx, pool, rows[0].DuplicateID, s.dataDir(), row.Season, row.Episode)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, dupCompare{
		MediaID:  rows[0].DuplicateID,
		Incoming: s.incomingSide(ctx, titleYear(row.Title, row.Year), "", stagedGroupPaths(ctx, pool, row)),
		Library:  lib,
	})
}

// stagedGroupPaths returns the source paths of every staged row sharing a row's title and
// year - the same grouping the preCheck page shows - with the hovered row's own path first
// so it is the one that gets probed.
func stagedGroupPaths(ctx context.Context, pool *sql.DB, row db.Import) []string {
	out := []string{row.SourcePath}
	staged, err := db.ListImports(ctx, pool, db.StatusPreCheck)
	if err != nil {
		return out
	}
	for _, r := range staged {
		if r.ID != row.ID && r.Title == row.Title && r.Year == row.Year {
			out = append(out, r.SourcePath)
		}
	}
	return out
}
