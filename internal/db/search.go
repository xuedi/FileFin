package db

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// Status filters a listing by the requesting user's playback state. The three states are
// mutually exclusive by construction, so a listing carries one of them, never a combination.
const (
	StatusAny       = "any"       // no state filter at all
	StatusUnwatched = "unwatched" // never started and never finished
	StatusProgress  = "progress"  // a resume pointer exists, not finished ("continue watching")
	StatusWatched   = "watched"   // marked watched
)

// Sort keys a listing can be ordered by. SortUpdated is the per-user state time, which is what
// orders the continue / favorites / completed rows; an item the user has never touched has no
// state row and sorts as 0.
const (
	SortYear    = "year"
	SortTitle   = "title"
	SortAdded   = "added"
	SortUpdated = "updated"
)

// ListOpts is one library listing: an optional text query scoped to a field, an optional
// playback-state filter, an ordering, and an optional cap. Home rows and search are the same
// listing with different options, so the search URL a home row's "more" tile points at
// returns that row's list verbatim.
type ListOpts struct {
	User          string // whose playback state the filters and the watched flag read
	Field         string // text scope: "all", "title", "cast", "year", ... (default "all")
	Query         string // the text; empty means "no text condition", not "no results"
	Status        string // one of the Status constants (default StatusAny)
	FavoritesOnly bool
	Sort          string // one of the Sort constants (default SortYear)
	Desc          bool
	Limit         int // 0 is no cap
}

// ListMedia returns the media matching opts, plus the total the filters match before the cap
// (so a capped row can say how many more there are). It denormalizes the search over the
// media row's columns plus the media_facets child table and LEFT JOINs the user's state
// mirror, so a listing is one indexed statement rather than a per-folder meta.json scan and
// the watched flag comes back on the row. A numeric scope with a non-numeric query returns
// no rows (never the whole library).
func ListMedia(ctx context.Context, pool *sql.DB, opts ListOpts) ([]MediaSummary, int, error) {
	where, args, ok := listWhere(opts)
	if !ok {
		return []MediaSummary{}, 0, nil
	}

	// The join is part of every statement: the filters, the ordering and the watched flag
	// all read the same per-user mirror row.
	from := `FROM media LEFT JOIN user_state us ON us.media_id = media.id AND us.user = ?`
	sel := `SELECT media.id, media.title, media.year, (media.poster <> ''), media.path, COALESCE(us.watched, 0) ` +
		from + ` WHERE ` + where + ` ORDER BY ` + listOrder(opts)
	selArgs := append([]any{opts.User}, args...)
	if opts.Limit > 0 {
		sel += ` LIMIT ?`
		selArgs = append(selArgs, opts.Limit)
	}
	rows, err := pool.QueryContext(ctx, sel, selArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list media: %w", err)
	}
	items, err := scanListRows(rows)
	if err != nil {
		return nil, 0, err
	}

	// Without a cap the rows are the total; with one it takes a second count.
	total := len(items)
	if opts.Limit > 0 && len(items) == opts.Limit {
		if err := pool.QueryRowContext(ctx,
			`SELECT COUNT(*) `+from+` WHERE `+where, selArgs[:1+len(args)]...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count media: %w", err)
		}
	}
	return items, total, nil
}

// scanListRows scans the listing column order, which carries the joined watched flag.
func scanListRows(rows *sql.Rows) ([]MediaSummary, error) {
	defer rows.Close()
	out := []MediaSummary{}
	for rows.Next() {
		var ms MediaSummary
		var hasPoster, watched int
		if err := rows.Scan(&ms.ID, &ms.Title, &ms.Year, &hasPoster, &ms.FolderPath, &watched); err != nil {
			return nil, fmt.Errorf("scan media listing row: %w", err)
		}
		ms.HasPoster = hasPoster != 0
		ms.Watched = watched != 0
		out = append(out, ms)
	}
	return out, rows.Err()
}

