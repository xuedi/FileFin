package db

import (
	"context"
	"database/sql"
	"fmt"
)

// People task statuses. A row is pending until the agent claims it (resolving); on success
// it is deleted, on failure it becomes error. The queue is transient cache state, refilled by
// the scan from items whose meta.json has no current TMDb cast or names a person the shared
// people store does not hold yet - the cast block and the people store on disk are the
// durable record.
const (
	PeopleStatusPending   = "pending"
	PeopleStatusResolving = "resolving"
	PeopleStatusError     = "error"
)

// PeopleTask is one row of the people_tasks queue.
type PeopleTask struct {
	ID      int64
	MediaID string
	Status  string
	Agent   string
	Error   string
}

// ActivePeople is an in-flight cast resolve for the Progress page, joined to the media title
// for display.
type ActivePeople struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Agent  string `json:"agent"`
	Status string `json:"status"`
}

// UpsertPendingPeople records a media folder as a pending people task. Unlike the other
// queues an error row is re-armed: its failures are transient (the API or the network), and
// the item still needing work is exactly why the scan came back to it. A resolving row keeps
// its agent.
func UpsertPendingPeople(ctx context.Context, pool *sql.DB, mediaID string) error {
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO people_tasks (media_id, status, agent, error) VALUES (?, ?, '', '')
         ON CONFLICT(media_id) DO UPDATE SET status = excluded.status, error = ''
         WHERE people_tasks.status = ?`,
		mediaID, PeopleStatusPending, PeopleStatusError); err != nil {
		return fmt.Errorf("upsert people %s: %w", mediaID, err)
	}
	return nil
}

// ClaimNextPeople atomically claims the oldest pending task for agent, flipping it to
// resolving, and returns it. ok is false when none is pending.
func ClaimNextPeople(ctx context.Context, pool *sql.DB, agent string) (PeopleTask, bool, error) {
	var t PeopleTask
	id, ok, err := peopleQueue.claim(ctx, pool, agent, "media_id", "", func(s scanner) (int64, error) {
		var id int64
		return id, s.Scan(&id, &t.MediaID)
	})
	if err != nil || !ok {
		return PeopleTask{}, ok, err
	}
	t.ID, t.Status, t.Agent = id, PeopleStatusResolving, agent
	return t, true, nil
}

// FinishPeople removes a task that resolved successfully.
func FinishPeople(ctx context.Context, pool *sql.DB, id int64) error {
	return peopleQueue.finish(ctx, pool, id)
}

// FailPeople marks a task failed with a message; the next scan re-arms it.
func FailPeople(ctx context.Context, pool *sql.DB, id int64, msg string) error {
	return peopleQueue.fail(ctx, pool, id, msg)
}

// ListActivePeople returns the in-flight cast resolves joined to their media title.
func ListActivePeople(ctx context.Context, pool *sql.DB) ([]ActivePeople, error) {
	return queryRows(ctx, pool,
		`SELECT t.id, COALESCE(m.title, ''), t.agent, t.status
         FROM people_tasks t
         LEFT JOIN media m ON m.id = t.media_id
         WHERE t.status = ? ORDER BY t.id`,
		func(r *sql.Rows) (ActivePeople, error) {
			var a ActivePeople
			return a, r.Scan(&a.ID, &a.Title, &a.Agent, &a.Status)
		}, PeopleStatusResolving)
}

// CountPendingPeople returns how many people tasks are still waiting.
func CountPendingPeople(ctx context.Context, pool *sql.DB) (int, error) {
	return peopleQueue.countPending(ctx, pool)
}

// PrunePeople removes any pending or error task for a media folder, used by the scan once
// the folder's cast is complete (or by the reconcile when the folder has vanished). A
// resolving row is left to its agent.
func PrunePeople(ctx context.Context, pool *sql.DB, mediaID string) error {
	return peopleQueue.pruneByMedia(ctx, pool, mediaID, "")
}

// ResetResolvingToPending re-queues every resolving row, used at startup so a task whose
// agent died mid-resolve is retried.
func ResetResolvingToPending(ctx context.Context, pool *sql.DB) error {
	return peopleQueue.resetActiveToPending(ctx, pool, "")
}

// ClearPeopleTasksAll empties the queue, for a full cache rebuild.
func ClearPeopleTasksAll(ctx context.Context, pool *sql.DB) error {
	return peopleQueue.clearAll(ctx, pool)
}
