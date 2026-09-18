package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/sources"
	"filefin/internal/tmdb"
	"filefin/internal/watchlist"
)

const (
	tmdbAgentName    = "TMDB"
	tmdbRestInterval = 1 * time.Second
	tmdbIdlePoll     = 5 * time.Second
)

// The TMDb matcher keeps a TMDb snapshot beside the OMDb one in every item's meta.json: by
// the IMDb id when the item has one, by a year-strict title search when it has none. The
// merge (see merge.go and the sources package) then fills and cross-checks the item's fields.

// needsTMDb reports whether an item should be matched on TMDb.
func needsTMDb(meta importer.Meta, now time.Time) bool {
	return needsSnapshot(meta, sources.TMDb, now)
}

// refillTMDb queues a TMDb task for every item without a current TMDb snapshot (other-media
// categories excluded) and prunes the rest. Nothing is queued without a TMDb key.
func (s *Server) refillTMDb(ctx context.Context, pool *sql.DB) (int, error) {
	if s.tmdbClient() == nil {
		return 0, nil
	}
	media, err := db.ListMediaWithCategory(ctx, pool)
	if err != nil {
		return 0, err
	}
	flags, err := db.CategoryFlags(ctx, pool)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	queued, failed := 0, 0
	for _, m := range media {
		meta, err := importer.ReadMeta(m.Path)
		if err == nil && !flags[m.CategoryID] && needsTMDb(meta, now) {
			if err := db.UpsertPendingTMDb(ctx, pool, m.ID); err != nil {
				failed++
				continue
			}
			queued++
		} else {
			s.bestEffort(db.PruneTMDb(ctx, pool, m.ID), "prune tmdb task")
		}
	}
	if failed > 0 {
		s.elog().Error("some TMDb tasks could not be queued", logging.Fields{"failed": failed})
	}
	return queued, nil
}

// handleTMDbScan is the manual queue refill behind "Re-scan metadata (TMDb)".
func (s *Server) handleTMDbScan(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.tmdbClient() == nil {
		http.Error(w, "set a TMDb API key first", http.StatusConflict)
		return
	}
	ctx := r.Context()
	queued, err := s.refillTMDb(ctx, pool)
	if err != nil {
		http.Error(w, "could not scan for TMDb work", http.StatusInternalServerError)
		return
	}
	pending, err := db.CountPendingTMDb(ctx, pool)
	if err != nil {
		http.Error(w, "could not count TMDb tasks", http.StatusInternalServerError)
		return
	}
	s.elog().Info("TMDb scan queued work", logging.Fields{"candidates": queued, "pending": pending})
	writeJSON(w, scanResult{Candidates: queued, Pending: pending})
}

// handleActiveTMDb returns the in-flight TMDb matches and the count still waiting.
func (s *Server) handleActiveTMDb(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	active, err := db.ListActiveTMDb(ctx, pool)
	if err != nil {
		http.Error(w, "could not list TMDb tasks", http.StatusInternalServerError)
		return
	}
	pending, err := db.CountPendingTMDb(ctx, pool)
	if err != nil {
		http.Error(w, "could not count TMDb tasks", http.StatusInternalServerError)
		return
	}
	writeJSON(w, queueStatus[db.ActiveTMDb]{Active: active, Pending: pending})
}

// startTMDbAgent launches the single TMDb matching agent once per process.
func (s *Server) startTMDbAgent() {
	s.tmdbStart.Do(func() { go s.tmdbLoop() })
}

