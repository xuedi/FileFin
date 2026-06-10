package db

import (
	"context"
	"database/sql"
	"fmt"
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
	if _, err := pool.ExecContext(ctx,
		`INSERT OR IGNORE INTO optimize_tasks
            (media_id, file_idx, source_path, optimized_path, status, agent, percent, error)
         VALUES (?, ?, ?, ?, ?, '', 0, '')`,
		mediaID, fileIdx, source, optimized, OptimizeStatusPending); err != nil {
		return fmt.Errorf("upsert optimize task %s/%d: %w", mediaID, fileIdx, err)
	}
	return nil
}

// ClaimNextTask atomically claims the oldest pending task for agent, flipping it to
// encoding, and returns it. ok is false when no task is pending.
func ClaimNextTask(ctx context.Context, pool *sql.DB, agent string) (OptimizeTask, bool, error) {
	var t OptimizeTask
	id, ok, err := optimizeQueue.claim(ctx, pool, agent,
		"media_id, file_idx, source_path, optimized_path", "percent = 0",
		func(s scanner) (int64, error) {
			var id int64
			return id, s.Scan(&id, &t.MediaID, &t.FileIdx, &t.SourcePath, &t.OptimizedPath)
		})
	if err != nil || !ok {
		return OptimizeTask{}, ok, err
	}
	t.ID, t.Status, t.Agent = id, OptimizeStatusEncoding, agent
	return t, true, nil
}

// UpdateTaskPercent mirrors an encode's progress percent into the row.
func UpdateTaskPercent(ctx context.Context, pool *sql.DB, id int64, percent int) error {
	if _, err := pool.ExecContext(ctx, `UPDATE optimize_tasks SET percent = ? WHERE id = ?`, percent, id); err != nil {
		return fmt.Errorf("update optimize percent %d: %w", id, err)
	}
	return nil
}

// FinishTask removes a task that completed successfully (the .optimized.mp4 on disk is
// now the record).
func FinishTask(ctx context.Context, pool *sql.DB, id int64) error {
	return optimizeQueue.finish(ctx, pool, id)
}

// FailTask marks a task failed with an error message, leaving it for inspection.
func FailTask(ctx context.Context, pool *sql.DB, id int64, msg string) error {
	return optimizeQueue.fail(ctx, pool, id, msg)
}

// ListActiveTasks returns the in-flight encodes joined to their media title and file
// name, ordered by id.
func ListActiveTasks(ctx context.Context, pool *sql.DB) ([]ActiveTask, error) {
	return queryRows(ctx, pool,
		`SELECT t.id, COALESCE(m.title, ''), COALESCE(f.name, ''), t.agent, t.percent
         FROM optimize_tasks t
         LEFT JOIN media m ON m.id = t.media_id
         LEFT JOIN media_files f ON f.media_id = t.media_id AND f.idx = t.file_idx
         WHERE t.status = ? ORDER BY t.id`,
		func(r *sql.Rows) (ActiveTask, error) {
			var a ActiveTask
			return a, r.Scan(&a.ID, &a.Title, &a.File, &a.Agent, &a.Percent)
		}, OptimizeStatusEncoding)
}

// CountPending returns how many tasks are still waiting to be claimed.
func CountPending(ctx context.Context, pool *sql.DB) (int, error) {
	return optimizeQueue.countPending(ctx, pool)
}

// PruneTask removes any pending or error task for a media/file, used by the planner once
// the file has a fresh sibling or no longer needs transcoding. An encoding row is left to
// its agent.
func PruneTask(ctx context.Context, pool *sql.DB, mediaID string, fileIdx int) error {
	return optimizeQueue.pruneByMedia(ctx, pool, mediaID, "file_idx = ?", fileIdx)
}

// PruneOptimizeForMedia removes every pending/error optimize task for a media item across
// all its file indices, used by the discovery reconcile when the folder has vanished.
func PruneOptimizeForMedia(ctx context.Context, pool *sql.DB, mediaID string) error {
	return optimizeQueue.pruneByMedia(ctx, pool, mediaID, "")
}

// ResetEncodingToPending re-queues every encoding row, used at startup so a task whose
// agent died mid-encode (no one owns it after a restart) is retried.
func ResetEncodingToPending(ctx context.Context, pool *sql.DB) error {
	return optimizeQueue.resetActiveToPending(ctx, pool, "percent = 0")
}

// ClearOptimizeTasksAll empties the queue, for a full cache rebuild (the queue is
// transient and refilled by the planner from the filesystem).
func ClearOptimizeTasksAll(ctx context.Context, pool *sql.DB) error {
	return optimizeQueue.clearAll(ctx, pool)
}