// listWhere joins the text condition and the state condition. ok is false when the text scope
// cannot match anything (a numeric scope given a non-numeric query), which is a listing of no
// rows rather than an unfiltered one. An empty query contributes no text condition, so a
// state filter alone lists the whole library in that state.
func listWhere(opts ListOpts) (string, []any, bool) {
	conds := []string{}
	args := []any{}
	if strings.TrimSpace(opts.Query) != "" {
		cond, textArgs, ok := searchWhere(opts.Field, opts.Query)
		if !ok {
			return "", nil, false
		}
		conds = append(conds, "("+cond+")")
		args = append(args, textArgs...)
	}
	switch opts.Status {
	case StatusUnwatched:
		conds = append(conds, `COALESCE(us.watched, 0) = 0 AND COALESCE(us.has_progress, 0) = 0`)
	case StatusProgress:
		conds = append(conds, `us.has_progress = 1 AND us.watched = 0`)
	case StatusWatched:
		conds = append(conds, `us.watched = 1`)
	}
	if opts.FavoritesOnly {
		conds = append(conds, `us.favorite = 1`)
	}
	if len(conds) == 0 {
		return "1", args, true
	}
	return strings.Join(conds, " AND "), args, true
}

// listOrder is the ORDER BY for a sort key and direction, each with a stable tiebreak so two
// items that compare equal keep a fixed order between calls.
func listOrder(opts ListOpts) string {
	dir := " ASC"
	if opts.Desc {
		dir = " DESC"
	}
	switch opts.Sort {
	case SortTitle:
		return "media.title" + dir + ", media.year"
	case SortAdded:
		return "media.added" + dir + ", media.title"
	case SortUpdated:
		return "COALESCE(us.updated, 0)" + dir + ", media.title"
	default: // SortYear and any unknown key: the library's browse order
		return "media.year" + dir + ", media.title"
	}
}

// searchWhere builds the WHERE condition and its bind args for a (field, q) pair. ok is
// false when a numeric scope got a non-numeric q (the caller returns no rows).
func searchWhere(field, q string) (string, []any, bool) {
	switch field {
	case "year":
		n, err := strconv.Atoi(strings.TrimSpace(q))
		if err != nil {
			return "", nil, false
		}
		return "media.year = ?", []any{n}, true
	case "decade":
		n, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(q)), "s"))
		if err != nil {
			return "", nil, false
		}
		d := (n / 10) * 10
		return "media.year BETWEEN ? AND ?", []any{d, d + 9}, true
	}

	p := likePattern(q)
	like := func(col string) string { return "LOWER(media." + col + ") LIKE ? ESCAPE '\\'" }
	facetExists := func(kind string) string {
		return "EXISTS (SELECT 1 FROM media_facets f WHERE f.media_id = media.id AND f.kind = '" +
			kind + "' AND LOWER(f.value) LIKE ? ESCAPE '\\')"
	}
	switch field {
	case "title":
		return like("title"), []any{p}, true
	case "description":
		return like("description"), []any{p}, true
	case "cast":
		return facetExists("actor"), []any{p}, true
	case "genre":
		return facetExists("genre"), []any{p}, true
	case "tag":
		return facetExists("tag"), []any{p}, true
	case "language":
		return like("language"), []any{p}, true
	case "director":
		return like("director"), []any{p}, true
	case "writer":
		return like("writer"), []any{p}, true
	default: // "all" and any unknown scope
		cols := []string{"title", "description", "plot", "language", "country", "director", "writer"}
		ors := make([]string, 0, len(cols)+3)
		args := make([]any, 0, len(cols)+3)
		for _, c := range cols {
			ors = append(ors, like(c))
			args = append(args, p)
		}
		ors = append(ors, facetExists("actor"), facetExists("genre"), facetExists("tag"))
		args = append(args, p, p, p)
		return strings.Join(ors, " OR "), args, true
	}
}

// likePattern wraps q as a case-insensitive substring LIKE pattern, escaping the LIKE
// wildcards (\, %, _) so a query containing them matches literally (the prior substring
// scan treated them as plain text). Pairs with `ESCAPE '\'` in the statement.
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + strings.ToLower(r.Replace(q)) + "%"
}