// tmdbLoop drains the TMDb queue one item at a time, idling without work, config, or key.
func (s *Server) tmdbLoop() {
	ctx := context.Background()
	recovered := false
	for {
		s.mu.RLock()
		installed := s.cfg != nil && s.cfg.SetupComplete()
		s.mu.RUnlock()
		client := s.tmdbClient()
		if !installed || client == nil {
			time.Sleep(tmdbIdlePoll)
			continue
		}
		pool, err := s.ensureDB(ctx)
		if err != nil {
			time.Sleep(tmdbIdlePoll)
			continue
		}
		if !recovered {
			_ = db.ResetMatchingToPending(ctx, pool)
			recovered = true
		}
		task, ok, err := db.ClaimNextTMDb(ctx, pool, tmdbAgentName)
		if err != nil || !ok {
			time.Sleep(tmdbIdlePoll)
			continue
		}
		s.tmdbOne(ctx, pool, client, task)
		time.Sleep(tmdbRestInterval)
	}
}

// tmdbOne matches one item on TMDb and records the snapshot. An IMDb id resolves directly; an
// item without one is searched by title and year and taken only on an exact or confident
// match. A miss is recorded as a failed snapshot (retried after a while); an API or network
// failure fails the task for the next scan to retry.
func (s *Server) tmdbOne(ctx context.Context, pool *sql.DB, client *tmdb.Client, task db.TMDbTask) {
	m, err := db.GetMedia(ctx, pool, task.MediaID)
	if err != nil {
		_ = db.FailTMDb(ctx, pool, task.ID, "media not found")
		return
	}
	meta, err := importer.ReadMeta(m.Path)
	if err != nil {
		_ = db.FailTMDb(ctx, pool, task.ID, "meta.json unreadable")
		return
	}
	now := time.Now().Unix()
	imdb := meta.Metadata["imdbID"]
	var snap *importer.Snapshot
	var title tmdb.Title
	if imdb != "" {
		title, err = client.FindByIMDb(ctx, imdb)
		if errors.Is(err, tmdb.ErrNotFound) {
			snap = &importer.Snapshot{ImdbID: imdb, Fetched: now, Error: "not on TMDb"}
		}
	} else {
		files, _ := db.MediaFiles(ctx, pool, m.ID)
		title, err = s.searchTMDb(ctx, client, m, files)
		if errors.Is(err, tmdb.ErrNotFound) {
			snap = &importer.Snapshot{Fetched: now, Error: "no confident match on TMDb"}
		}
	}
	if snap == nil {
		if err != nil {
			_ = db.FailTMDb(ctx, pool, task.ID, err.Error())
			s.elog().Info("TMDb lookup failed for "+m.Title, logging.Fields{"id": m.ID, "error": err.Error()})
			return
		}
		d, err := client.Details(ctx, title.Kind, title.ID)
		if err != nil {
			_ = db.FailTMDb(ctx, pool, task.ID, err.Error())
			return
		}
		snap = sources.FromTMDb(d, now)
		if imdb != "" && snap.ImdbID == "" {
			snap.ImdbID = imdb // found by it, so it is the record of that id
		}
	}
	written, err := s.writeTMDbSnapshot(m.Path, snap)
	if err != nil {
		_ = db.FailTMDb(ctx, pool, task.ID, "write meta: "+err.Error())
		return
	}
	posterRel := folderPoster(m.Path)
	if posterRel == "" && snap.Usable() {
		posterRel = s.fetchSourcePoster(ctx, m.Path, sources.TMDb, snap)
	}
	s.bestEffort(s.writeMediaCacheRow(ctx, pool, m.ID, written.Title, written.Year, written, posterRel), "mirror tmdb match")
	_ = db.FinishTMDb(ctx, pool, task.ID)
	if snap.Error != "" {
		s.elog().Info("no TMDb match for "+m.Title, logging.Fields{"id": m.ID, "reason": snap.Error})
		return
	}
	s.elog().Info("matched "+m.Title+" on TMDb", logging.Fields{"id": m.ID, "tmdb": snap.ID, "imdbID": snap.ImdbID})
}

