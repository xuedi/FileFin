package db

import (
	"context"
	"database/sql"
	"fmt"
)

// media_health records the discovery agent's per-item integrity check: when it last
// looked, a fingerprint of the folder (meta.json mtime + file list) so an unchanged item
// can be skipped, whether the item is healthy, and a JSON list of issues when not. It is
// derived cache state - cleared by a rebuild and re-derived - never written into the media
// tree, so a corrupt meta.json (which could not record its own health) is still flagged.

// UnhealthyMedia is one flagged item for the admin health panel: the media id, its title
// (joined from the media row), the raw issues JSON, and when it was last checked.
type UnhealthyMedia struct {
	MediaID       string `json:"id"`
	Title         string `json:"title"`
	Issues        string `json:"issues"`
	LastCheckedAt int64  `json:"lastCheckedAt"`
}

// UpsertHealth records (or replaces) the health row for a media item, stamping the check
// time so the rolling sweep orders by least-recently-checked. issues is the marshalled
// issue list ("" or "[]" when healthy).
func UpsertHealth(ctx context.Context, pool *sql.DB, mediaID, fingerprint string, ok bool, issues string, checkedAt int64) error {
	if _, err := pool.ExecContext(ctx,
		`INSERT OR REPLACE INTO media_health (media_id, last_checked_at, fingerprint, ok, issues)
         VALUES (?, ?, ?, ?, ?)`,
		mediaID, checkedAt, fingerprint, ok, issues); err != nil {
		return fmt.Errorf("upsert health %s: %w", mediaID, err)
	}
	return nil
}

// HealthFingerprint returns the stored fingerprint for an item, or "" when it has no
// health row yet (never checked), so the reconcile knows whether the folder changed.
func HealthFingerprint(ctx context.Context, pool *sql.DB, mediaID string) (string, error) {
	var fp string
	err := pool.QueryRowContext(ctx, `SELECT fingerprint FROM media_health WHERE media_id = ?`, mediaID).Scan(&fp)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get fingerprint %s: %w", mediaID, err)
	}
	return fp, nil
}

// OldestUncheckedMedia returns up to limit media ids ordered by least-recently-checked
// (never-checked items, which have no health row, sort first), so the rolling sweep
// processes the whole library as a continuous trickle.
func OldestUncheckedMedia(ctx context.Context, pool *sql.DB, limit int) ([]string, error) {
	return queryRows(ctx, pool,
		`SELECT m.id FROM media m
         LEFT JOIN media_health h ON h.media_id = m.id
         ORDER BY COALESCE(h.last_checked_at, 0) ASC, m.id LIMIT ?`,
		func(r *sql.Rows) (string, error) {
			var id string
			return id, r.Scan(&id)
		}, limit)
}

// ListUnhealthy returns the items flagged with issues at their last check, joined to the
// media title, for the admin health panel.
func ListUnhealthy(ctx context.Context, pool *sql.DB) ([]UnhealthyMedia, error) {
	return queryRows(ctx, pool,
		`SELECT h.media_id, COALESCE(m.title, ''), h.issues, h.last_checked_at
         FROM media_health h
         LEFT JOIN media m ON m.id = h.media_id
         WHERE h.ok = 0 AND h.last_checked_at > 0
         ORDER BY m.title`,
		func(r *sql.Rows) (UnhealthyMedia, error) {
			var u UnhealthyMedia
			return u, r.Scan(&u.MediaID, &u.Title, &u.Issues, &u.LastCheckedAt)
		})
}

// CountUnhealthy returns how many checked items currently carry issues.
func CountUnhealthy(ctx context.Context, pool *sql.DB) (int, error) {
	var n int
	if err := pool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_health WHERE ok = 0 AND last_checked_at > 0`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count unhealthy: %w", err)
	}
	return n, nil
}

// CountUncheckedMedia returns how many media items have never been checked (no health row
// or a zero check time), i.e. the rolling sweep's remaining backlog.
func CountUncheckedMedia(ctx context.Context, pool *sql.DB) (int, error) {
	var n int
	if err := pool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media m
         LEFT JOIN media_health h ON h.media_id = m.id
         WHERE COALESCE(h.last_checked_at, 0) = 0`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count unchecked media: %w", err)
	}
	return n, nil
}

// PruneHealth removes the health row for a media item, used when the item is deleted from
// the cache (its folder vanished from disk).
func PruneHealth(ctx context.Context, pool *sql.DB, mediaID string) error {
	if _, err := pool.ExecContext(ctx, `DELETE FROM media_health WHERE media_id = ?`, mediaID); err != nil {
		return fmt.Errorf("prune health %s: %w", mediaID, err)
	}
	return nil
}

// ClearHealthAll empties the health table, for a full cache rebuild (health is derived
// cache state, re-derived by the discovery sweep).
func ClearHealthAll(ctx context.Context, pool *sql.DB) error {
	if _, err := pool.ExecContext(ctx, `DELETE FROM media_health`); err != nil {
		return fmt.Errorf("clear media_health: %w", err)
	}
	return nil
}
