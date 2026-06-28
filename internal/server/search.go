package server

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"filefin/internal/db"
	"filefin/internal/importer"
)

// handleSearch runs a library-wide query against the rich meta.json facets and returns
// the matching items as MediaSummary rows (the same shape the home/category lists use).
// Params: q (the text, trimmed) and field (the scope, default "all"). An empty q yields
// no results rather than the whole library.
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

// searchMedia scans every media row, reads each folder's meta.json live (the home page
// already does the same), and keeps the rows matching (field, q). It overlays the
// per-user watched flag like the category listing does and preserves AllMedia's
// year-then-title order.
func (s *Server) searchMedia(ctx context.Context, pool *sql.DB, user, field, q string) ([]db.MediaSummary, error) {
	out := []db.MediaSummary{}
	if q == "" {
		return out, nil
	}
	media, err := db.AllMedia(ctx, pool)
	if err != nil {
		return nil, err
	}
	for _, m := range media {
		// A missing meta.json yields an empty Meta: text facets simply will not match,
		// while year/decade still match on the cache row's year.
		meta, _ := importer.ReadMeta(m.FolderPath)
		if !mediaMatches(meta, m.Year, field, q) {
			continue
		}
		m.Watched = meta.State[user].Watched
		out = append(out, m)
	}
	return out, nil
}

// mediaMatches reports whether one item satisfies the query. year is the cache row's
// year (used for the numeric scopes); meta supplies every text facet. An unknown field
// falls back to "all".
func mediaMatches(meta importer.Meta, year int, field, q string) bool {
	switch field {
	case "year":
		n, err := strconv.Atoi(strings.TrimSpace(q))
		return err == nil && n == year
	case "decade":
		n, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(q)), "s"))
		if err != nil {
			return false
		}
		d := (n / 10) * 10
		return year >= d && year <= d+9
	}

	ql := strings.ToLower(q)
	switch field {
	case "title":
		return containsFold(meta.Title, ql)
	case "description":
		return containsFold(meta.Description, ql)
	case "cast":
		return anyContainsFold(meta.Actors, ql)
	case "genre":
		return anyContainsFold(meta.Tags, ql)
	case "language":
		return containsFold(meta.Metadata["language"], ql)
	case "director":
		return containsFold(meta.Metadata["directedBy"], ql)
	case "writer":
		return containsFold(meta.Metadata["writtenBy"], ql)
	default: // "all" and any unknown scope
		if containsFold(meta.Title, ql) || containsFold(meta.Description, ql) || containsFold(meta.Plot, ql) {
			return true
		}
		if anyContainsFold(meta.Actors, ql) || anyContainsFold(meta.Tags, ql) {
			return true
		}
		for _, k := range []string{"language", "origin", "directedBy", "writtenBy"} {
			if containsFold(meta.Metadata[k], ql) {
				return true
			}
		}
		return false
	}
}

// containsFold reports whether s contains the already-lowercased needle, case-insensitively.
func containsFold(s, lowerNeedle string) bool {
	return strings.Contains(strings.ToLower(s), lowerNeedle)
}

// anyContainsFold reports whether any element of list contains the lowercased needle.
func anyContainsFold(list []string, lowerNeedle string) bool {
	for _, s := range list {
		if containsFold(s, lowerNeedle) {
			return true
		}
	}
	return false
}