// writeTMDbSnapshot stores a TMDb snapshot and re-merges. A record of a different TMDb title
// than before unpins the fields pinned to TMDb; a usable record marks the item matched.
func (s *Server) writeTMDbSnapshot(dir string, snap *importer.Snapshot) (importer.Meta, error) {
	return s.metaMgr.Update(dir, func(cur importer.Meta) importer.Meta {
		if old := cur.Sources[sources.TMDb]; old != nil && old.ID != snap.ID {
			cur.Choices = dropChoicesFor(cur.Choices, sources.TMDb)
		}
		cur.Sources = withSnapshot(cur.Sources, sources.TMDb, snap)
		if snap.Usable() {
			cur.Enriched = true
		}
		out, _ := s.resolveMeta(cur)
		return out
	})
}

// searchTMDb finds an item without an IMDb id by its title and year. A folder of episodes is
// a series, anything else a movie. Only an exact or confident pairing by the watch-history
// matcher counts (a unique title with a year off by at most one); anything vaguer is
// ErrNotFound, because a wrong match is worse than none.
func (s *Server) searchTMDb(ctx context.Context, client *tmdb.Client, m db.Media, files []db.MediaFile) (tmdb.Title, error) {
	kind := tmdb.KindMovie
	for _, f := range files {
		if f.Episode > 0 {
			kind = tmdb.KindTV
			break
		}
	}
	results, err := client.Search(ctx, m.Title, 0, kind)
	if err != nil {
		return tmdb.Title{}, err
	}
	lib := make([]watchlist.LibraryItem, 0, 2*len(results))
	for _, r := range results {
		id := r.Kind + "/" + strconv.Itoa(r.ID)
		lib = append(lib, watchlist.LibraryItem{ID: id, Title: r.Title, Year: r.Year})
		if r.OriginalTitle != "" && r.OriginalTitle != r.Title {
			lib = append(lib, watchlist.LibraryItem{ID: id, Title: r.OriginalTitle, Year: r.Year})
		}
	}
	match := watchlist.MatchLibrary([]watchlist.Entry{{Title: m.Title, Year: m.Year}}, lib)
	if len(match) == 0 || match[0].Item == nil ||
		(match[0].Confidence != watchlist.ConfidenceExact && match[0].Confidence != watchlist.ConfidenceConfident) {
		return tmdb.Title{}, tmdb.ErrNotFound
	}
	k, idStr, _ := strings.Cut(match[0].Item.ID, "/")
	id, _ := strconv.Atoi(idStr)
	return tmdb.Title{Kind: k, ID: id, Name: match[0].Item.Title}, nil
}

