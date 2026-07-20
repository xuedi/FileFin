package server

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strings"

	"filefin/internal/db"
	"filefin/internal/library"
	"filefin/internal/logging"
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
	// The markers summary the library list shows at a glance: the kind of media this
	// category takes, and how much past imports have taught it.
	Kind    string `json:"kind"`
	Learned int    `json:"learned"`
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
		kind := c.Markers.Kind
		if kind == "" {
			kind = library.KindBoth
		}
		out = append(out, categoryDTO{
			ID: c.ID, Name: c.Name, Leaf: c.Leaf, Alias: c.Alias,
			ParentID: idByName[c.Parent], OtherMedia: c.OtherMedia, Position: c.Position, Empty: c.Empty,
			Kind: kind, Learned: len(c.Markers.Learned),
		})
	}
	return out
}

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := library.List(s.dataDir())
	if err != nil {
		s.logger().For(logging.Backend).Error("could not list categories", logging.Fields{"error": err.Error()})
		http.Error(w, "could not read categories", http.StatusInternalServerError)
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

// markersDTO is the wire shape of a category's markers. Learned is optional on a write:
// absent means "leave what the imports have taught alone", present replaces it, which is
// how the category page removes a learned marker that went wrong.
type markersDTO struct {
	Kind      string         `json:"kind"`
	Languages []string       `json:"languages"`
	Countries []string       `json:"countries"`
	Keywords  []string       `json:"keywords"`
	Learned   map[string]int `json:"learned,omitempty"`
}

// learnedMarker is one row of the category page's "Learned from imports" table: what was
// seen, how often it landed here, and which other categories have also seen it - the
// context that says whether the marker is a reliable pointer or a shared one.
type learnedMarker struct {
	Marker string   `json:"marker"`
	Count  int      `json:"count"`
	AlsoIn []string `json:"alsoIn"`
}

// categoryDetail is the category page's payload: the identity, what belongs here, what has
// been learned, and what the category holds.
type categoryDetail struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Leaf       string          `json:"leaf"`
	Alias      string          `json:"alias"`
	Parent     string          `json:"parent"`
	TopLevel   bool            `json:"topLevel"`
	OtherMedia bool            `json:"otherMedia"` // this category's own flag
	Inherited  bool            `json:"inherited"`  // the flag a sub-category inherits
	Empty      bool            `json:"empty"`      // safe to delete
	HasSubs    bool            `json:"hasSubs"`    // holds sub-categories
	Media      int             `json:"media"`
	Files      int             `json:"files"`
	Markers    markersDTO      `json:"markers"`
	Learned    []learnedMarker `json:"learned"`
}

// handleCategoryDetail serves one category's page: its identity, its markers, and the
// learned markers with the other categories that have seen each of them.
func (s *Server) handleCategoryDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cats, err := library.List(s.dataDir())
	if err != nil {
		http.Error(w, "could not read categories", http.StatusInternalServerError)
		return
	}
	var cat library.Category
	found := false
	for _, c := range cats {
		if c.Name == name {
			cat, found = c, true
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	d := categoryDetail{
		ID: cat.ID, Name: cat.Name, Leaf: cat.Leaf, Alias: cat.Alias, Parent: cat.Parent,
		TopLevel: cat.Parent == "", OtherMedia: cat.OtherMedia, Empty: cat.Empty,
		Markers: markersDTO{
			Kind: cat.Markers.Kind, Languages: cat.Markers.Languages,
			Countries: cat.Markers.Countries, Keywords: cat.Markers.Keywords,
		},
		Learned: learnedMarkers(cat, cats),
	}
	if d.Markers.Kind == "" {
		d.Markers.Kind = library.KindBoth
	}
	for _, c := range cats {
		if c.Parent == cat.Name {
			d.HasSubs = true
		}
		if !d.TopLevel && c.Name == rootOf(cat, cats) {
			d.Inherited = c.OtherMedia
		}
	}
	if pool, err := s.ensureDB(r.Context()); err == nil {
		if counts, err := db.MediaCountsByCategory(r.Context(), pool); err == nil {
			d.Media, d.Files = counts[cat.ID].Media, counts[cat.ID].Files
		}
	}
	writeJSON(w, d)
}

// rootOf returns the relpath of the top-level category a category descends from - the one
// whose other-media flag the whole subtree inherits.
func rootOf(cat library.Category, cats []library.Category) string {
	byName := make(map[string]library.Category, len(cats))
	for _, c := range cats {
		byName[c.Name] = c
	}
	seen := map[string]bool{}
	for cat.Parent != "" && !seen[cat.Name] {
		seen[cat.Name] = true
		parent, ok := byName[cat.Parent]
		if !ok {
			break
		}
		cat = parent
	}
	return cat.Name
}

// learnedMarkers turns a category's learned counts into page rows, strongest first, each
// naming the other categories that have also been fed that marker.
func learnedMarkers(cat library.Category, cats []library.Category) []learnedMarker {
	out := make([]learnedMarker, 0, len(cat.Markers.Learned))
	for marker, count := range cat.Markers.Learned {
		row := learnedMarker{Marker: marker, Count: count, AlsoIn: []string{}}
		for _, other := range cats {
			if other.Name != cat.Name && other.Markers.Learned[marker] > 0 {
				row.AlsoIn = append(row.AlsoIn, other.Alias)
			}
		}
		sort.Strings(row.AlsoIn)
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Marker < out[j].Marker
	})
	return out
}

// handleUpdateCategory is the single write path for a category: the alias, the other-media
// flag, and the markers all arrive in one PUT. A markers section left out keeps the stored
// markers untouched, so a caller that only renames cannot wipe what imports have taught.
func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	req, err := decodeJSON[struct {
		Alias      string      `json:"alias"`
		OtherMedia bool        `json:"otherMedia"`
		Markers    *markersDTO `json:"markers"`
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
	var cur library.Category
	for _, c := range cats {
		if c.Name == name {
			cur, found, topLevel = c, true, c.Parent == ""
			break
		}
	}
	if !found {
		http.Error(w, "no category named "+name, http.StatusNotFound)
		return
	}
	otherMedia := req.OtherMedia && topLevel
	if err := library.SetAlias(dataDir, name, req.Alias, otherMedia); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Markers != nil {
		m := library.Markers{
			Kind: req.Markers.Kind, Languages: req.Markers.Languages,
			Countries: req.Markers.Countries, Keywords: req.Markers.Keywords,
			Learned: req.Markers.Learned,
		}
		if m.Kind == library.KindBoth {
			m.Kind = ""
		}
		if m.Learned == nil {
			m.Learned = cur.Markers.Learned
		}
		if err := library.SetMarkers(dataDir, name, m); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
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
