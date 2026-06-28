package server

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"filefin/internal/db"
)

// handleSearch runs a library-wide query against the denormalized facet columns and returns
// the matching items as MediaSummary rows (the same shape the home/category lists use).
// Params: q (the text, trimmed) and field (the scope, default "all"). An empty q yields no
// results rather than the whole library.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	field := r.URL.Query().Get("field")
	if field == "" {
		field = "all"
	}
	results, err := s.searchMedia(r.Context(), pool, userFrom(r), field, q)
	if err != nil {
		http.Error(w, "could not search", http.StatusInternalServerError)
		return
	}
	writeJSON(w, results)
}

// searchMedia queries the cache for the matching rows and overlays the per-user watched flag
// from the user_state mirror (one set lookup, not a per-result read).
func (s *Server) searchMedia(ctx context.Context, pool *sql.DB, user, field, q string) ([]db.MediaSummary, error) {
	results, err := db.SearchMedia(ctx, pool, field, q)
	if err != nil {
		return nil, err
	}
	if err := s.overlayWatched(ctx, pool, user, results); err != nil {
		return nil, err
	}
	return results, nil
}

// overlayWatched folds the user's watched flag onto a listing from the user_state mirror,
// replacing the former per-item meta.json reads with a single query. Shared by search and the
// category listing.
func (s *Server) overlayWatched(ctx context.Context, pool *sql.DB, user string, items []db.MediaSummary) error {
	if len(items) == 0 {
		return nil
	}
	watched, err := db.WatchedSet(ctx, pool, user)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].Watched = watched[items[i].ID]
	}
	return nil
}
