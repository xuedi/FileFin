package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Enrich task statuses. A row is pending until the agent claims it (enriching); on
// success it is deleted, on failure it becomes error. The queue is transient cache
// state, refilled by the scan from media rows still flagged enriched = 0 - the
// enriched meta.json on disk is the durable record.
const (
	EnrichStatusPending   = "pending"
	EnrichStatusEnriching = "enriching"
	EnrichStatusError     = "error"
)

// EnrichTask is one row of the enrich_tasks queue.
type EnrichTask struct {
	ID      int64
	MediaID string
	Status  string
	Agent   string
	Error   string
}

// ActiveEnrich is an in-flight enrichment for the Progress page, joined to the media
// title for display.
type ActiveEnrich struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Agent  string `json:"agent"`
	Status string `json:"status"`
}

// UpsertPendingEnrich records a media folder as a pending enrichment task. INSERT OR
// IGNORE leaves any existing row for the same media untouched (an enriching row keeps
// its agent; an error row is cleared by PruneEnrich once the folder is enriched).
func UpsertPendingEnrich(ctx context.Context, pool *sql.DB, mediaID string) error {
	if _, err := pool.ExecContext(ctx,
		`INSERT OR IGNORE INTO enrich_tasks (media_id, status, agent, error)
         VALUES (?, ?, '', '')`,
		mediaID, EnrichStatusPending); err != nil {
		return fmt.Errorf("upsert enrich %s: %w", mediaID, err)
	}
	return nil
}

// ClaimNextEnrich atomically claims the oldest pending task for agent, flipping it to
// enriching, and returns it. ok is false when none is pending.
func ClaimNextEnrich(ctx context.Context, pool *sql.DB, agent string) (EnrichTask, bool, error) {
	var t EnrichTask
	id, ok, err := enrichQueue.claim(ctx, pool, agent, "media_id", "", func(s scanner) (int64, error) {
		var id int64
		return id, s.Scan(&id, &t.MediaID)
	})
	if err != nil || !ok {
		return EnrichTask{}, ok, err
	}
	t.ID, t.Status, t.Agent = id, EnrichStatusEnriching, agent
	return t, true, nil
}

// FinishEnrich removes a task that enriched successfully (the meta.json on disk is now
// the record).
func FinishEnrich(ctx context.Context, pool *sql.DB, id int64) error {
	return enrichQueue.finish(ctx, pool, id)
}

// FailEnrich marks a task failed with a message, leaving it for inspection (not
// retried automatically).
func FailEnrich(ctx context.Context, pool *sql.DB, id int64, msg string) error {
	return enrichQueue.fail(ctx, pool, id, msg)
}

// ListActiveEnrich returns the in-flight enrichments joined to their media title.
func ListActiveEnrich(ctx context.Context, pool *sql.DB) ([]ActiveEnrich, error) {
	return queryRows(ctx, pool,
		`SELECT t.id, COALESCE(m.title, ''), t.agent, t.status
         FROM enrich_tasks t
         LEFT JOIN media m ON m.id = t.media_id
         WHERE t.status = ? ORDER BY t.id`,
		func(r *sql.Rows) (ActiveEnrich, error) {
			var a ActiveEnrich
			return a, r.Scan(&a.ID, &a.Title, &a.Agent, &a.Status)
		}, EnrichStatusEnriching)
}

// CountPendingEnrich returns how many enrichment tasks are still waiting.
func CountPendingEnrich(ctx context.Context, pool *sql.DB) (int, error) {
	return enrichQueue.countPending(ctx, pool)
}

// PruneEnrich removes any pending or error task for a media folder, used by the scan
// once the folder is enriched. An enriching row is left to its agent.
func PruneEnrich(ctx context.Context, pool *sql.DB, mediaID string) error {
	return enrichQueue.pruneByMedia(ctx, pool, mediaID, "")
}

// PruneEnrichedTasks drops pending/error tasks for media that are now enriched, so a
// re-scan does not keep stale work for folders enriched in the meantime.
func PruneEnrichedTasks(ctx context.Context, pool *sql.DB) error {
	if _, err := pool.ExecContext(ctx,
		`DELETE FROM enrich_tasks WHERE status IN (?, ?)
         AND media_id IN (SELECT id FROM media WHERE enriched = 1)`,
		EnrichStatusPending, EnrichStatusError); err != nil {
		return fmt.Errorf("prune enriched tasks: %w", err)
	}
	return nil
}

// ResetEnrichingToPending re-queues every enriching row, used at startup so a task
// whose agent died mid-lookup is retried.
func ResetEnrichingToPending(ctx context.Context, pool *sql.DB) error {
	return enrichQueue.resetActiveToPending(ctx, pool, "")
}

// ClearEnrichTasksAll empties the queue, for a full cache rebuild (the queue is
// transient and refilled by the scan from the media cache).
func ClearEnrichTasksAll(ctx context.Context, pool *sql.DB) error {
	return enrichQueue.clearAll(ctx, pool)
}
