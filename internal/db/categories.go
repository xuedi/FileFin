package db

import (
	"context"
	"database/sql"
	"fmt"
)

// schema is the cache layout. CREATE TABLE IF NOT EXISTS keeps Build idempotent.
// imports is the universal import interface (producers fill it, the importer drains
// it); media/media_files are the importer-written media cache.
const schema = `CREATE TABLE IF NOT EXISTS categories (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    alias        TEXT NOT NULL,
    other_media  INTEGER NOT NULL DEFAULT 0,
    parent_id    INTEGER,
    position     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS imports (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id  INTEGER,
    source_path  TEXT,
    filename     TEXT,
    title        TEXT,
    year         INTEGER,
    status       TEXT,
    api_json     TEXT,
    poster       TEXT,
    copied       INTEGER,
    total        INTEGER,
    error        TEXT,
    delete_after INTEGER NOT NULL DEFAULT 0,
    season       INTEGER NOT NULL DEFAULT 0,
    episode      INTEGER NOT NULL DEFAULT 0,
    subtitles    TEXT,
    origin       TEXT
);
CREATE TABLE IF NOT EXISTS media (
    id          TEXT PRIMARY KEY,
    category_id INTEGER,
    path        TEXT,
    year        INTEGER,
    title       TEXT,
    description TEXT,
    plot        TEXT,
    poster      TEXT,
    enriched    INTEGER NOT NULL DEFAULT 0,
    language    TEXT NOT NULL DEFAULT '',
    country     TEXT NOT NULL DEFAULT '',
    director    TEXT NOT NULL DEFAULT '',
    writer      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS media_facets (
    media_id TEXT,
    kind     TEXT,
    value    TEXT
);
CREATE INDEX IF NOT EXISTS idx_media_facets_kv ON media_facets (kind, value);
CREATE INDEX IF NOT EXISTS idx_media_facets_media ON media_facets (media_id);
CREATE TABLE IF NOT EXISTS media_files (
    media_id    TEXT,
    idx         INTEGER,
    path        TEXT,
    name        TEXT,
    season      INTEGER,
    episode     INTEGER,
    ext         TEXT,
    container   TEXT NOT NULL DEFAULT '',
    video_codec TEXT NOT NULL DEFAULT '',
    audio_codec TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS optimize_tasks (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    media_id       TEXT,
    file_idx       INTEGER,
    source_path    TEXT,
    optimized_path TEXT,
    status         TEXT,
    agent          TEXT,
    percent        INTEGER,
    error          TEXT,
    UNIQUE(media_id, file_idx)
);
CREATE TABLE IF NOT EXISTS enrich_tasks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    media_id     TEXT,
    status       TEXT,
    agent        TEXT,
    error        TEXT,
    attempted_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE(media_id)
);
CREATE TABLE IF NOT EXISTS thumbnail_tasks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    media_id     TEXT,
    other_media  INTEGER NOT NULL DEFAULT 0,
    status       TEXT,
    agent        TEXT,
    error        TEXT,
    UNIQUE(media_id)
);
CREATE TABLE IF NOT EXISTS probe_tasks (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    media_id TEXT,
    status   TEXT,
    agent    TEXT,
    error    TEXT,
    UNIQUE(media_id)
);
CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    alias         TEXT,
    admin         INTEGER NOT NULL DEFAULT 0,
    blocked       INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL DEFAULT 0,
    last_login_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS media_health (
    media_id        TEXT PRIMARY KEY,
    last_checked_at INTEGER NOT NULL DEFAULT 0,
    fingerprint     TEXT NOT NULL DEFAULT '',
    ok              INTEGER NOT NULL DEFAULT 0,
    issues          TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS user_state (
    user         TEXT,
    media_id     TEXT,
    watched      INTEGER NOT NULL DEFAULT 0,
    favorite     INTEGER NOT NULL DEFAULT 0,
    rating       INTEGER NOT NULL DEFAULT 0,
    has_progress INTEGER NOT NULL DEFAULT 0,
    updated      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user, media_id)
);
CREATE INDEX IF NOT EXISTS idx_user_state_user_updated ON user_state (user, updated);`

