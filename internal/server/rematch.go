package server

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/omdb"
	"filefin/internal/recognize"
)

// The manual-match surface behind the admin "Unhealthy media" page: list the media OMDb
// could not match, drill into one, search OMDb for candidates, and apply the chosen record
// through the shared replace-mode enrichment write (see applyOmdbResult).

// matchFile is one file of a media item in the match context (no playback fields).
type matchFile struct {
	Name    string `json:"name"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Ext     string `json:"ext"`
}

// matchContext is the detail view's payload: the folder/file facts, the current match (for a
// re-match comparison), the enrich failure reason, and a folder-name guess to seed the form.
type matchContext struct {
	ID         string      `json:"id"`
	Folder     string      `json:"folder"`
	Category   string      `json:"category"`
	Title      string      `json:"title"`
	Year       int         `json:"year"`
	Enriched   bool        `json:"enriched"`
	HasPoster  bool        `json:"hasPoster"`
	ImdbID     string      `json:"imdbId"`
	Plot       string      `json:"plot"`
	Error      string      `json:"error"`
	Files      []matchFile `json:"files"`
	GuessTitle string      `json:"guessTitle"`
	GuessYear  int         `json:"guessYear"`
}

// omdbCandidate is one OMDb search hit offered for selection.
type omdbCandidate struct {
	ImdbID    string `json:"imdbId"`
	Title     string `json:"title"`
	Year      string `json:"year"`
	Type      string `json:"type"`
	HasPoster bool   `json:"hasPoster"`
}

// handleUnmatched lists every media item still without an OMDb metadata match.
func (s *Server) handleUnmatched(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	items, err := db.ListUnmatchedMedia(r.Context(), pool)
	if err != nil {
		http.Error(w, "could not list unmatched media", http.StatusInternalServerError)
		return
	}
	writeJSON(w, struct {
		Items []db.UnmatchedMedia `json:"items"`
	}{items})
}

// handleMatchContext returns one item's match context for the detail view.
func (s *Server) handleMatchContext(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	mc, ok := s.buildMatchContext(r.Context(), pool, r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, mc)
}

// buildMatchContext assembles the detail payload for a media id. ok is false for an unknown id.
func (s *Server) buildMatchContext(ctx context.Context, pool *sql.DB, id string) (matchContext, bool) {
	m, err := db.GetMedia(ctx, pool, id)
	if err != nil {
		return matchContext{}, false
	}
	cat, _ := db.CategoryName(ctx, pool, m.CategoryID)
	files, _ := db.MediaFiles(ctx, pool, id)
	meta, _ := importer.ReadMeta(m.Path)
	errMsg, _ := db.EnrichError(ctx, pool, id)
	guess := recognize.FromPath(filepath.Base(m.Path))
	plot := m.Description
	if plot == "" {
		plot = m.Plot
	}
	mc := matchContext{
		ID: m.ID, Folder: filepath.Base(m.Path), Category: cat,
		Title: m.Title, Year: m.Year, Enriched: m.Enriched, HasPoster: m.Poster != "",
		ImdbID: meta.Metadata["imdbID"], Plot: plot, Error: errMsg,
		Files: []matchFile{}, GuessTitle: guess.Title, GuessYear: guess.Year,
	}
	for _, f := range files {
		mc.Files = append(mc.Files, matchFile{Name: f.Name, Season: f.Season, Episode: f.Episode, Ext: f.Ext})
	}
	return mc, true
}

// handleOmdbSearch returns OMDb candidates for a media item: a single record when an imdb id
// is given, otherwise a title (+ optional year) search.
func (s *Server) handleOmdbSearch(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, err := db.GetMedia(r.Context(), pool, r.PathValue("id")); err != nil {
		http.NotFound(w, r)
		return
	}
	client := s.omdbClient()
	if client == nil {
		http.Error(w, "OMDb API key is not configured", http.StatusBadRequest)
		return
	}
	req, err := decodeJSON[struct {
		Title  string `json:"title"`
		Year   int    `json:"year"`
		ImdbID string `json:"imdbId"`
		Kind   string `json:"kind"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cands := []omdbCandidate{}
	if imdbID := strings.TrimSpace(req.ImdbID); imdbID != "" {
		if mv, err := client.LookupByID(r.Context(), imdbID); err == nil {
			cands = append(cands, candidateFromMovie(mv))
		}
	} else {
		title := strings.TrimSpace(req.Title)
		if title == "" {
			http.Error(w, "a title or IMDb id is required", http.StatusBadRequest)
			return
		}
		res, err := client.Search(r.Context(), title, req.Year, strings.TrimSpace(req.Kind))
		if err != nil {
			http.Error(w, "could not reach OMDb", http.StatusBadGateway)
			return
		}
		for _, hit := range res {
			cands = append(cands, candidateFromSearch(hit))
		}
	}
	writeJSON(w, struct {
		Candidates []omdbCandidate `json:"candidates"`
	}{cands})
}

