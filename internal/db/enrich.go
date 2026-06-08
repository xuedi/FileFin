package db

import (
	"context"
	"database/sql"
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
	_, err := pool.ExecContext(ctx,
		`INSERT OR IGNORE INTO enrich_tasks (media_id, status, agent, error)
         VALUES (?, ?, '', '')`,
		mediaID, EnrichStatusPending)
	return err
}

// ClaimNextEnrich atomically claims the oldest pending task for agent, flipping it to
// enriching, and returns it. ok is false when none is pending. The single cache
// connection makes the read-then-claim race-free.
func ClaimNextEnrich(ctx context.Context, pool *sql.DB, agent string) (EnrichTask, bool, error) {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return EnrichTask{}, false, err
	}
	defer tx.Rollback()

	var t EnrichTask
	err = tx.QueryRowContext(ctx,
		`SELECT id, media_id FROM enrich_tasks WHERE status = ? ORDER BY id LIMIT 1`,
		EnrichStatusPending).Scan(&t.ID, &t.MediaID)
	if err == sql.ErrNoRows {
		return EnrichTask{}, false, nil
	}
	if err != nil {
		return EnrichTask{}, false, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE enrich_tasks SET status = ?, agent = ?, error = '' WHERE id = ?`,
		EnrichStatusEnriching, agent, t.ID); err != nil {
		return EnrichTask{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return EnrichTask{}, false, err
	}
	t.Status, t.Agent = EnrichStatusEnriching, agent
	return t, true, nil
}

// FinishEnrich removes a task that enriched successfully (the meta.json on disk is now
// the record).
func FinishEnrich(ctx context.Context, pool *sql.DB, id int64) error {
	_, err := pool.ExecContext(ctx, `DELETE FROM enrich_tasks WHERE id = ?`, id)
	return err
}

// FailEnrich marks a task failed with a message, leaving it for inspection (not
// retried automatically).
func FailEnrich(ctx context.Context, pool *sql.DB, id int64, msg string) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE enrich_tasks SET status = ?, agent = '', error = ? WHERE id = ?`,
		EnrichStatusError, msg, id)
	return err
}

// ListActiveEnrich returns the in-flight enrichments joined to their media title.
func ListActiveEnrich(ctx context.Context, pool *sql.DB) ([]ActiveEnrich, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT t.id, COALESCE(m.title, ''), t.agent, t.status
         FROM enrich_tasks t
         LEFT JOIN media m ON m.id = t.media_id
         WHERE t.status = ? ORDER BY t.id`, EnrichStatusEnriching)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActiveEnrich{}
	for rows.Next() {
		var a ActiveEnrich
		if err := rows.Scan(&a.ID, &a.Title, &a.Agent, &a.Status); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountPendingEnrich returns how many enrichment tasks are still waiting.
func CountPendingEnrich(ctx context.Context, pool *sql.DB) (int, error) {
	var n int
	err := pool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM enrich_tasks WHERE status = ?`, EnrichStatusPending).Scan(&n)
	return n, err
}

// PruneEnrich removes any pending or error task for a media folder, used by the scan
// once the folder is enriched. An enriching row is left to its agent.
func PruneEnrich(ctx context.Context, pool *sql.DB, mediaID string) error {
	_, err := pool.ExecContext(ctx,
		`DELETE FROM enrich_tasks WHERE media_id = ? AND status IN (?, ?)`,
		mediaID, EnrichStatusPending, EnrichStatusError)
	return err
}

// PruneEnrichedTasks drops pending/error tasks for media that are now enriched, so a
// re-scan does not keep stale work for folders enriched in the meantime.
func PruneEnrichedTasks(ctx context.Context, pool *sql.DB) error {
	_, err := pool.ExecContext(ctx,
		`DELETE FROM enrich_tasks WHERE status IN (?, ?)
         AND media_id IN (SELECT id FROM media WHERE enriched = 1)`,
		EnrichStatusPending, EnrichStatusError)
	return err
}

// ResetEnrichingToPending re-queues every enriching row, used at startup so a task
// whose agent died mid-lookup is retried.
func ResetEnrichingToPending(ctx context.Context, pool *sql.DB) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE enrich_tasks SET status = ?, agent = '' WHERE status = ?`,
		EnrichStatusPending, EnrichStatusEnriching)
	return err
}

// ClearEnrichTasksAll empties the queue, for a full cache rebuild (the queue is
// transient and refilled by the scan from the media cache).
func ClearEnrichTasksAll(ctx context.Context, pool *sql.DB) error {
	_, err := pool.ExecContext(ctx, `DELETE FROM enrich_tasks`)
	return err
}
