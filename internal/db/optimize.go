package db

import (
	"context"
	"database/sql"
)

// Optimize task statuses. A row is pending until an agent claims it (encoding); on
// success it is deleted, on failure it becomes error. The queue is transient cache
// state, rebuilt by the planner from media_files - the on-disk .optimized.mp4 is the
// durable record.
const (
	OptimizeStatusPending  = "pending"
	OptimizeStatusEncoding = "encoding"
	OptimizeStatusError    = "error"
)

// OptimizeTask is one row of the optimize_tasks queue.
type OptimizeTask struct {
	ID            int64
	MediaID       string
	FileIdx       int
	SourcePath    string
	OptimizedPath string
	Status        string
	Agent         string
	Percent       int
	Error         string
}

// ActiveTask is an in-flight encode for the Progress page, joined to the media title
// and file name for display.
type ActiveTask struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	File    string `json:"file"`
	Agent   string `json:"agent"`
	Percent int    `json:"percent"`
}

// UpsertPendingTask records a candidate as a pending task. INSERT OR IGNORE leaves any
// existing row for the same media/file untouched (encoding rows keep their agent; error
// rows are cleared by PruneTask once the file no longer needs work).
func UpsertPendingTask(ctx context.Context, pool *sql.DB, mediaID string, fileIdx int, source, optimized string) error {
	_, err := pool.ExecContext(ctx,
		`INSERT OR IGNORE INTO optimize_tasks
            (media_id, file_idx, source_path, optimized_path, status, agent, percent, error)
         VALUES (?, ?, ?, ?, ?, '', 0, '')`,
		mediaID, fileIdx, source, optimized, OptimizeStatusPending)
	return err
}

// ClaimNextTask atomically claims the oldest pending task for agent, flipping it to
// encoding, and returns it. ok is false when no task is pending. The single cache
// connection makes the read-then-claim transaction race-free: two callers never get the
// same row.
func ClaimNextTask(ctx context.Context, pool *sql.DB, agent string) (OptimizeTask, bool, error) {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return OptimizeTask{}, false, err
	}
	defer tx.Rollback()

	var t OptimizeTask
	err = tx.QueryRowContext(ctx,
		`SELECT id, media_id, file_idx, source_path, optimized_path
         FROM optimize_tasks WHERE status = ? ORDER BY id LIMIT 1`, OptimizeStatusPending).
		Scan(&t.ID, &t.MediaID, &t.FileIdx, &t.SourcePath, &t.OptimizedPath)
	if err == sql.ErrNoRows {
		return OptimizeTask{}, false, nil
	}
	if err != nil {
		return OptimizeTask{}, false, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE optimize_tasks SET status = ?, agent = ?, percent = 0, error = '' WHERE id = ?`,
		OptimizeStatusEncoding, agent, t.ID); err != nil {
		return OptimizeTask{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return OptimizeTask{}, false, err
	}
	t.Status, t.Agent = OptimizeStatusEncoding, agent
	return t, true, nil
}

// UpdateTaskPercent mirrors an encode's progress percent into the row.
func UpdateTaskPercent(ctx context.Context, pool *sql.DB, id int64, percent int) error {
	_, err := pool.ExecContext(ctx, `UPDATE optimize_tasks SET percent = ? WHERE id = ?`, percent, id)
	return err
}

// FinishTask removes a task that completed successfully (the .optimized.mp4 on disk is
// now the record).
func FinishTask(ctx context.Context, pool *sql.DB, id int64) error {
	_, err := pool.ExecContext(ctx, `DELETE FROM optimize_tasks WHERE id = ?`, id)
	return err
}

// FailTask marks a task failed with an error message, leaving it for inspection.
func FailTask(ctx context.Context, pool *sql.DB, id int64, msg string) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE optimize_tasks SET status = ?, agent = '', error = ? WHERE id = ?`,
		OptimizeStatusError, msg, id)
	return err
}

// ListActiveTasks returns the in-flight encodes joined to their media title and file
// name, ordered by id.
func ListActiveTasks(ctx context.Context, pool *sql.DB) ([]ActiveTask, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT t.id, COALESCE(m.title, ''), COALESCE(f.name, ''), t.agent, t.percent
         FROM optimize_tasks t
         LEFT JOIN media m ON m.id = t.media_id
         LEFT JOIN media_files f ON f.media_id = t.media_id AND f.idx = t.file_idx
         WHERE t.status = ? ORDER BY t.id`, OptimizeStatusEncoding)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActiveTask{}
	for rows.Next() {
		var a ActiveTask
		if err := rows.Scan(&a.ID, &a.Title, &a.File, &a.Agent, &a.Percent); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountPending returns how many tasks are still waiting to be claimed.
func CountPending(ctx context.Context, pool *sql.DB) (int, error) {
	var n int
	err := pool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM optimize_tasks WHERE status = ?`, OptimizeStatusPending).Scan(&n)
	return n, err
}

// PruneTask removes any pending or error task for a media/file, used by the planner once
// the file has a fresh sibling or no longer needs transcoding. An encoding row is left to
// its agent.
func PruneTask(ctx context.Context, pool *sql.DB, mediaID string, fileIdx int) error {
	_, err := pool.ExecContext(ctx,
		`DELETE FROM optimize_tasks WHERE media_id = ? AND file_idx = ? AND status IN (?, ?)`,
		mediaID, fileIdx, OptimizeStatusPending, OptimizeStatusError)
	return err
}

// ResetEncodingToPending re-queues every encoding row, used at startup so a task whose
// agent died mid-encode (no one owns it after a restart) is retried.
func ResetEncodingToPending(ctx context.Context, pool *sql.DB) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE optimize_tasks SET status = ?, agent = '', percent = 0 WHERE status = ?`,
		OptimizeStatusPending, OptimizeStatusEncoding)
	return err
}

// ClearOptimizeTasksAll empties the queue, for a full cache rebuild (the queue is
// transient and refilled by the planner from the filesystem).
func ClearOptimizeTasksAll(ctx context.Context, pool *sql.DB) error {
	_, err := pool.ExecContext(ctx, `DELETE FROM optimize_tasks`)
	return err
}
