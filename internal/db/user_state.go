package db

import (
	"context"
	"database/sql"
	"fmt"
)

// UserStateRow is the cache mirror of one user's playback state for one media item. It is
// derived from the authoritative meta.json State map; the cache stays fully rebuildable, so
// nothing here is a source of truth. HasProgress is whether a resume pointer exists (the home
// "continue" predicate needs only its presence, not the pointer itself).
type UserStateRow struct {
	Watched     bool
	Favorite    bool
	Rating      int
	HasProgress bool
	Updated     int64
}

// UpsertUserState writes one (user, media) mirror row, called on every live state mutation
// right after the meta.json write. INSERT OR REPLACE keeps it idempotent on the composite key.
func UpsertUserState(ctx context.Context, pool *sql.DB, user, mediaID string, r UserStateRow) error {
	_, err := pool.ExecContext(ctx,
		`INSERT OR REPLACE INTO user_state (user, media_id, watched, favorite, rating, has_progress, updated)
         VALUES (?, ?, ?, ?, ?, ?, ?)`,
		user, mediaID, boolInt(r.Watched), boolInt(r.Favorite), r.Rating, boolInt(r.HasProgress), r.Updated)
	if err != nil {
		return fmt.Errorf("upsert user state %s/%s: %w", user, mediaID, err)
	}
	return nil
}

// ReplaceUserStateForMedia swaps every user's mirror rows for one media item, used by the
// scanner (rebuild / reconcile / backfill) to re-derive the mirror from meta.json. A folder
// whose meta.json carries no state ends up with no rows, exactly as on disk.
func ReplaceUserStateForMedia(ctx context.Context, pool *sql.DB, mediaID string, byUser map[string]UserStateRow) error {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace user state %s: %w", mediaID, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_state WHERE media_id = ?`, mediaID); err != nil {
		return fmt.Errorf("clear user state %s: %w", mediaID, err)
	}
	for user, r := range byUser {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_state (user, media_id, watched, favorite, rating, has_progress, updated)
             VALUES (?, ?, ?, ?, ?, ?, ?)`,
			user, mediaID, boolInt(r.Watched), boolInt(r.Favorite), r.Rating, boolInt(r.HasProgress), r.Updated); err != nil {
			return fmt.Errorf("insert user state %s/%s: %w", user, mediaID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace user state %s: %w", mediaID, err)
	}
	return nil
}

// ClearUserStateForMedia removes a media item's mirror rows, for a folder that vanished from
// disk (called alongside DeleteMedia).
func ClearUserStateForMedia(ctx context.Context, pool *sql.DB, mediaID string) error {
	if _, err := pool.ExecContext(ctx, `DELETE FROM user_state WHERE media_id = ?`, mediaID); err != nil {
		return fmt.Errorf("clear user state for media %s: %w", mediaID, err)
	}
	return nil
}

// ClearUserState empties the whole mirror, for a full rebuild from disk.
func ClearUserState(ctx context.Context, pool *sql.DB) error {
	if _, err := pool.ExecContext(ctx, `DELETE FROM user_state`); err != nil {
		return fmt.Errorf("clear user state: %w", err)
	}
	return nil
}

// WatchedSet returns the set of media ids a user has marked watched, for folding the watched
// flag onto a listing in one query instead of a per-item read.
func WatchedSet(ctx context.Context, pool *sql.DB, user string) (map[string]bool, error) {
	rows, err := pool.QueryContext(ctx, `SELECT media_id FROM user_state WHERE user = ? AND watched = 1`, user)
	if err != nil {
		return nil, fmt.Errorf("query watched set %s: %w", user, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan watched id: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// HomeBuckets returns a user's continue / favorites / completed rows from the mirror, each a
// join to media for the tile fields and ordered by the per-user updated time (newest first) -
// the cache-served replacement for the former per-folder meta.json scan.
func HomeBuckets(ctx context.Context, pool *sql.DB, user string) (cont, fav, done []MediaSummary, err error) {
	if cont, err = homeBucket(ctx, pool, user, `has_progress = 1 AND watched = 0`); err != nil {
		return nil, nil, nil, err
	}
	if fav, err = homeBucket(ctx, pool, user, `favorite = 1`); err != nil {
		return nil, nil, nil, err
	}
	if done, err = homeBucket(ctx, pool, user, `watched = 1`); err != nil {
		return nil, nil, nil, err
	}
	return cont, fav, done, nil
}

// homeBucket runs one bucket's query: the user's mirror rows matching pred, joined to media,
// newest-first.
func homeBucket(ctx context.Context, pool *sql.DB, user, pred string) ([]MediaSummary, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT m.id, m.title, m.year, (m.poster <> ''), us.watched, m.path
         FROM user_state us JOIN media m ON m.id = us.media_id
         WHERE us.user = ? AND us.`+pred+`
         ORDER BY us.updated DESC`, user)
	if err != nil {
		return nil, fmt.Errorf("query home bucket: %w", err)
	}
	defer rows.Close()
	out := []MediaSummary{}
	for rows.Next() {
		var ms MediaSummary
		var hasPoster, watched int
		if err := rows.Scan(&ms.ID, &ms.Title, &ms.Year, &hasPoster, &watched, &ms.FolderPath); err != nil {
			return nil, fmt.Errorf("scan home row: %w", err)
		}
		ms.HasPoster = hasPoster != 0
		ms.Watched = watched != 0
		out = append(out, ms)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