// tmdbCandidate is one TMDb search hit offered in the manual match view.
type tmdbCandidate struct {
	Kind          string `json:"kind"`
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"originalTitle"`
	Year          int    `json:"year"`
	Poster        string `json:"poster"`
	Overview      string `json:"overview"`
}

// handleTMDbSearch returns TMDb candidates for the manual match view.
func (s *Server) handleTMDbSearch(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, err := db.GetMedia(r.Context(), pool, r.PathValue("id")); err != nil {
		http.NotFound(w, r)
		return
	}
	client := s.tmdbClient()
	if client == nil {
		http.Error(w, "TMDb API key is not configured", http.StatusBadRequest)
		return
	}
	req, err := decodeJSON[struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		Kind  string `json:"kind"`
	}](w, r)
	if err != nil || strings.TrimSpace(req.Title) == "" {
		http.Error(w, "a title is required", http.StatusBadRequest)
		return
	}
	kind := ""
	switch req.Kind {
	case "movie":
		kind = tmdb.KindMovie
	case "series":
		kind = tmdb.KindTV
	}
	res, err := client.Search(r.Context(), strings.TrimSpace(req.Title), req.Year, kind)
	if err != nil {
		http.Error(w, "could not reach TMDb", http.StatusBadGateway)
		return
	}
	out := []tmdbCandidate{}
	for _, c := range res {
		cand := tmdbCandidate{
			Kind: sourceKind(c.Kind), ID: c.ID, Title: c.Title, OriginalTitle: c.OriginalTitle,
			Year: c.Year, Overview: c.Overview,
		}
		if c.PosterPath != "" {
			cand.Poster = sourcePosterURL(sources.TMDb, &importer.Snapshot{Poster: c.PosterPath})
		}
		out = append(out, cand)
	}
	writeJSON(w, struct {
		Candidates []tmdbCandidate `json:"candidates"`
	}{out})
}

// handleApplyTMDbMatch records the chosen TMDb title as the item's TMDb snapshot. When TMDb
// links it to an IMDb id the item's OMDb record does not share, OMDb is re-matched to that id
// in the same action, so the two sources describe the same work.
func (s *Server) handleApplyTMDbMatch(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	m, err := db.GetMedia(ctx, pool, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	client := s.tmdbClient()
	if client == nil {
		http.Error(w, "TMDb API key is not configured", http.StatusBadRequest)
		return
	}
	req, err := decodeJSON[struct {
		Kind string `json:"kind"`
		ID   int    `json:"id"`
	}](w, r)
	kind := map[string]string{"movie": tmdb.KindMovie, "series": tmdb.KindTV}[req.Kind]
	if err != nil || kind == "" || req.ID <= 0 {
		http.Error(w, "a TMDb kind and id are required", http.StatusBadRequest)
		return
	}
	d, err := client.Details(ctx, kind, req.ID)
	if err != nil {
		http.Error(w, "could not fetch that title from TMDb", http.StatusBadGateway)
		return
	}
	snap := sources.FromTMDb(d, time.Now().Unix())
	written, err := s.writeTMDbSnapshot(m.Path, snap)
	if err != nil {
		http.Error(w, "could not write the match", http.StatusInternalServerError)
		return
	}
	if o := written.Sources[sources.OMDb]; snap.ImdbID != "" && (o == nil || o.ImdbID != snap.ImdbID) {
		if oc := s.omdbClient(); oc != nil {
			if mv, err := oc.LookupByID(ctx, snap.ImdbID); err == nil {
				s.bestEffort(s.applyOmdbResult(ctx, pool, m, oc, mv, written.Title, written.Year, true), "re-match OMDb to the TMDb title")
				written, _ = importer.ReadMeta(m.Path)
			}
		}
	}
	posterRel := folderPoster(m.Path)
	if posterRel == "" {
		posterRel = s.fetchSourcePoster(ctx, m.Path, sources.TMDb, snap)
	}
	if err := s.writeMediaCacheRow(ctx, pool, m.ID, written.Title, written.Year, written, posterRel); err != nil {
		http.Error(w, "could not write the match", http.StatusInternalServerError)
		return
	}
	s.bestEffort(db.PruneTMDb(ctx, pool, id), "clear tmdb task after manual match")
	s.bestEffort(db.PruneEnrich(ctx, pool, id), "clear enrich task after manual match")
	s.elog().Info(userFrom(r)+" matched "+written.Title+" on TMDb", logging.Fields{"id": id, "tmdb": snap.ID, "imdbID": snap.ImdbID})
	mc, _ := s.buildMatchContext(ctx, pool, id)
	writeJSON(w, mc)
}

// handleTMDbPoster proxies a TMDb poster at a small size so the admin pages load it
// same-origin.
func (s *Server) handleTMDbPoster(w http.ResponseWriter, r *http.Request) {
	client := s.tmdbClient()
	if client == nil {
		http.NotFound(w, r)
		return
	}
	size := r.URL.Query().Get("size")
	if size != "w92" && size != "w154" && size != "w185" {
		size = "w154"
	}
	img, err := client.Poster(r.Context(), size, r.URL.Query().Get("path"))
	if err != nil || len(img) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(img))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(img)
}

// sourceKind maps TMDb's "tv" onto the "series" every snapshot uses.
func sourceKind(tmdbKind string) string {
	if tmdbKind == tmdb.KindTV {
		return sources.KindSeries
	}
	return sources.KindMovie
}
