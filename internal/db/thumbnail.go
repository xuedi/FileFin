package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Thumbnail task statuses. A row is pending until the agent claims it (generating); on
// success it is deleted, on failure it becomes error (left for the admin to see). The
// queue is transient cache state, refilled by the thumbnail scan from the media cache -
// the WebP files on disk are the durable record.
const (
	ThumbStatusPending    = "pending"
	ThumbStatusGenerating = "generating"
	ThumbStatusError      = "error"
)

// ThumbnailTask is one row of the thumbnail_tasks queue. OtherMedia carries the owning
// category's effective other-media flag so the agent knows whether to extract a frame
// poster when the folder has none.
type ThumbnailTask struct {
	ID         int64
	MediaID    string
	OtherMedia bool
	Status     string
	Agent      string
	Error      string
}

// ActiveThumbnail is an in-flight thumbnail job for the Progress page, joined to the
// media title for display.
type ActiveThumbnail struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Agent  string `json:"agent"`
	Status string `json:"status"`
}

// UpsertPendingThumbnail records a media folder as a pending thumbnail task with its
// other-media flag. INSERT OR IGNORE leaves any existing row for the same media untouched
// (a generating row keeps its agent; an error row is cleared by PruneThumbnail once the
// folder is complete).
func UpsertPendingThumbnail(ctx context.Context, pool *sql.DB, mediaID string, otherMedia bool) error {
	if _, err := pool.ExecContext(ctx,
		`INSERT OR IGNORE INTO thumbnail_tasks (media_id, other_media, status, agent, error)
         VALUES (?, ?, ?, '', '')`,
		mediaID, otherMedia, ThumbStatusPending); err != nil {
		return fmt.Errorf("upsert thumbnail %s: %w", mediaID, err)
	}
	return nil
}

// ClaimNextThumbnail atomically claims the oldest pending task for agent, flipping it to
// generating, and returns it. ok is false when none is pending.
func ClaimNextThumbnail(ctx context.Context, pool *sql.DB, agent string) (ThumbnailTask, bool, error) {
	var t ThumbnailTask
	id, ok, err := thumbnailQueue.claim(ctx, pool, agent, "media_id, other_media", "",
		func(s scanner) (int64, error) {
			var id int64
			return id, s.Scan(&id, &t.MediaID, &t.OtherMedia)
		})
	if err != nil || !ok {
		return ThumbnailTask{}, ok, err
	}
	t.ID, t.Status, t.Agent = id, ThumbStatusGenerating, agent
	return t, true, nil
}

// FinishThumbnail removes a task that generated successfully (the WebP files on disk are
// now the record).
func FinishThumbnail(ctx context.Context, pool *sql.DB, id int64) error {
	return thumbnailQueue.finish(ctx, pool, id)
}

// FailThumbnail marks a task failed with a message, leaving it for inspection (not
// retried automatically).
func FailThumbnail(ctx context.Context, pool *sql.DB, id int64, msg string) error {
	return thumbnailQueue.fail(ctx, pool, id, msg)
}

// ListActiveThumbnail returns the in-flight thumbnail jobs joined to their media title.
func ListActiveThumbnail(ctx context.Context, pool *sql.DB) ([]ActiveThumbnail, error) {
	return queryRows(ctx, pool,
		`SELECT t.id, COALESCE(m.title, ''), t.agent, t.status
         FROM thumbnail_tasks t
         LEFT JOIN media m ON m.id = t.media_id
         WHERE t.status = ? ORDER BY t.id`,
		func(r *sql.Rows) (ActiveThumbnail, error) {
			var a ActiveThumbnail
			return a, r.Scan(&a.ID, &a.Title, &a.Agent, &a.Status)
		}, ThumbStatusGenerating)
}

// CountPendingThumbnail returns how many thumbnail tasks are still waiting.
func CountPendingThumbnail(ctx context.Context, pool *sql.DB) (int, error) {
	return thumbnailQueue.countPending(ctx, pool)
}

// PruneThumbnail removes any pending or error task for a media folder, used by the scan
// once the folder's thumbnails are complete. A generating row is left to its agent.
func PruneThumbnail(ctx context.Context, pool *sql.DB, mediaID string) error {
	return thumbnailQueue.pruneByMedia(ctx, pool, mediaID, "")
}

// ResetGeneratingToPending re-queues every generating row, used at startup so a task
// whose agent died mid-encode is retried.
func ResetGeneratingToPending(ctx context.Context, pool *sql.DB) error {
	return thumbnailQueue.resetActiveToPending(ctx, pool, "")
}

// ClearThumbnailTasksAll empties the queue, for a full cache rebuild (the queue is
// transient and refilled by the scan from the media cache).
func ClearThumbnailTasksAll(ctx context.Context, pool *sql.DB) error {
	return thumbnailQueue.clearAll(ctx, pool)
}

// MediaPoster is a minimal media row for the thumbnail scan: the folder path, base
// poster basename (empty when none), and owning category id (to resolve the other-media
// flag).
type MediaPoster struct {
	ID         string
	Path       string
	Poster     string
	CategoryID int64
}

// AllMediaPosters returns every media item with its folder path, poster basename, and
// category id, for the thumbnail scan to decide candidacy.
func AllMediaPosters(ctx context.Context, pool *sql.DB) ([]MediaPoster, error) {
	return queryRows(ctx, pool,
		`SELECT id, path, poster, category_id FROM media ORDER BY id`,
		func(r *sql.Rows) (MediaPoster, error) {
			var m MediaPoster
			return m, r.Scan(&m.ID, &m.Path, &m.Poster, &m.CategoryID)
		})
}

// CategoryFlags returns a map of category id to its other-media flag, for resolving each
// media item's owning category once during a scan.
func CategoryFlags(ctx context.Context, pool *sql.DB) (map[int64]bool, error) {
	rows, err := pool.QueryContext(ctx, `SELECT id, other_media FROM categories`)
	if err != nil {
		return nil, fmt.Errorf("query category flags: %w", err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		var other bool
		if err := rows.Scan(&id, &other); err != nil {
			return nil, fmt.Errorf("scan category flag: %w", err)
		}
		out[id] = other
	}
	return out, rows.Err()
}

// SetMediaPoster updates only the poster basename of a media cache row, used when the
// thumbnail agent writes a frame-derived base poster for an other-media folder.
func SetMediaPoster(ctx context.Context, pool *sql.DB, id, poster string) error {
	if _, err := pool.ExecContext(ctx, `UPDATE media SET poster = ? WHERE id = ?`, poster, id); err != nil {
		return fmt.Errorf("set media poster %s: %w", id, err)
	}
	return nil
}
