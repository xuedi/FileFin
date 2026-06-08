package db

import (
	"context"
	"database/sql"
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
	_, err := pool.ExecContext(ctx,
		`INSERT OR IGNORE INTO thumbnail_tasks (media_id, other_media, status, agent, error)
         VALUES (?, ?, ?, '', '')`,
		mediaID, otherMedia, ThumbStatusPending)
	return err
}

// ClaimNextThumbnail atomically claims the oldest pending task for agent, flipping it to
// generating, and returns it. ok is false when none is pending.
func ClaimNextThumbnail(ctx context.Context, pool *sql.DB, agent string) (ThumbnailTask, bool, error) {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return ThumbnailTask{}, false, err
	}
	defer tx.Rollback()

	var t ThumbnailTask
	err = tx.QueryRowContext(ctx,
		`SELECT id, media_id, other_media FROM thumbnail_tasks WHERE status = ? ORDER BY id LIMIT 1`,
		ThumbStatusPending).Scan(&t.ID, &t.MediaID, &t.OtherMedia)
	if err == sql.ErrNoRows {
		return ThumbnailTask{}, false, nil
	}
	if err != nil {
		return ThumbnailTask{}, false, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE thumbnail_tasks SET status = ?, agent = ?, error = '' WHERE id = ?`,
		ThumbStatusGenerating, agent, t.ID); err != nil {
		return ThumbnailTask{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ThumbnailTask{}, false, err
	}
	t.Status, t.Agent = ThumbStatusGenerating, agent
	return t, true, nil
}

// FinishThumbnail removes a task that generated successfully (the WebP files on disk are
// now the record).
func FinishThumbnail(ctx context.Context, pool *sql.DB, id int64) error {
	_, err := pool.ExecContext(ctx, `DELETE FROM thumbnail_tasks WHERE id = ?`, id)
	return err
}

// FailThumbnail marks a task failed with a message, leaving it for inspection (not
// retried automatically).
func FailThumbnail(ctx context.Context, pool *sql.DB, id int64, msg string) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE thumbnail_tasks SET status = ?, agent = '', error = ? WHERE id = ?`,
		ThumbStatusError, msg, id)
	return err
}

// ListActiveThumbnail returns the in-flight thumbnail jobs joined to their media title.
func ListActiveThumbnail(ctx context.Context, pool *sql.DB) ([]ActiveThumbnail, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT t.id, COALESCE(m.title, ''), t.agent, t.status
         FROM thumbnail_tasks t
         LEFT JOIN media m ON m.id = t.media_id
         WHERE t.status = ? ORDER BY t.id`, ThumbStatusGenerating)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActiveThumbnail{}
	for rows.Next() {
		var a ActiveThumbnail
		if err := rows.Scan(&a.ID, &a.Title, &a.Agent, &a.Status); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountPendingThumbnail returns how many thumbnail tasks are still waiting.
func CountPendingThumbnail(ctx context.Context, pool *sql.DB) (int, error) {
	var n int
	err := pool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM thumbnail_tasks WHERE status = ?`, ThumbStatusPending).Scan(&n)
	return n, err
}

// PruneThumbnail removes any pending or error task for a media folder, used by the scan
// once the folder's thumbnails are complete. A generating row is left to its agent.
func PruneThumbnail(ctx context.Context, pool *sql.DB, mediaID string) error {
	_, err := pool.ExecContext(ctx,
		`DELETE FROM thumbnail_tasks WHERE media_id = ? AND status IN (?, ?)`,
		mediaID, ThumbStatusPending, ThumbStatusError)
	return err
}

// ResetGeneratingToPending re-queues every generating row, used at startup so a task
// whose agent died mid-encode is retried.
func ResetGeneratingToPending(ctx context.Context, pool *sql.DB) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE thumbnail_tasks SET status = ?, agent = '' WHERE status = ?`,
		ThumbStatusPending, ThumbStatusGenerating)
	return err
}

// ClearThumbnailTasksAll empties the queue, for a full cache rebuild (the queue is
// transient and refilled by the scan from the media cache).
func ClearThumbnailTasksAll(ctx context.Context, pool *sql.DB) error {
	_, err := pool.ExecContext(ctx, `DELETE FROM thumbnail_tasks`)
	return err
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
	rows, err := pool.QueryContext(ctx,
		`SELECT id, path, poster, category_id FROM media ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaPoster{}
	for rows.Next() {
		var m MediaPoster
		if err := rows.Scan(&m.ID, &m.Path, &m.Poster, &m.CategoryID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CategoryFlags returns a map of category id to its other-media flag, for resolving each
// media item's owning category once during a scan.
func CategoryFlags(ctx context.Context, pool *sql.DB) (map[int64]bool, error) {
	rows, err := pool.QueryContext(ctx, `SELECT id, other_media FROM categories`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		var other bool
		if err := rows.Scan(&id, &other); err != nil {
			return nil, err
		}
		out[id] = other
	}
	return out, rows.Err()
}

// SetMediaPoster updates only the poster basename of a media cache row, used when the
// thumbnail agent writes a frame-derived base poster for an other-media folder.
func SetMediaPoster(ctx context.Context, pool *sql.DB, id, poster string) error {
	_, err := pool.ExecContext(ctx, `UPDATE media SET poster = ? WHERE id = ?`, poster, id)
	return err
}
