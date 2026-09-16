package server

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"filefin/internal/db"
	"filefin/internal/library"
	"filefin/internal/logging"
	"filefin/internal/transcode"
)

// Applying what misnamed.go reports: rename a media folder and its files to the names its
// metadata and the configured format call for. A media id is the hash of the folder's
// relative path, so this is the one operation that deliberately changes an item's id. Only
// the names move - meta.json travels with the folder, which is why the per-user state, the
// ratings, the resume pointers, the posters and the optimized copies all survive.

// handleRename renames one media item's folder and files to match its metadata. It answers
// with the item's new id, since the rename mints one.
func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pool, err := s.ensureDB(ctx)
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	m, err := db.GetMedia(ctx, pool, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	files, err := db.MediaFiles(ctx, pool, m.ID)
	if err != nil {
		http.Error(w, "could not read the item's files", http.StatusInternalServerError)
		return
	}
	// The plan is always recomputed here: the browser's copy may be minutes old, and it is
	// the plan that carries every safety check the rename relies on.
	plan, ok := plannedRename(m, files, s.mediaFormat())
	if !ok {
		http.Error(w, "this item cannot be renamed right now", http.StatusConflict)
		return
	}
	if busy, reason := s.renameBlocked(ctx, pool, m); busy {
		http.Error(w, reason, http.StatusConflict)
		return
	}
	cats, err := library.List(s.dataDir())
	if err != nil {
		http.Error(w, "could not read categories", http.StatusInternalServerError)
		return
	}
	cat, ok := categoryByID(cats, m.CategoryID)
	if !ok {
		http.Error(w, "the item's category is gone", http.StatusConflict)
		return
	}

	// Files first, then the folder: both are plain renames within one category, so nothing is
	// copied and nothing is deleted. Every target was checked free while the plan was built.
	for _, c := range plan.Files {
		if err := os.Rename(filepath.Join(m.Path, c.From), filepath.Join(m.Path, c.To)); err != nil {
			http.Error(w, "could not rename "+c.From, http.StatusInternalServerError)
			return
		}
	}
	if plan.Folder.From != plan.Folder.To {
		if err := s.metaMgr.Rename(m.Path, filepath.Join(filepath.Dir(m.Path), plan.Folder.To)); err != nil {
			http.Error(w, "could not rename the folder", http.StatusInternalServerError)
			return
		}
	}

	// Re-key the cache rather than wait for a discovery tick: to the cache this is the old id
	// vanishing and a new folder appearing, which is exactly what the reconcile does.
	newID := mediaID(cat.Name, plan.Folder.To)
	if newID != m.ID {
		s.dropMediaFromCache(ctx, pool, m.ID, "renamed")
	}
	insertMediaFromDisk(ctx, pool, s.dataDir(), cat, plan.Folder.To)
	s.logger().For(logging.Backend).Info("media renamed to match its metadata",
		logging.Fields{"from": plan.Folder.From, "to": plan.Folder.To, "files": len(plan.Files)})

	writeJSON(w, struct {
		ID string `json:"id"`
	}{newID})
}

// renameBlocked reports whether work in flight makes it unsafe to move an item's files, with
// the reason to show the admin. An optimize job writes through a temp file beside its source,
// so a leftover one is treated as busy too.
func (s *Server) renameBlocked(ctx context.Context, pool *sql.DB, m db.Media) (bool, string) {
	if active, err := db.HasActiveOptimize(ctx, pool, m.ID); err != nil || active {
		return true, "this item is being optimized right now"
	}
	entries, err := os.ReadDir(m.Path)
	if err != nil {
		return true, "the item's folder could not be read"
	}
	for _, e := range entries {
		if strings.HasSuffix(strings.ToLower(e.Name()), transcode.OptimizedTmpSuffix) {
			return true, "an unfinished optimized copy is still in the folder"
		}
	}
	return false, ""
}

// categoryByID finds a category by its stable config.json id.
func categoryByID(cats []library.Category, id int64) (library.Category, bool) {
	for _, c := range cats {
		if c.ID == id {
			return c, true
		}
	}
	return library.Category{}, false
}
