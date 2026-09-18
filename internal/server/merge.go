package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"filefin/internal/config"
	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/omdb"
	"filefin/internal/sources"
	"filefin/internal/thumbnail"
)

const (
	// omdbSnapshotsPerDay caps the OMDb lookups spent recording snapshots of items that are
	// already matched. A free OMDb key allows 1000 requests a day, and new items and re-matches
	// need their share, so a large library is caught up over a few days instead of in one burst.
	omdbSnapshotsPerDay = 200

	// snapshotRetryInterval is how long a failed lookup stands before it is tried again.
	snapshotRetryInterval = 14 * 24 * time.Hour
)

// metadataRules returns the library-wide source rules under lock.
func (s *Server) metadataRules() map[string][]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return nil
	}
	return s.cfg.RuleOrders()
}

// resolveMeta merges an item's source snapshots into its top-level fields under the rules.
func (s *Server) resolveMeta(m importer.Meta) (importer.Meta, []sources.Row) {
	return sources.Resolve(m, s.metadataRules())
}

// withSnapshot sets one source's snapshot on a (possibly nil) map without touching the
// caller's map.
func withSnapshot(in map[string]*importer.Snapshot, src string, snap *importer.Snapshot) map[string]*importer.Snapshot {
	out := make(map[string]*importer.Snapshot, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	out[src] = snap
	return out
}

// manualChoices keeps only the fields pinned to their hand-typed value.
func manualChoices(in map[string]string) map[string]string {
	var out map[string]string
	for k, v := range in {
		if v == sources.ChoiceManual {
			if out == nil {
				out = map[string]string{}
			}
			out[k] = v
		}
	}
	return out
}

// dropChoicesFor unpins every field pinned to one source, for when that source's record is
// replaced by a different match.
func dropChoicesFor(in map[string]string, src string) map[string]string {
	var out map[string]string
	for k, v := range in {
		if v != src {
			if out == nil {
				out = map[string]string{}
			}
			out[k] = v
		}
	}
	return out
}

// needsSnapshot reports whether an item lacks a current record from one source: none yet, a
// record of another IMDb id than the item has now, or a failed lookup old enough to retry.
func needsSnapshot(meta importer.Meta, src string, now time.Time) bool {
	snap := meta.Sources[src]
	imdb := meta.Metadata["imdbID"]
	switch {
	case snap == nil:
		return true
	case snap.Error != "":
		return now.Sub(time.Unix(snap.Fetched, 0)) > snapshotRetryInterval
	}
	return imdb != "" && snap.ImdbID != "" && snap.ImdbID != imdb
}

// omdbBackfillRemaining is how many OMDb snapshot lookups today's budget still allows.
func (s *Server) omdbBackfillRemaining() int {
	s.omdbBudgetMu.Lock()
	defer s.omdbBudgetMu.Unlock()
	s.rollOMDbBudget()
	return omdbSnapshotsPerDay - s.omdbBudgetUsed
}

// spendOMDbBackfill counts one OMDb snapshot lookup against today's budget.
func (s *Server) spendOMDbBackfill() {
	s.omdbBudgetMu.Lock()
	defer s.omdbBudgetMu.Unlock()
	s.rollOMDbBudget()
	s.omdbBudgetUsed++
}

func (s *Server) rollOMDbBudget() {
	if day := time.Now().Format("2006-01-02"); day != s.omdbBudgetDay {
		s.omdbBudgetDay, s.omdbBudgetUsed = day, 0
	}
}

// queueOMDbSnapshots queues an OMDb lookup by IMDb id for matched items without a current
// OMDb snapshot, as many as today's budget still allows. It returns how many it queued.
func (s *Server) queueOMDbSnapshots(ctx context.Context, pool *sql.DB) int {
	remaining := s.omdbBackfillRemaining()
	if remaining <= 0 || s.omdbClient() == nil {
		return 0
	}
	media, err := db.AllMedia(ctx, pool)
	if err != nil {
		return 0
	}
	now := time.Now()
	queued := 0
	for _, m := range media {
		if queued >= remaining {
			break
		}
		meta, err := importer.ReadMeta(m.FolderPath)
		if err != nil || meta.Metadata["imdbID"] == "" || !needsSnapshot(meta, sources.OMDb, now) {
			continue
		}
		if db.UpsertPendingEnrich(ctx, pool, m.ID) == nil {
			queued++
		}
	}
	return queued
}

// conflictItem is one row of the Conflict report: an item whose sources disagree.
type conflictItem struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Year     int      `json:"year"`
	Folder   string   `json:"folder"`
	Category string   `json:"category"`
	Fields   []string `json:"fields"`
	Identity bool     `json:"identity"`
}

