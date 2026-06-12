package server

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"filefin/internal/db"
	"filefin/internal/library"
)

// dataDir returns the configured data directory under lock.
func (s *Server) dataDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return ""
	}
	return s.cfg.DataDir
}

// categoryDTO is the wire shape for a category, carrying the tree links (parentId, leaf)
// the frontend needs to render the nesting. otherMedia is the category's own stored flag.
type categoryDTO struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Leaf       string `json:"leaf"`
	Alias      string `json:"alias"`
	ParentID   int64  `json:"parentId"`
	OtherMedia bool   `json:"otherMedia"`
	Position   int    `json:"position"` // sort order among siblings (same parent)
	Empty      bool   `json:"empty"`
	Media      int    `json:"media"` // media items in this category (each a movie or one TV show)
	Files      int    `json:"files"` // media files across those items
}

// categoryDTOs maps the on-disk categories to the wire shape, resolving each category's
// parent relpath to the parent's id (0 for top level).
func categoryDTOs(cats []library.Category) []categoryDTO {
	idByName := make(map[string]int64, len(cats))
	for _, c := range cats {
		idByName[c.Name] = c.ID
	}
	out := make([]categoryDTO, 0, len(cats))
	for _, c := range cats {
		out = append(out, categoryDTO{
			ID: c.ID, Name: c.Name, Leaf: c.Leaf, Alias: c.Alias,
			ParentID: idByName[c.Parent], OtherMedia: c.OtherMedia, Position: c.Position, Empty: c.Empty,
		})
	}
	return out
}

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := library.List(s.dataDir())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dtos := categoryDTOs(cats)
	// Annotate each category with its media/file tally; a cache that is unavailable just
	// leaves the counts at zero rather than failing the listing.
	if pool, err := s.ensureDB(r.Context()); err == nil {
		if counts, err := db.MediaCountsByCategory(r.Context(), pool); err == nil {
			for i := range dtos {
				c := counts[dtos[i].ID]
				dtos[i].Media, dtos[i].Files = c.Media, c.Files
			}
		}
	}
	writeJSON(w, dtos)
}

