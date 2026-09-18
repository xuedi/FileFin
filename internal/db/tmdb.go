package db

import (
	"context"
	"database/sql"
	"fmt"
)

// TMDb task statuses. A row is pending until the agent claims it (matching); on success it
// is deleted, on failure it becomes error. The queue is transient cache state, refilled by the
// scan from items whose meta.json has no current TMDb snapshot - the snapshot on disk is the
// durable record.
const (
	TMDbStatusPending  = "pending"
	TMDbStatusMatching = "matching"
	TMDbStatusError    = "error"
)

// TMDbTask is one row of the tmdb_tasks queue.
type TMDbTask struct {
	ID      int64
	MediaID string
	Status  string
	Agent   string
	Error   string
}

// ActiveTMDb is an in-flight TMDb match for the Progress page, joined to the media title
// for display.
type ActiveTMDb struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Agent  string `json:"agent"`
	Status string `json:"status"`
}

// UpsertPendingTMDb records a media folder as a pending TMDb task. Unlike the other
// queues an error row is re-armed: its failures are transient (the API or the network), and
// the item still needing work is exactly why the scan came back to it. A matching row keeps
// its agent.
func UpsertPendingTMDb(ctx context.Context, pool *sql.DB, mediaID string) error {
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO tmdb_tasks (media_id, status, agent, error) VALUES (?, ?, '', '')
         ON CONFLICT(media_id) DO UPDATE SET status = excluded.status, error = ''
         WHERE tmdb_tasks.status = ?`,
		mediaID, TMDbStatusPending, TMDbStatusError); err != nil {
		return fmt.Errorf("upsert tmdb %s: %w", mediaID, err)
	}
	return nil
}

// ClaimNextTMDb atomically claims the oldest pending task for agent, flipping it to
// matching, and returns it. ok is false when none is pending.
func ClaimNextTMDb(ctx context.Context, pool *sql.DB, agent string) (TMDbTask, bool, error) {
	var t TMDbTask
	id, ok, err := tmdbQueue.claim(ctx, pool, agent, "media_id", "", func(s scanner) (int64, error) {
		var id int64
		return id, s.Scan(&id, &t.MediaID)
	})
	if err != nil || !ok {
		return TMDbTask{}, ok, err
	}
	t.ID, t.Status, t.Agent = id, TMDbStatusMatching, agent
	return t, true, nil
}

// FinishTMDb removes a task that matched successfully.
func FinishTMDb(ctx context.Context, pool *sql.DB, id int64) error {
	return tmdbQueue.finish(ctx, pool, id)
}

// FailTMDb marks a task failed with a message; the next scan re-arms it.
func FailTMDb(ctx context.Context, pool *sql.DB, id int64, msg string) error {
	return tmdbQueue.fail(ctx, pool, id, msg)
}

// ListActiveTMDb returns the in-flight TMDb matches joined to their media title.
func ListActiveTMDb(ctx context.Context, pool *sql.DB) ([]ActiveTMDb, error) {
	return queryRows(ctx, pool,
		`SELECT t.id, COALESCE(m.title, ''), t.agent, t.status
         FROM tmdb_tasks t
         LEFT JOIN media m ON m.id = t.media_id
         WHERE t.status = ? ORDER BY t.id`,
		func(r *sql.Rows) (ActiveTMDb, error) {
			var a ActiveTMDb
			return a, r.Scan(&a.ID, &a.Title, &a.Agent, &a.Status)
		}, TMDbStatusMatching)
}

// CountPendingTMDb returns how many TMDb tasks are still waiting.
func CountPendingTMDb(ctx context.Context, pool *sql.DB) (int, error) {
	return tmdbQueue.countPending(ctx, pool)
}

// PruneTMDb removes any pending or error task for a media folder, used by the scan once
// the folder has a current TMDb snapshot (or by the reconcile when the folder has vanished). A
// matching row is left to its agent.
func PruneTMDb(ctx context.Context, pool *sql.DB, mediaID string) error {
	return tmdbQueue.pruneByMedia(ctx, pool, mediaID, "")
}

// ResetMatchingToPending re-queues every matching row, used at startup so a task whose
// agent died mid-match is retried.
func ResetMatchingToPending(ctx context.Context, pool *sql.DB) error {
	return tmdbQueue.resetActiveToPending(ctx, pool, "")
}

// ClearTMDbTasksAll empties the queue, for a full cache rebuild.
func ClearTMDbTasksAll(ctx context.Context, pool *sql.DB) error {
	return tmdbQueue.clearAll(ctx, pool)
}