// conflictItems is the report: every item whose sources disagree on a field no pick or rule
// has settled, the ones that probably describe different works first. It is derived from
// meta.json on every read, so it can never go stale against the files or the rules.
func (s *Server) conflictItems(ctx context.Context, pool *sql.DB) ([]conflictItem, error) {
	media, err := db.ListMediaWithCategory(ctx, pool)
	if err != nil {
		return nil, err
	}
	rules := s.metadataRules()
	out := []conflictItem{}
	for _, m := range media {
		meta, err := importer.ReadMeta(m.Path)
		if err != nil || len(sources.Present(meta)) < 2 {
			continue
		}
		_, rows := sources.Resolve(meta, rules)
		conflicts := sources.Conflicts(rows)
		if len(conflicts) == 0 {
			continue
		}
		it := conflictItem{ID: m.ID, Title: m.Title, Year: m.Year, Folder: filepath.Base(m.Path), Category: m.Category}
		for _, c := range conflicts {
			it.Fields = append(it.Fields, strings.ToLower(c.Label))
			it.Identity = it.Identity || c.Class == sources.ClassIdentity
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Identity && !out[j].Identity })
	return out, nil
}

// handleConflicts lists the items whose metadata sources disagree.
func (s *Server) handleConflicts(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	items, err := s.conflictItems(r.Context(), pool)
	if err != nil {
		http.Error(w, "could not list conflicts", http.StatusInternalServerError)
		return
	}
	writeJSON(w, struct {
		Items []conflictItem `json:"items"`
	}{items})
}

// mergeSource is one source column of the merge view.
type mergeSource struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	ImdbID  string `json:"imdbId"`
	Fetched string `json:"fetched"`
	Error   string `json:"error"`
	Poster  string `json:"poster"` // same-origin URL of the source's poster, "" when it has none
}

// mergeView is the merge page: the item, its sources, and every field row.
type mergeView struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Year      int           `json:"year"`
	Folder    string        `json:"folder"`
	Category  string        `json:"category"`
	HasPoster bool          `json:"hasPoster"`
	Sources   []mergeSource `json:"sources"`
	Rows      []sources.Row `json:"rows"`
}

