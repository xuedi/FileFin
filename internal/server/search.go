package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"

	"filefin/internal/db"
)

// handleSearch runs a library-wide listing and returns the matching items as MediaSummary
// rows (the same shape the home/category lists use). Params: q (the text, trimmed) and field
// (the scope, default "all"), plus the filter/sort controls status, fav, sort and dir.
//
// A request that asks for nothing at all - no text, no filter, no explicit ordering - yields
// no results rather than the whole library, so a bare Enter in the search box cannot replace
// the home page by accident. Naming a sort is asking for something ("all media, newest
// first"), so it counts.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	opts := listOptsFrom(q, userFrom(r))
	if opts.Query == "" && opts.Status == db.StatusAny && !opts.FavoritesOnly && q.Get("sort") == "" {
		writeJSON(w, []db.MediaSummary{})
		return
	}
	results, _, err := db.ListMedia(r.Context(), pool, opts)
	if err != nil {
		http.Error(w, "could not search", http.StatusInternalServerError)
		return
	}
	writeJSON(w, results)
}

// listOptsFrom reads a search query string into listing options, normalising every control to
// a known value so an unknown one degrades to the default rather than erroring.
func listOptsFrom(q url.Values, user string) db.ListOpts {
	field := q.Get("field")
	if field == "" {
		field = "all"
	}
	return db.ListOpts{
		User:          user,
		Field:         field,
		Query:         strings.TrimSpace(q.Get("q")),
		Status:        listStatus(q.Get("status")),
		FavoritesOnly: q.Get("fav") == "1",
		Sort:          listSort(q.Get("sort")),
		Desc:          q.Get("dir") == "desc",
	}
}

func listStatus(v string) string {
	switch v {
	case db.StatusUnwatched, db.StatusProgress, db.StatusWatched:
		return v
	default:
		return db.StatusAny
	}
}

func listSort(v string) string {
	switch v {
	case db.SortTitle, db.SortAdded, db.SortUpdated:
		return v
	default:
		return db.SortYear
	}
}

// searchQueryString renders listing options back into the search query string that reproduces
// them. It is how a home row tells the UI where its "more" tile leads: the row and the search
// it opens are built from one set of options, so they cannot drift apart.
func searchQueryString(opts db.ListOpts) string {
	v := url.Values{}
	if opts.Query != "" {
		v.Set("q", opts.Query)
		if opts.Field != "" && opts.Field != "all" {
			v.Set("field", opts.Field)
		}
	}
	if opts.Status != db.StatusAny && opts.Status != "" {
		v.Set("status", opts.Status)
	}
	if opts.FavoritesOnly {
		v.Set("fav", "1")
	}
	v.Set("sort", listSort(opts.Sort)) // always explicit: it is what makes the listing intentional
	if opts.Desc {
		v.Set("dir", "desc")
	}
	return v.Encode()
}

// overlayWatched folds the user's watched flag onto a listing from the user_state mirror,
// replacing the former per-item meta.json reads with a single query. Used by the category
// listing; the search/home listings join the mirror themselves.
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
