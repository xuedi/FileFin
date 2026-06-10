package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Probe task statuses. A row is pending until the agent claims it (probing); on success it
// is deleted, on failure it becomes error (left for the admin to see). The queue is
// transient cache state, refilled by the scan from media files whose format columns are
// empty or whose meta.json technical block is incomplete - the probed columns and the
// meta.json technical block on disk are the durable record.
const (
	ProbeStatusPending = "pending"
	ProbeStatusProbing = "probing"
	ProbeStatusError   = "error"
)

// ProbeTask is one row of the probe_tasks queue.
type ProbeTask struct {
	ID      int64
	MediaID string
	Status  string
	Agent   string
	Error   string
}

// ActiveProbe is an in-flight probe for the Progress page, joined to the media title for
// display.
type ActiveProbe struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Agent  string `json:"agent"`
	Status string `json:"status"`
}

// UpsertPendingProbe records a media folder as a pending probe task. INSERT OR IGNORE
// leaves any existing row for the same media untouched (a probing row keeps its agent; an
// error row is cleared by PruneProbe once the folder's format is complete).
func UpsertPendingProbe(ctx context.Context, pool *sql.DB, mediaID string) error {
	if _, err := pool.ExecContext(ctx,
		`INSERT OR IGNORE INTO probe_tasks (media_id, status, agent, error)
         VALUES (?, ?, '', '')`,
		mediaID, ProbeStatusPending); err != nil {
		return fmt.Errorf("upsert probe %s: %w", mediaID, err)
	}
	return nil
}

// ClaimNextProbe atomically claims the oldest pending task for agent, flipping it to
// probing, and returns it. ok is false when none is pending.
func ClaimNextProbe(ctx context.Context, pool *sql.DB, agent string) (ProbeTask, bool, error) {
	var t ProbeTask
	id, ok, err := probeQueue.claim(ctx, pool, agent, "media_id", "", func(s scanner) (int64, error) {
		var id int64
		return id, s.Scan(&id, &t.MediaID)
	})
	if err != nil || !ok {
		return ProbeTask{}, ok, err
	}
	t.ID, t.Status, t.Agent = id, ProbeStatusProbing, agent
	return t, true, nil
}

// FinishProbe removes a task that probed successfully (the format columns and meta.json
// technical block are now the record).
func FinishProbe(ctx context.Context, pool *sql.DB, id int64) error {
	return probeQueue.finish(ctx, pool, id)
}

// FailProbe marks a task failed with a message, leaving it for inspection (not retried
// automatically).
func FailProbe(ctx context.Context, pool *sql.DB, id int64, msg string) error {
	return probeQueue.fail(ctx, pool, id, msg)
}

// ListActiveProbe returns the in-flight probes joined to their media title.
func ListActiveProbe(ctx context.Context, pool *sql.DB) ([]ActiveProbe, error) {
	return queryRows(ctx, pool,
		`SELECT t.id, COALESCE(m.title, ''), t.agent, t.status
         FROM probe_tasks t
         LEFT JOIN media m ON m.id = t.media_id
         WHERE t.status = ? ORDER BY t.id`,
		func(r *sql.Rows) (ActiveProbe, error) {
			var a ActiveProbe
			return a, r.Scan(&a.ID, &a.Title, &a.Agent, &a.Status)
		}, ProbeStatusProbing)
}

// CountPendingProbe returns how many probe tasks are still waiting.
func CountPendingProbe(ctx context.Context, pool *sql.DB) (int, error) {
	return probeQueue.countPending(ctx, pool)
}

// PruneProbe removes any pending or error task for a media folder, used by the scan once
// the folder's format is complete (or by the reconcile when the folder has vanished). A
// probing row is left to its agent.
func PruneProbe(ctx context.Context, pool *sql.DB, mediaID string) error {
	return probeQueue.pruneByMedia(ctx, pool, mediaID, "")
}

// ResetProbingToPending re-queues every probing row, used at startup so a task whose agent
// died mid-probe is retried.
func ResetProbingToPending(ctx context.Context, pool *sql.DB) error {
	return probeQueue.resetActiveToPending(ctx, pool, "")
}

// ClearProbeTasksAll empties the queue, for a full cache rebuild (the queue is transient
// and refilled by the scan from the media cache).
func ClearProbeTasksAll(ctx context.Context, pool *sql.DB) error {
	return probeQueue.clearAll(ctx, pool)
}
