package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Tag is one entry of the library's curated tag vocabulary: the tag itself and how many
// media items carry it.
type Tag struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// ListTags returns every curated tag in the library with its item count, ordered by count
// descending then tag, so the sidebar leads with the tags that actually classify something.
// It reads the media_facets mirror, which the idx_media_facets_kv index already covers.
func ListTags(ctx context.Context, pool *sql.DB) ([]Tag, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT value, COUNT(DISTINCT media_id) FROM media_facets
         WHERE kind = 'tag' AND value <> ''
         GROUP BY value ORDER BY 2 DESC, 1`)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer rows.Close()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.Tag, &t.Count); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MediaIDsWithTag returns the ids of every media item carrying a tag, so a rename or delete
// knows which folders' meta.json to rewrite.
func MediaIDsWithTag(ctx context.Context, pool *sql.DB, tag string) ([]string, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT DISTINCT media_id FROM media_facets WHERE kind = 'tag' AND value = ? ORDER BY media_id`, tag)
	if err != nil {
		return nil, fmt.Errorf("media ids with tag %q: %w", tag, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan tagged media id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