// Category is one row of the categories cache. It mirrors the authoritative config.json
// files on disk; the cache can always be rebuilt from those files. ParentID is 0 for a
// top-level category.
// On input to ReplaceCategories, OtherMedia is the category's OWN flag (from config.json);
// the stored other_media column holds the EFFECTIVE flag (the root category's flag,
// propagated down the subtree), which is what the agents read.
type Category struct {
	ID         int64
	Name       string
	ParentID   int64
	Alias      string
	OtherMedia bool
	Position   int
}

// Build runs the schema and any column migrations. It is idempotent and safe to call
// on every admin entry.
func Build(ctx context.Context, pool *sql.DB) error {
	if _, err := pool.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	return migrate(ctx, pool)
}

// migrate brings an existing cache up to the current column set. CREATE TABLE IF NOT
// EXISTS never alters a table that already exists, so columns added after a cache was
// first built are applied here. Each step is skipped when already present.
func migrate(ctx context.Context, pool *sql.DB) error {
	cols := []struct{ table, name, ddl string }{
		{"imports", "delete_after", `ALTER TABLE imports ADD COLUMN delete_after INTEGER NOT NULL DEFAULT 0`},
		{"imports", "season", `ALTER TABLE imports ADD COLUMN season INTEGER NOT NULL DEFAULT 0`},
		{"imports", "episode", `ALTER TABLE imports ADD COLUMN episode INTEGER NOT NULL DEFAULT 0`},
		{"imports", "subtitles", `ALTER TABLE imports ADD COLUMN subtitles TEXT`},
		{"imports", "origin", `ALTER TABLE imports ADD COLUMN origin TEXT`},
		{"media", "enriched", `ALTER TABLE media ADD COLUMN enriched INTEGER NOT NULL DEFAULT 0`},
		// When an enrich attempt last failed, so discovery can re-queue stale error rows. An
		// existing cache gains it as 0, which the first discovery retry sweeps in once.
		{"enrich_tasks", "attempted_at", `ALTER TABLE enrich_tasks ADD COLUMN attempted_at INTEGER NOT NULL DEFAULT 0`},
		// Denormalized facets for SQL-backed search; backfilled from meta.json by a rebuild
		// or the rolling reconcile (the multivalued actors/tags live in media_facets).
		{"media", "language", `ALTER TABLE media ADD COLUMN language TEXT NOT NULL DEFAULT ''`},
		{"media", "country", `ALTER TABLE media ADD COLUMN country TEXT NOT NULL DEFAULT ''`},
		{"media", "director", `ALTER TABLE media ADD COLUMN director TEXT NOT NULL DEFAULT ''`},
		{"media", "writer", `ALTER TABLE media ADD COLUMN writer TEXT NOT NULL DEFAULT ''`},
		{"categories", "other_media", `ALTER TABLE categories ADD COLUMN other_media INTEGER NOT NULL DEFAULT 0`},
		{"categories", "parent_id", `ALTER TABLE categories ADD COLUMN parent_id INTEGER`},
		{"categories", "position", `ALTER TABLE categories ADD COLUMN position INTEGER NOT NULL DEFAULT 0`},
		// Probed format, set at import and refreshed by the probe agent. An existing cache
		// upgrades with empty columns; an un-probed row reads as empty and the decisions
		// fall back to the filename extension until the probe agent backfills it.
		{"media_files", "container", `ALTER TABLE media_files ADD COLUMN container TEXT NOT NULL DEFAULT ''`},
		{"media_files", "video_codec", `ALTER TABLE media_files ADD COLUMN video_codec TEXT NOT NULL DEFAULT ''`},
		{"media_files", "audio_codec", `ALTER TABLE media_files ADD COLUMN audio_codec TEXT NOT NULL DEFAULT ''`},
	}
	for _, c := range cols {
		has, err := hasColumn(ctx, pool, c.table, c.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := pool.ExecContext(ctx, c.ddl); err != nil {
				return fmt.Errorf("add column %s.%s: %w", c.table, c.name, err)
			}
		}
	}
	// Columns retired after a cache was first built. media.category and imports.category
	// duplicated category_id (the name is joined from categories at read time);
	// media.folder was always filepath.Base(path). Dropped when present so an existing
	// cache matches the current schema (a full rebuild would do the same).
	dropped := []struct{ table, name string }{
		{"media", "category"},
		{"media", "folder"},
		{"imports", "category"},
	}
	for _, d := range dropped {
		has, err := hasColumn(ctx, pool, d.table, d.name)
		if err != nil {
			return err
		}
		if has {
			if _, err := pool.ExecContext(ctx, `ALTER TABLE `+d.table+` DROP COLUMN `+d.name); err != nil {
				return fmt.Errorf("drop column %s.%s: %w", d.table, d.name, err)
			}
		}
	}
	return nil
}

