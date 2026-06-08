package db

import (
	"context"
	"database/sql"
)

// User is one row of the users cache. The cache mirrors the authoritative config
// (~/.filefin.json); its only load-bearing job is minting the auto-increment id, which
// is written back into the config. It can always be rebuilt from the config.
type User struct {
	ID          int64
	Username    string
	Alias       string
	Admin       bool
	Blocked     bool
	CreatedAt   int64
	LastLoginAt int64
}

// InsertUser inserts a user (id auto-assigned) and returns the new id. Used to mint a
// stable id the caller writes back into the config.
func InsertUser(ctx context.Context, pool *sql.DB, u User) (int64, error) {
	res, err := pool.ExecContext(ctx,
		`INSERT INTO users (username, alias, admin, blocked, created_at, last_login_at)
         VALUES (?, ?, ?, ?, ?, ?)`,
		u.Username, u.Alias, u.Admin, u.Blocked, u.CreatedAt, u.LastLoginAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpsertUser writes a user row at an explicit id, replacing any existing row. Used to
// re-seed the mirror from the config (preserving the config's id) and to reflect edits.
func UpsertUser(ctx context.Context, pool *sql.DB, u User) error {
	_, err := pool.ExecContext(ctx,
		`INSERT OR REPLACE INTO users (id, username, alias, admin, blocked, created_at, last_login_at)
         VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.Alias, u.Admin, u.Blocked, u.CreatedAt, u.LastLoginAt)
	return err
}

// TouchUserLogin records a user's last-login time in the mirror (best-effort; the
// config is the source of truth).
func TouchUserLogin(ctx context.Context, pool *sql.DB, username string, ts int64) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE username = ?`, ts, username)
	return err
}

// DeleteUserByID removes a mirror row by id, used to prune accounts the config no longer
// has (e.g. a stale row left in the disposable cache after a reinstall).
func DeleteUserByID(ctx context.Context, pool *sql.DB, id int64) error {
	_, err := pool.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

// ListUsers returns the mirrored users ordered by id. The admin page reads accounts
// from the config (the source of truth); this is for verifying the mirror and rebuilds.
func ListUsers(ctx context.Context, pool *sql.DB) ([]User, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT id, username, alias, admin, blocked, created_at, last_login_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Alias, &u.Admin, &u.Blocked, &u.CreatedAt, &u.LastLoginAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
