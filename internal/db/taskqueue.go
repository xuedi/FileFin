package db

import (
	"context"
	"database/sql"
	"fmt"
)

// scanner is the common subset of *sql.Row and *sql.Rows used by the queue helpers, so
// one scan callback works for both a single-row claim and a multi-row list.
type scanner interface{ Scan(...any) error }

// taskQueue captures the parts that differ between the three otherwise-identical
// background work queues (enrich, optimize, thumbnail): the table name and its
// pending/active/error status strings. The operations that are byte-for-byte identical
// across the queues live as methods here; the column-specific ones (claim/list/upsert)
// stay as thin per-queue wrappers that supply their columns to these helpers.
type taskQueue struct {
	table   string
	pending string
	active  string
	errored string
}

var (
	enrichQueue    = taskQueue{table: "enrich_tasks", pending: EnrichStatusPending, active: EnrichStatusEnriching, errored: EnrichStatusError}
	optimizeQueue  = taskQueue{table: "optimize_tasks", pending: OptimizeStatusPending, active: OptimizeStatusEncoding, errored: OptimizeStatusError}
	thumbnailQueue = taskQueue{table: "thumbnail_tasks", pending: ThumbStatusPending, active: ThumbStatusGenerating, errored: ThumbStatusError}
	probeQueue     = taskQueue{table: "probe_tasks", pending: ProbeStatusPending, active: ProbeStatusProbing, errored: ProbeStatusError}
)

// claim runs the race-free read-then-flip transaction shared by every queue: it selects
// the oldest pending row (its id plus selectCols), flips it to active (agent set, error
// cleared, plus any extraSet such as "percent = 0"), and commits, returning the claimed
// id. ok is false when nothing is pending. The single cache connection (MaxOpenConns(1))
// makes the select-then-update atomic: two agents can never claim the same row. The
// deferred Rollback is a no-op once Commit succeeds and undoes every early-return error
// path - the idiom a SQLite transaction needs.
func (q taskQueue) claim(ctx context.Context, pool *sql.DB, agent, selectCols, extraSet string, scan func(scanner) (int64, error)) (int64, bool, error) {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("begin claim %s: %w", q.table, err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx,
		`SELECT id, `+selectCols+` FROM `+q.table+` WHERE status = ? ORDER BY id LIMIT 1`, q.pending)
	id, err := scan(row)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("claim %s: %w", q.table, err)
	}
	set := "status = ?, agent = ?, error = ''"
	if extraSet != "" {
		set += ", " + extraSet
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE `+q.table+` SET `+set+` WHERE id = ?`, q.active, agent, id); err != nil {
		return 0, false, fmt.Errorf("flip %s %d to active: %w", q.table, id, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, false, fmt.Errorf("commit claim %s: %w", q.table, err)
	}
	return id, true, nil
}

// finish removes a task that completed successfully (the on-disk artifact is now the
// record).
func (q taskQueue) finish(ctx context.Context, pool *sql.DB, id int64) error {
	if _, err := pool.ExecContext(ctx, `DELETE FROM `+q.table+` WHERE id = ?`, id); err != nil {
		return fmt.Errorf("finish %s %d: %w", q.table, id, err)
	}
	return nil
}

// fail marks a task failed with a message, leaving it for inspection (not retried
// automatically).
func (q taskQueue) fail(ctx context.Context, pool *sql.DB, id int64, msg string) error {
	if _, err := pool.ExecContext(ctx,
		`UPDATE `+q.table+` SET status = ?, agent = '', error = ? WHERE id = ?`,
		q.errored, msg, id); err != nil {
		return fmt.Errorf("fail %s %d: %w", q.table, id, err)
	}
	return nil
}

// countPending returns how many tasks are still waiting to be claimed.
func (q taskQueue) countPending(ctx context.Context, pool *sql.DB) (int, error) {
	var n int
	if err := pool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+q.table+` WHERE status = ?`, q.pending).Scan(&n); err != nil {
		return 0, fmt.Errorf("count pending %s: %w", q.table, err)
	}
	return n, nil
}

// pruneByMedia removes any pending or error task for a media folder (optionally narrowed
// by extraWhere, e.g. a file index), used by the scan once the folder no longer needs
// work. An active row is left to its agent.
func (q taskQueue) pruneByMedia(ctx context.Context, pool *sql.DB, mediaID, extraWhere string, extraArgs ...any) error {
	args := append([]any{mediaID}, extraArgs...)
	args = append(args, q.pending, q.errored)
	where := `media_id = ?`
	if extraWhere != "" {
		where += ` AND ` + extraWhere
	}
	if _, err := pool.ExecContext(ctx,
		`DELETE FROM `+q.table+` WHERE `+where+` AND status IN (?, ?)`, args...); err != nil {
		return fmt.Errorf("prune %s %s: %w", q.table, mediaID, err)
	}
	return nil
}

// resetActiveToPending re-queues every active row (plus any extraSet such as
// "percent = 0"), used at startup so a task whose agent died mid-run is retried.
func (q taskQueue) resetActiveToPending(ctx context.Context, pool *sql.DB, extraSet string) error {
	set := "status = ?, agent = ''"
	if extraSet != "" {
		set += ", " + extraSet
	}
	if _, err := pool.ExecContext(ctx,
		`UPDATE `+q.table+` SET `+set+` WHERE status = ?`, q.pending, q.active); err != nil {
		return fmt.Errorf("reset %s to pending: %w", q.table, err)
	}
	return nil
}

// clearAll empties the queue, for a full cache rebuild (the queue is transient and
// refilled by the scan).
func (q taskQueue) clearAll(ctx context.Context, pool *sql.DB) error {
	if _, err := pool.ExecContext(ctx, `DELETE FROM `+q.table); err != nil {
		return fmt.Errorf("clear %s: %w", q.table, err)
	}
	return nil
}

// queryRows runs a query and scans every row through scan into a slice, with the
// defer-Close + rows.Err discipline written once. The element type is inferred from scan.
func queryRows[T any](ctx context.Context, pool *sql.DB, query string, scan func(*sql.Rows) (T, error), args ...any) ([]T, error) {
	rows, err := pool.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