// handleApplyMatch fetches the chosen record by imdb id and writes it onto the item in
// replace mode (correcting title/year, metadata, and poster), then clears any enrich task.
func (s *Server) handleApplyMatch(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	m, err := db.GetMedia(r.Context(), pool, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	client := s.omdbClient()
	if client == nil {
		http.Error(w, "OMDb API key is not configured", http.StatusBadRequest)
		return
	}
	req, err := decodeJSON[struct {
		ImdbID string `json:"imdbId"`
		Title  string `json:"title"`
		Year   int    `json:"year"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	imdbID := strings.TrimSpace(req.ImdbID)
	if imdbID == "" {
		http.Error(w, "an IMDb id is required", http.StatusBadRequest)
		return
	}
	mv, err := client.LookupByID(r.Context(), imdbID)
	if err != nil {
		http.Error(w, "could not fetch that title from OMDb", http.StatusBadGateway)
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(mv.Title)
	}
	year := req.Year
	if year <= 0 {
		year = yearFromOMDb(mv.Year)
	}
	if err := s.applyOmdbResult(r.Context(), pool, m, client, mv, title, year, true); err != nil {
		http.Error(w, "could not write the match", http.StatusInternalServerError)
		return
	}
	s.bestEffort(db.PruneEnrich(r.Context(), pool, id), "clear enrich task after manual match")
	s.elog().Info(userFrom(r)+" matched "+title,
		logging.Fields{"id": id, "imdbID": imdbID, "title": title, "year": year})
	mc, _ := s.buildMatchContext(r.Context(), pool, id)
	writeJSON(w, mc)
}

// handleOmdbPoster proxies a candidate's small OMDb poster so the search UI loads its
// thumbnails same-origin (no external CDN reference in the served page).
func (s *Server) handleOmdbPoster(w http.ResponseWriter, r *http.Request) {
	client := s.omdbClient()
	if client == nil {
		http.NotFound(w, r)
		return
	}
	img, ct, err := client.Poster(r.Context(), r.PathValue("imdbId"), 150)
	if err != nil || len(img) == 0 {
		http.NotFound(w, r)
		return
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(img)
}

func candidateFromMovie(mv *omdb.Movie) omdbCandidate {
	return omdbCandidate{
		ImdbID: mv.ImdbID, Title: mv.Title, Year: mv.Year, Type: mv.Type,
		HasPoster: mv.Poster != "" && mv.Poster != "N/A",
	}
}

func candidateFromSearch(res omdb.SearchResult) omdbCandidate {
	return omdbCandidate{
		ImdbID: res.ImdbID, Title: res.Title, Year: res.Year, Type: res.Type,
		HasPoster: res.Poster != "" && res.Poster != "N/A",
	}
}

// yearFromOMDb reads the leading four-digit year from an OMDb Year ("2011", "2011-2013").
func yearFromOMDb(s string) int {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(s[:4])
	return y
}