// mirrorCategories re-derives the categories cache from disk (the source of truth),
// resolving parent links and the effective other-media propagation. It is called after
// any category change so the cache - parent ids and the propagated flag - stays correct.
func (s *Server) mirrorCategories(ctx context.Context, pool *sql.DB) error {
	cats, err := library.List(s.dataDir())
	if err != nil {
		return err
	}
	idByName := make(map[string]int64, len(cats))
	for _, c := range cats {
		idByName[c.Name] = c.ID
	}
	rows := make([]db.Category, 0, len(cats))
	for _, c := range cats {
		rows = append(rows, db.Category{
			ID: c.ID, Name: c.Name, ParentID: idByName[c.Parent],
			Alias: c.Alias, OtherMedia: c.OtherMedia, Position: c.Position,
		})
	}
	return db.ReplaceCategories(ctx, pool, rows)
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Name     string `json:"name"`     // the new leaf folder name
		Alias    string `json:"alias"`    // optional display alias
		ParentID int64  `json:"parentId"` // 0 = top level
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := library.ValidName(req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dataDir := s.dataDir()
	cats, err := library.List(dataDir)
	if err != nil {
		http.Error(w, "could not read categories", http.StatusInternalServerError)
		return
	}
	// Global leaf-name uniqueness: the new leaf must not match any existing category's
	// leaf anywhere in the tree, so indented labels and dropdowns are never ambiguous.
	parentRel := ""
	for _, c := range cats {
		if strings.EqualFold(c.Leaf, req.Name) {
			http.Error(w, "a category named "+req.Name+" already exists", http.StatusBadRequest)
			return
		}
		if req.ParentID != 0 && c.ID == req.ParentID {
			parentRel = c.Name
		}
	}
	if req.ParentID != 0 && parentRel == "" {
		http.Error(w, "unknown parent category", http.StatusBadRequest)
		return
	}
	alias := strings.TrimSpace(req.Alias)
	if alias == "" {
		alias = req.Name
	}
	relName := req.Name
	if parentRel != "" {
		relName = parentRel + "/" + req.Name
	}
	// Append after the existing siblings: a new category sorts last in its group.
	nextPos := 0
	for _, c := range cats {
		if c.Parent == parentRel && c.Position >= nextPos {
			nextPos = c.Position + 1
		}
	}
	// The id is minted by the cache so it is unique and monotonic; it is then stored
	// in config.json, which remains the source of truth.
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	id, err := db.InsertCategory(r.Context(), pool, relName, alias, req.ParentID)
	if err != nil {
		http.Error(w, "could not create category", http.StatusInternalServerError)
		return
	}
	cat, err := library.Create(dataDir, parentRel, req.Name, alias, id, nextPos)
	if err != nil {
		s.bestEffort(db.DeleteCategory(r.Context(), pool, relName), "delete orphan category row")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.bestEffort(s.mirrorCategories(r.Context(), pool), "mirror categories") // fix parent ids + effective flag
	writeJSON(w, categoryDTO{
		ID: cat.ID, Name: cat.Name, Leaf: cat.Leaf, Alias: cat.Alias,
		ParentID: req.ParentID, OtherMedia: cat.OtherMedia, Position: cat.Position, Empty: cat.Empty,
	})
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	// Filesystem is the source of truth: delete it first, then re-mirror the cache (the
	// removed row is simply absent from the fresh scan).
	if err := library.Delete(s.dataDir(), name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if pool, err := s.ensureDB(r.Context()); err == nil {
		s.bestEffort(s.mirrorCategories(r.Context(), pool), "mirror categories")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetAlias(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	req, err := decodeJSON[struct {
		Alias      string `json:"alias"`
		OtherMedia bool   `json:"otherMedia"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// The other-media flag is meaningful only on a top-level category; a sub-category
	// inherits the root's flag and never stores its own.
	dataDir := s.dataDir()
	cats, err := library.List(dataDir)
	if err != nil {
		http.Error(w, "could not read categories", http.StatusInternalServerError)
		return
	}
	topLevel := false
	found := false
	for _, c := range cats {
		if c.Name == name {
			found, topLevel = true, c.Parent == ""
			break
		}
	}
	if !found {
		http.Error(w, "no category named "+name, http.StatusBadRequest)
		return
	}
	otherMedia := req.OtherMedia && topLevel
	if err := library.SetAlias(dataDir, name, req.Alias, otherMedia); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Re-mirror so an other-media toggle re-propagates the effective flag across the
	// whole subtree's cache rows.
	if pool, err := s.ensureDB(r.Context()); err == nil {
		s.bestEffort(s.mirrorCategories(r.Context(), pool), "mirror categories")
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleReorderCategories renumbers one parent's children to the given order. The request
// carries a parent id and the full ordered list of that parent's child ids; the order must
// be exactly that sibling set (no missing, extra, or duplicate ids), which confines a
// reorder to a single level - a category can never move to another parent this way. Each
// child's config.json gets its new dense position, then the cache is re-mirrored.
func (s *Server) handleReorderCategories(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		ParentID int64   `json:"parentId"` // 0 = top level
		Order    []int64 `json:"order"`    // child ids in their new order
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	dataDir := s.dataDir()
	cats, err := library.List(dataDir)
	if err != nil {
		http.Error(w, "could not read categories", http.StatusInternalServerError)
		return
	}
	idByName := make(map[string]int64, len(cats))
	for _, c := range cats {
		idByName[c.Name] = c.ID
	}
	// Collect the parent's actual children and index them by id, so we can both validate the
	// request against the real sibling set and resolve each id back to its relpath.
	siblings := map[int64]library.Category{}
	for _, c := range cats {
		if idByName[c.Parent] == req.ParentID {
			siblings[c.ID] = c
		}
	}
	if len(req.Order) != len(siblings) {
		http.Error(w, "order must list exactly this parent's children", http.StatusBadRequest)
		return
	}
	seen := map[int64]bool{}
	for _, id := range req.Order {
		if _, ok := siblings[id]; !ok || seen[id] {
			http.Error(w, "order must list exactly this parent's children", http.StatusBadRequest)
			return
		}
		seen[id] = true
	}
	for pos, id := range req.Order {
		if err := library.SetPosition(dataDir, siblings[id].Name, pos); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if pool, err := s.ensureDB(r.Context()); err == nil {
		s.bestEffort(s.mirrorCategories(r.Context(), pool), "mirror categories")
	}
	w.WriteHeader(http.StatusNoContent)
}
