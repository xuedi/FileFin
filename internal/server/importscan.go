package server

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/recognize"
)

// importItem is one recognisable media sitting in the import folder: every video file of one
// entry that recognises to the same title and year, folded into a single line for the import
// page. It is derived from the filesystem on every read and never stored - the page is a
// preview of what an import would create, and nothing is written until the admin presses
// Import.
type importItem struct {
	ID        string `json:"id"`    // stable across scans: derived from entry + recognised title/year
	Entry     string `json:"entry"` // the top-level entry it lives in
	Dir       bool   `json:"dir"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
	SubCount  int    `json:"subCount"`
	HasPoster bool   `json:"hasPoster"`
	Duplicate string `json:"duplicate"` // the library item this would import a second time
	paths     []string
	probes    []db.Import
}

// handleImportFolder previews the import folder: the configured path plus one item per
// recognised media, with the duplicate check already applied. An unconfigured import folder
// is an empty listing, not an error - the page explains the gap itself.
func (s *Server) handleImportFolder(w http.ResponseWriter, r *http.Request) {
	folder := s.importFolder()
	out := struct {
		Folder string       `json:"folder"`
		Items  []importItem `json:"items"`
	}{Folder: folder, Items: []importItem{}}
	if folder == "" {
		writeJSON(w, out)
		return
	}
	items, err := scanImportFolder(folder)
	if err != nil {
		http.Error(w, "could not read the import folder", http.StatusInternalServerError)
		return
	}
	if pool, err := s.ensureDB(r.Context()); err == nil {
		s.markDuplicateItems(r.Context(), pool, items)
	}
	out.Items = items
	writeJSON(w, out)
}

// handleImportFolderStart imports the picked media in one step: it re-scans the folder,
// matches each requested item by id, and stages its files straight as import rows (skipping
// preCheck - the page the admin just reviewed *is* the check) so the poller copies them on
// its next tick. An id that no longer resolves is skipped rather than failing the batch: the
// folder can have changed since the page was drawn.
func (s *Server) handleImportFolderStart(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		DeleteAfter bool `json:"deleteAfter"`
		PurgeFolder bool `json:"purgeFolder"`
		Items       []struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			Year       int    `json:"year"`
			CategoryID int64  `json:"categoryId"`
		} `json:"items"`
	}](w, r)
	if err != nil || len(req.Items) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	folder := s.importFolder()
	if folder == "" {
		http.Error(w, "no import folder configured", http.StatusBadRequest)
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
	byID := map[string]importItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	ctx := r.Context()
	staged, skipped := 0, 0
	for _, want := range req.Items {
		it, ok := byID[want.ID]
		if !ok {
			skipped++
			continue
		}
		cat, ok := s.categoryByID(want.CategoryID)
		if !ok {
			skipped++
			continue
		}
		title := strings.TrimSpace(want.Title)
		if title == "" {
			title = it.Title
		}
		staged += s.stageItem(ctx, pool, it, cat.ID, title, want.Year, req.DeleteAfter)
	}
	if skipped > 0 {
		s.logger().For(logging.Import).Error("some media could not be staged for import",
			logging.Fields{"skipped": skipped, "requested": len(req.Items)})
	}
	// Emptying the folder only makes sense once the sources have been consumed, so it rides
	// on delete-after; the poller runs it when this batch has finished copying.
	if req.PurgeFolder && req.DeleteAfter && staged > 0 {
		s.purgeArmed.Store(true)
	}
	s.logger().For(logging.Import).Info("queued "+strconv.Itoa(staged)+" media file(s) for import",
		logging.Fields{"files": staged, "media": len(req.Items) - skipped})
	writeJSON(w, struct {
		Started int `json:"started"`
		Skipped int `json:"skipped"`
	}{staged, skipped})
}

// stageItem writes one import row per file of a recognised media, already in the import
// status so the poller picks them up without a second confirmation. The admin's title and
// year win over what recognition guessed; season and episode stay per file.
func (s *Server) stageItem(ctx context.Context, pool *sql.DB, it importItem, categoryID int64, title string, year int, deleteAfter bool) int {
	n := 0
	for i, path := range it.paths {
		subsJSON := ""
		if subs := importer.FindSidecarSubtitles(path); len(subs) > 0 {
			if b, err := json.Marshal(subs); err == nil {
				subsJSON = string(b)
			}
		}
		if _, err := db.InsertImport(ctx, pool, db.Import{
			CategoryID: categoryID, SourcePath: path, Filename: filepath.Base(path),
			Title: title, Year: year, Season: it.probes[i].Season, Episode: it.probes[i].Episode,
			Subtitles: subsJSON, Poster: importer.FindSidecarPoster(path),
			Status: db.StatusImport, DeleteAfter: deleteAfter, Origin: db.OriginFolder,
		}); err != nil {
			continue
		}
		n++
	}
	return n
}

// scanImportFolder walks the import folder once and folds its video files into recognised
// media: everything that recognises to the same title and year becomes one item, because
// that is exactly what the importer would place in one media folder - a show split over two
// season folders is one media, not two. Recognition reads each path relative to the import
// folder, so folder names inform title and season exactly as they do at staging time.
func scanImportFolder(folder string) ([]importItem, error) {
	files, err := scanVideoFiles(folder)
	if err != nil {
		return nil, err
	}
	items := []importItem{}
	idx := map[string]int{}
	entries := []map[string]bool{} // per item: which top-level entries it spans
	for _, f := range files {
		rel, err := filepath.Rel(folder, f.path)
		if err != nil {
			rel = filepath.Base(f.path)
		}
		entry, dir := topEntry(rel)
		p := recognize.FromPath(rel)
		key := p.Title + "\x00" + strconv.Itoa(p.Year)
		i, ok := idx[key]
		if !ok {
			i = len(items)
			idx[key] = i
			items = append(items, importItem{ID: groupID(key), Title: p.Title, Year: p.Year})
			entries = append(entries, map[string]bool{})
		}
		it := &items[i]
		it.Files++
		it.Bytes += f.size
		it.SubCount += len(importer.FindSidecarSubtitles(f.path))
		if !it.HasPoster && importer.FindSidecarPoster(f.path) != "" {
			it.HasPoster = true
		}
		it.Dir = it.Dir || dir
		entries[i][entry] = true
		it.paths = append(it.paths, f.path)
		it.probes = append(it.probes, db.Import{Title: p.Title, Year: p.Year, Season: p.Season, Episode: p.Episode})
	}
	for i := range items {
		items[i].Entry = entryLabel(entries[i])
	}
	return items, nil
}

// entryLabel names where a media came from: the entry itself when it lives in one, or a
// count when its files are spread over several.
func entryLabel(entries map[string]bool) string {
	if len(entries) == 1 {
		for name := range entries {
			return name
		}
	}
	return strconv.Itoa(len(entries)) + " entries"
}

// topEntry splits a path relative to the import folder into its top-level entry and whether
// that entry is a directory (the file sits deeper) or the loose video file itself.
func topEntry(rel string) (string, bool) {
	if i := strings.IndexRune(rel, filepath.Separator); i >= 0 {
		return rel[:i], true
	}
	return rel, false
}

// groupID is a short stable hash of an item's identity, so the page can hand an item back
// for import without ever sending a filesystem path.
func groupID(key string) string {
	sum := sha1.Sum([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
}

// markDuplicateItems labels an item that the library already holds. A media counts as a
// duplicate only when *every* one of its files is already there, so a season pack whose first
// episodes are imported still reads as new work rather than a re-import.
func (s *Server) markDuplicateItems(ctx context.Context, pool *sql.DB, items []importItem) {
	var probes []db.Import
	for _, it := range items {
		probes = append(probes, it.probes...)
	}
	s.markDuplicates(ctx, pool, probes)
	at := 0
	for i := range items {
		all, label := true, ""
		for _, p := range probes[at : at+len(items[i].probes)] {
			if p.Duplicate == "" {
				all = false
				break
			}
			if label == "" {
				label = p.Duplicate
			}
		}
		if all {
			items[i].Duplicate = label
		}
		at += len(items[i].probes)
	}
}