// handleMergeView returns one item's merge page.
func (s *Server) handleMergeView(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	m, err := db.GetMedia(r.Context(), pool, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	meta, err := importer.ReadMeta(m.Path)
	if err != nil {
		http.Error(w, "meta.json is unreadable", http.StatusConflict)
		return
	}
	cat, _ := db.CategoryName(r.Context(), pool, m.CategoryID)
	_, rows := s.resolveMeta(meta)
	v := mergeView{
		ID: m.ID, Title: m.Title, Year: m.Year, Folder: filepath.Base(m.Path), Category: cat,
		HasPoster: m.Poster != "", Sources: []mergeSource{}, Rows: rows,
	}
	for _, src := range sources.Order {
		snap := meta.Sources[src]
		if snap == nil {
			continue
		}
		v.Sources = append(v.Sources, mergeSource{
			Name: src, Label: sources.Label(src), ID: snap.ID, Kind: snap.Kind, ImdbID: snap.ImdbID,
			Fetched: fmtUnix(snap.Fetched), Error: snap.Error, Poster: sourcePosterURL(src, snap),
		})
	}
	writeJSON(w, v)
}

// sourcePosterURL is the same-origin proxy URL of a source's poster.
func sourcePosterURL(src string, snap *importer.Snapshot) string {
	if !snap.Usable() || snap.Poster == "" {
		return ""
	}
	switch src {
	case sources.OMDb:
		return "/api/admin/omdb/poster/" + url.PathEscape(snap.ImdbID)
	case sources.TMDb:
		return "/api/admin/tmdb/poster?size=w185&path=" + url.QueryEscape(snap.Poster)
	}
	return ""
}

// handleApplyMerge writes the admin's picks: each picked field is pinned for this item, the
// remembered ones become library rules, and a picked poster replaces the folder's. It answers
// with the next item still in conflict, so a queue of them can be worked through.
func (s *Server) handleApplyMerge(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	m, err := db.GetMedia(ctx, pool, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	req, err := decodeJSON[struct {
		Picks    map[string]string `json:"picks"`
		Remember []string          `json:"remember"`
		Poster   string            `json:"poster"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	meta, err := importer.ReadMeta(m.Path)
	if err != nil {
		http.Error(w, "meta.json is unreadable", http.StatusConflict)
		return
	}
	present := sources.Present(meta)
	for field, pick := range req.Picks {
		f, ok := sources.FieldByKey(field)
		if !ok || !f.Pickable || !validPick(f, pick, present) {
			http.Error(w, "cannot pick "+pick+" for "+field, http.StatusBadRequest)
			return
		}
	}
	rules := map[string]config.MetadataRule{}
	now := time.Now().Unix()
	for _, field := range req.Remember {
		pick := req.Picks[field]
		if !slices.Contains(present, pick) {
			continue // only a source can be a rule; "keep" and "union" stay with this item
		}
		order := []string{pick}
		for _, src := range sources.Order {
			if src != pick {
				order = append(order, src)
			}
		}
		rules[field] = config.MetadataRule{Order: order, Set: now}
	}
	if len(rules) > 0 {
		if _, ok := s.mutateConfig(w, func(c *config.Config) {
			merged := make(map[string]config.MetadataRule, len(c.MetadataRules)+len(rules))
			for k, v := range c.MetadataRules {
				merged[k] = v
			}
			for k, v := range rules {
				merged[k] = v
			}
			c.MetadataRules = merged
		}); !ok {
			return
		}
	}

	written, err := s.metaMgr.Update(m.Path, func(cur importer.Meta) importer.Meta {
		choices := make(map[string]string, len(cur.Choices)+len(req.Picks))
		for k, v := range cur.Choices {
			choices[k] = v
		}
		for k, v := range req.Picks {
			choices[k] = v
		}
		cur.Choices = choices
		out, _ := s.resolveMeta(cur)
		return out
	})
	if err != nil {
		http.Error(w, "could not save the merge", http.StatusInternalServerError)
		return
	}
	posterRel := folderPoster(m.Path)
	if req.Poster != "" {
		if !slices.Contains(present, req.Poster) {
			http.Error(w, "cannot take the poster from "+req.Poster, http.StatusBadRequest)
			return
		}
		if name := s.fetchSourcePoster(ctx, m.Path, req.Poster, written.Sources[req.Poster]); name != "" {
			posterRel = name
		}
	}
	if err := s.writeMediaCacheRow(ctx, pool, m.ID, written.Title, written.Year, written, posterRel); err != nil {
		http.Error(w, "could not save the merge", http.StatusInternalServerError)
		return
	}
	fields := logging.Fields{"id": m.ID, "picks": len(req.Picks), "rules": len(rules)}
	s.elog().Info(userFrom(r)+" merged metadata for "+written.Title, fields)
	if len(rules) > 0 {
		s.reapplyRules()
	}

	next := ""
	if items, err := s.conflictItems(ctx, pool); err == nil {
		for _, it := range items {
			if it.ID != m.ID {
				next = it.ID
				break
			}
		}
	}
	writeJSON(w, struct {
		Next string `json:"next"`
	}{next})
}

// validPick reports whether a pick names something the field can take: a source that has a
// value for it, the union of a list, or the value it already has.
func validPick(f sources.Field, pick string, present []string) bool {
	switch pick {
	case sources.ChoiceManual:
		return true
	case sources.ChoiceUnion:
		return f.List
	}
	return slices.Contains(present, pick)
}

// fetchSourcePoster downloads a source's poster into the folder, replacing the current one
// and its sized variants (the thumbnail agent rebuilds them). It returns the new base name,
// or "" when the download failed and the old poster stays.
func (s *Server) fetchSourcePoster(ctx context.Context, dir, src string, snap *importer.Snapshot) string {
	if !snap.Usable() || snap.Poster == "" {
		return ""
	}
	var img []byte
	ext := ".jpg"
	switch src {
	case sources.OMDb:
		client := s.omdbClient()
		if client == nil {
			return ""
		}
		data, ct, err := client.Poster(ctx, snap.ImdbID, 600)
		if err != nil {
			return ""
		}
		img, ext = data, omdb.PosterExt(ct)
	case sources.TMDb:
		client := s.tmdbClient()
		if client == nil {
			return ""
		}
		data, err := client.Poster(ctx, "w780", snap.Poster)
		if err != nil {
			return ""
		}
		img, ext = data, imageExtFromCT(http.DetectContentType(data))
	}
	if len(img) == 0 || ext == "" {
		return ""
	}
	return replaceFolderPoster(dir, img, ext)
}

// replaceFolderPoster writes a new base poster and drops the old one and its sized variants.
func replaceFolderPoster(dir string, img []byte, ext string) string {
	old := folderPoster(dir)
	name := "poster" + ext
	if os.WriteFile(filepath.Join(dir, name), img, 0o644) != nil {
		return ""
	}
	if old != "" && old != name {
		_ = os.Remove(filepath.Join(dir, old))
	}
	_ = os.Remove(filepath.Join(dir, thumbnail.DetailName()))
	_ = os.Remove(filepath.Join(dir, thumbnail.TileName()))
	return name
}

// reapplyRules re-resolves every item with source snapshots after the rules changed, in the
// background. A change while a pass runs schedules one more pass rather than a second one
// alongside it.
func (s *Server) reapplyRules() {
	if !s.reapplying.CompareAndSwap(false, true) {
		s.reapplyAgain.Store(true)
		return
	}
	go func() {
		defer s.reapplying.Store(false)
		for {
			s.reapplyRulesPass(context.Background())
			if !s.reapplyAgain.Swap(false) {
				return
			}
		}
	}()
}

func (s *Server) reapplyRulesPass(ctx context.Context) {
	pool, err := s.ensureDB(ctx)
	if err != nil {
		return
	}
	media, err := db.AllMedia(ctx, pool)
	if err != nil {
		return
	}
	changed := 0
	for _, m := range media {
		meta, err := importer.ReadMeta(m.FolderPath)
		if err != nil || len(sources.Present(meta)) == 0 {
			continue
		}
		if resolved, _ := s.resolveMeta(meta); reflect.DeepEqual(resolved, meta) {
			continue
		}
		written, err := s.metaMgr.Update(m.FolderPath, func(cur importer.Meta) importer.Meta {
			out, _ := s.resolveMeta(cur)
			return out
		})
		if err != nil {
			continue
		}
		s.bestEffort(s.writeMediaCacheRow(ctx, pool, m.ID, written.Title, written.Year, written, folderPoster(m.FolderPath)), "mirror re-applied rules")
		changed++
	}
	s.elog().Info("re-applied metadata rules", logging.Fields{"changed": changed})
}

// ruleView is one library rule as the Settings page lists it.
type ruleView struct {
	Field string   `json:"field"`
	Label string   `json:"label"`
	Order []string `json:"order"`
	Set   string   `json:"set"`
}

// ruleViews lists the rules for the Settings page, in the merge view's field order.
func ruleViews(cfg *config.Config) []ruleView {
	out := []ruleView{}
	for _, f := range sources.Fields {
		r, ok := cfg.MetadataRules[f.Key]
		if !ok {
			continue
		}
		labels := make([]string, 0, len(r.Order))
		for _, src := range r.Order {
			labels = append(labels, sources.Label(src))
		}
		out = append(out, ruleView{Field: f.Key, Label: f.Label, Order: labels, Set: fmtUnix(r.Set)})
	}
	return out
}

// handleDeleteRule removes one field's library rule and re-resolves the library without it.
func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	field := r.PathValue("field")
	cfg, ok := s.mutateConfig(w, func(c *config.Config) {
		kept := make(map[string]config.MetadataRule, len(c.MetadataRules))
		for k, v := range c.MetadataRules {
			if k != field {
				kept[k] = v
			}
		}
		c.MetadataRules = kept
	})
	if !ok {
		return
	}
	s.reapplyRules()
	writeJSON(w, s.settingsPayload(cfg))
}
