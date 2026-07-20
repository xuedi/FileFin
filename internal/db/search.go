package db

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// SearchMedia returns the media items matching a query, scoped by field, ordered by year
// then title (the library's browse order). It denormalizes the search over the media row's
// columns plus the media_facets child table, so a query is one indexed statement rather than
// a per-folder meta.json scan. An empty q, or a numeric scope with a non-numeric q, returns
// no rows (never the whole library). An unknown field falls back to "all".
func SearchMedia(ctx context.Context, pool *sql.DB, field, q string) ([]MediaSummary, error) {
	if strings.TrimSpace(q) == "" {
		return []MediaSummary{}, nil
	}
	cond, args, ok := searchWhere(field, q)
	if !ok {
		return []MediaSummary{}, nil
	}
	rows, err := pool.QueryContext(ctx,
		`SELECT id, title, year, (poster <> ''), path FROM media WHERE `+cond+` ORDER BY year, title`, args...)
	if err != nil {
		return nil, fmt.Errorf("search media (%s): %w", field, err)
	}
	return scanSummaries(rows)
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
		return "year = ?", []any{n}, true
	case "decade":
		n, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(q)), "s"))
		if err != nil {
			return "", nil, false
		}
		d := (n / 10) * 10
		return "year BETWEEN ? AND ?", []any{d, d + 9}, true
	}

	p := likePattern(q)
	like := func(col string) string { return "LOWER(" + col + ") LIKE ? ESCAPE '\\'" }
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