// SchemaVersion reads SQLite's user_version pragma, used to gate one-time data backfills
// (column migrations are handled structurally by migrate; this tracks data-level upgrades).
func SchemaVersion(ctx context.Context, pool *sql.DB) (int, error) {
	var v int
	if err := pool.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil {
		return 0, fmt.Errorf("read user_version: %w", err)
	}
	return v, nil
}

// SetSchemaVersion writes the user_version pragma. PRAGMA takes no bound parameters, so the
// integer is formatted into the statement (an internal constant, never user input).
func SetSchemaVersion(ctx context.Context, pool *sql.DB, v int) error {
	if _, err := pool.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, v)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}
	return nil
}

// hasColumn reports whether a table already has a named column.
func hasColumn(ctx context.Context, pool *sql.DB, table, column string) (bool, error) {
	rows, err := pool.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("table_info %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("scan table_info %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// CountCategories returns the number of cached category rows.
func CountCategories(ctx context.Context, pool *sql.DB) (int, error) {
	var n int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM categories`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count categories: %w", err)
	}
	return n, nil
}

// InsertCategory inserts a category under parentID (0 = top level) and returns the
// auto-assigned id.
func InsertCategory(ctx context.Context, pool *sql.DB, name, alias string, parentID int64) (int64, error) {
	res, err := pool.ExecContext(ctx,
		`INSERT INTO categories (name, alias, parent_id) VALUES (?, ?, ?)`, name, alias, nullID(parentID))
	if err != nil {
		return 0, fmt.Errorf("insert category %q: %w", name, err)
	}
	return res.LastInsertId()
}

// nullID maps 0 to SQL NULL (top-level / no parent) and any other id to itself.
func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// UpdateCategoryAlias updates the cached alias and other-media flag for a category.
func UpdateCategoryAlias(ctx context.Context, pool *sql.DB, name, alias string, otherMedia bool) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE categories SET alias = ?, other_media = ? WHERE name = ?`, alias, otherMedia, name)
	if err != nil {
		return fmt.Errorf("update category alias %q: %w", name, err)
	}
	return nil
}

// DeleteCategory removes a category from the cache.
func DeleteCategory(ctx context.Context, pool *sql.DB, name string) error {
	_, err := pool.ExecContext(ctx, `DELETE FROM categories WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete category %q: %w", name, err)
	}
	return nil
}

// ReplaceCategories rebuilds the cache from the filesystem: it wipes the table and
// re-inserts every category with its stored id and parent id, so ids survive the rebuild.
// Each row's stored other_media is the EFFECTIVE flag - the root category's own flag,
// propagated down the subtree - computed here from the parent links and each category's
// own flag.
func ReplaceCategories(ctx context.Context, pool *sql.DB, cats []Category) error {
	own := make(map[int64]Category, len(cats))
	for _, c := range cats {
		own[c.ID] = c
	}
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace categories: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM categories`); err != nil {
		return fmt.Errorf("clear categories: %w", err)
	}
	for _, c := range cats {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO categories (id, name, alias, other_media, parent_id, position) VALUES (?, ?, ?, ?, ?, ?)`,
			c.ID, c.Name, c.Alias, effectiveOtherMedia(c, own), nullID(c.ParentID), c.Position); err != nil {
			return fmt.Errorf("insert category %q: %w", c.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace categories: %w", err)
	}
	return nil
}

// effectiveOtherMedia walks c up to its root through the own map and returns the root's
// own other-media flag - the value every category in a subtree inherits. A broken parent
// link (missing id) or a cycle stops the walk at the last reachable node.
func effectiveOtherMedia(c Category, own map[int64]Category) bool {
	seen := map[int64]bool{}
	for c.ParentID != 0 && !seen[c.ID] {
		seen[c.ID] = true
		parent, ok := own[c.ParentID]
		if !ok {
			break
		}
		c = parent
	}
	return c.OtherMedia
}
