package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Import status values. preCheck rows are produced by an assessment (a producer)
// and do not import until the admin presses Start, which flips them to import; the
// poller then drives import -> importing -> done/error.
const (
	StatusPreCheck  = "preCheck"
	StatusImport    = "import"
	StatusImporting = "importing"
	StatusDone      = "done"
	StatusError     = "error"
)

// Import origins record which front stage produced a row. The importer ignores
// these; the UI uses them to drive source-specific affordances.
const (
	OriginFolder   = "folder"
	OriginUpload   = "upload"
	OriginPlex     = "plex"
	OriginJellyfin = "jellyfin"
)

// Import is one row of the imports table: a single media file staged for import.
// It is the only contract the importer depends on, regardless of which producer
// (folder scan, Plex, Jellyfin) wrote it. Poster holds a downloaded temp poster
// path (/tmp/{id}.{ext}) that the importer copies into the media folder.
type Import struct {
	ID         int64  `json:"id"`
	CategoryID int64  `json:"categoryId"`
	Category   string `json:"category"`
	SourcePath string `json:"sourcePath"`
	Filename   string `json:"filename"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	Status     string `json:"status"`
	APIJSON    string `json:"-"`
	Poster     string `json:"-"`
	Copied     int64  `json:"copied"`
	Total      int64  `json:"total"`
	Error      string `json:"error"`
	HasPoster  bool   `json:"hasPoster"`
	// Season and Episode are non-zero when the file was recognised as a TV episode;
	// the importer writes them into the media file name and groups episodes of one
	// show into a single folder.
	Season  int `json:"season"`
	Episode int `json:"episode"`
	// Subtitles is the JSON-encoded list of sidecar subtitles discovered beside the
	// source file (importer.Subtitle), staged to ride along like the poster. The
	// importer unmarshals and places them. SubCount/HasSubtitles are derived for the UI.
	Subtitles    string `json:"-"`
	SubCount     int    `json:"subCount"`
	HasSubtitles bool   `json:"hasSubtitles"`
	// DeleteAfter removes the source file once the import succeeds. The import folder
	// is a vacuum: media staged there is meant to be cleared after copy. Defaults to
	// false so producers that import from a library someone keeps leave originals be.
	DeleteAfter bool `json:"deleteAfter"`
	// Origin records which front stage produced the row ("folder", "upload", "plex").
	// The importer and preCheck page treat every row the same, but the UI uses it to
	// drive source-specific affordances (e.g. Plex locks delete-after off).
	Origin string `json:"origin"`
	// Duplicate names the library item this row would import a second time, empty when
	// the media is new. It is derived when rows are handed to the preCheck page, never
	// stored: the library and the row's own title/year both keep changing, so a stale
	// answer would be worse than none.
	Duplicate string `json:"duplicate"`
}

// InsertImport inserts a staged import row and returns its new id.
func InsertImport(ctx context.Context, pool *sql.DB, imp Import) (int64, error) {
	res, err := pool.ExecContext(ctx,
		`INSERT INTO imports
            (category_id, source_path, filename, title, year, status, api_json, poster, copied, total, error, delete_after, season, episode, subtitles, origin)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		imp.CategoryID, imp.SourcePath, imp.Filename, imp.Title, imp.Year,
		imp.Status, imp.APIJSON, imp.Poster, imp.Copied, imp.Total, imp.Error, imp.DeleteAfter,
		imp.Season, imp.Episode, imp.Subtitles, imp.Origin)
	if err != nil {
		return 0, fmt.Errorf("insert import %q: %w", imp.SourcePath, err)
	}
	return res.LastInsertId()
}

// importSelect reads every import row joined to its category so Category carries the
// live category name (relpath); category is no longer stored on the row. A LEFT JOIN
// keeps rows whose category_id is unset, yielding an empty Category.
const importSelect = `SELECT i.id, i.category_id, c.name, i.source_path, i.filename, i.title, i.year, i.status, i.api_json, i.poster, i.copied, i.total, i.error, i.delete_after, i.season, i.episode, i.subtitles, i.origin FROM imports i LEFT JOIN categories c ON c.id = i.category_id`

func scanImport(rows interface{ Scan(...any) error }) (Import, error) {
	var imp Import
	var category, subtitles, origin sql.NullString
	err := rows.Scan(&imp.ID, &imp.CategoryID, &category, &imp.SourcePath, &imp.Filename,
		&imp.Title, &imp.Year, &imp.Status, &imp.APIJSON, &imp.Poster, &imp.Copied, &imp.Total, &imp.Error,
		&imp.DeleteAfter, &imp.Season, &imp.Episode, &subtitles, &origin)
	imp.HasPoster = imp.Poster != ""
	imp.Category = category.String
	imp.Subtitles = subtitles.String
	imp.Origin = origin.String
	imp.SubCount = countJSONArray(imp.Subtitles)
	imp.HasSubtitles = imp.SubCount > 0
	return imp, err
}

// countJSONArray returns the element count of a JSON array stored in a column,
// 0 for empty or malformed values - used to surface a subtitle count without the
// db package depending on the importer's Subtitle type.
func countJSONArray(s string) int {
	if s == "" {
		return 0
	}
	var raw []json.RawMessage
	if json.Unmarshal([]byte(s), &raw) != nil {
		return 0
	}
	return len(raw)
}

// ListImports returns import rows. An empty status returns every row; otherwise it
// filters to that status. Rows are ordered by id (insertion order).
func ListImports(ctx context.Context, pool *sql.DB, status string) ([]Import, error) {
	q := importSelect
	var args []any
	if status != "" {
		q += ` WHERE i.status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY i.id`
	rows, err := pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query imports: %w", err)
	}
	defer rows.Close()
	out := []Import{}
	for rows.Next() {
		imp, err := scanImport(rows)
		if err != nil {
			return nil, fmt.Errorf("scan import: %w", err)
		}
		out = append(out, imp)
	}
	return out, rows.Err()
}

// ListActiveImports returns rows still in flight (import or importing) for the
// Progress page.
func ListActiveImports(ctx context.Context, pool *sql.DB) ([]Import, error) {
	rows, err := pool.QueryContext(ctx,
		importSelect+` WHERE i.status IN (?, ?) ORDER BY i.id`,
		StatusImport, StatusImporting)
	if err != nil {
		return nil, fmt.Errorf("query active imports: %w", err)
	}
	defer rows.Close()
	out := []Import{}
	for rows.Next() {
		imp, err := scanImport(rows)
		if err != nil {
			return nil, fmt.Errorf("scan import: %w", err)
		}
		out = append(out, imp)
	}
	return out, rows.Err()
}

// GetImport returns a single import row by id.
func GetImport(ctx context.Context, pool *sql.DB, id int64) (Import, error) {
	row := pool.QueryRowContext(ctx, importSelect+` WHERE i.id = ?`, id)
	imp, err := scanImport(row)
	if err == sql.ErrNoRows {
		return Import{}, err
	}
	if err != nil {
		return Import{}, fmt.Errorf("get import %d: %w", id, err)
	}
	return imp, nil
}

// UpdateImportFields updates the user-editable title/year of a staged row.
func UpdateImportFields(ctx context.Context, pool *sql.DB, id int64, title string, year int) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE imports SET title = ?, year = ? WHERE id = ?`, title, year, id)
	if err != nil {
		return fmt.Errorf("update import fields %d: %w", id, err)
	}
	return nil
}

// UpdateImportCategory moves a staged row to a different category.
func UpdateImportCategory(ctx context.Context, pool *sql.DB, id, categoryID int64) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE imports SET category_id = ? WHERE id = ?`, categoryID, id)
	if err != nil {
		return fmt.Errorf("update import category %d: %w", id, err)
	}
	return nil
}

// UpdateImportProgress records the import agent's progress and terminal state for a row.
func UpdateImportProgress(ctx context.Context, pool *sql.DB, id int64, status string, copied, total int64, errMsg string) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE imports SET status = ?, copied = ?, total = ?, error = ? WHERE id = ?`,
		status, copied, total, errMsg, id)
	if err != nil {
		return fmt.Errorf("update import progress %d: %w", id, err)
	}
	return nil
}

// ResetInterruptedImports flips rows left mid-copy (importing) back to import so the
// poller re-copies them, and returns how many were recovered. A row reaches importing
// only while a copy is in flight; if the process stops then, nothing would ever resume
// it, so this runs once at startup. Re-copying is safe: CopyFile writes a .part temp and
// renames atomically, so a half-written file is never left behind.
func ResetInterruptedImports(ctx context.Context, pool *sql.DB) (int64, error) {
	res, err := pool.ExecContext(ctx,
		`UPDATE imports SET status = ?, copied = 0, total = 0, error = '' WHERE status = ?`,
		StatusImport, StatusImporting)
	if err != nil {
		return 0, fmt.Errorf("reset interrupted imports: %w", err)
	}
	return res.RowsAffected()
}

// SetImportStatus bulk-flips rows from one status to another (e.g. preCheck ->
// import when Start is pressed) and returns how many rows changed.
func SetImportStatus(ctx context.Context, pool *sql.DB, from, to string) (int64, error) {
	res, err := pool.ExecContext(ctx, `UPDATE imports SET status = ? WHERE status = ?`, to, from)
	if err != nil {
		return 0, fmt.Errorf("set import status %s->%s: %w", from, to, err)
	}
	return res.RowsAffected()
}

// SetDeleteAfterForStatus sets delete_after on every row in a given status, so the
// admin's per-batch choice is recorded just before the rows are started.
func SetDeleteAfterForStatus(ctx context.Context, pool *sql.DB, status string, value bool) error {
	_, err := pool.ExecContext(ctx, `UPDATE imports SET delete_after = ? WHERE status = ?`, value, status)
	if err != nil {
		return fmt.Errorf("set delete_after for status %s: %w", status, err)
	}
	return nil
}

// DeleteImport removes a single import row.
func DeleteImport(ctx context.Context, pool *sql.DB, id int64) error {
	_, err := pool.ExecContext(ctx, `DELETE FROM imports WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete import %d: %w", id, err)
	}
	return nil
}

// ClearStagedImports removes every staged (preCheck) row, whatever category or source
// produced it. Starting a new upload/Plex/Jellyfin flow calls it first: only one batch is
// ever under review, so a batch left behind by an abandoned flow is replaced rather than
// silently mixed into the new one.
func ClearStagedImports(ctx context.Context, pool *sql.DB) error {
	_, err := pool.ExecContext(ctx, `DELETE FROM imports WHERE status = ?`, StatusPreCheck)
	if err != nil {
		return fmt.Errorf("clear staged imports: %w", err)
	}
	return nil
}

// ClearImportsAll removes every import row. Imports are transient state that cannot
// be rebuilt from the filesystem, so a full cache rebuild simply drops them.
func ClearImportsAll(ctx context.Context, pool *sql.DB) error {
	_, err := pool.ExecContext(ctx, `DELETE FROM imports`)
	if err != nil {
		return fmt.Errorf("clear all imports: %w", err)
	}
	return nil
}

// CountUnfinishedImports counts import rows queued or copying (status import/importing) -
// the import task backlog for the admin tasks overview.
func CountUnfinishedImports(ctx context.Context, pool *sql.DB) (int, error) {
	var n int
	err := pool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM imports WHERE status IN (?, ?)`, StatusImport, StatusImporting).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count unfinished imports: %w", err)
	}
	return n, nil
}

// CountUnfinishedImportsUnder counts rows whose source still lives under pathPrefix and
// that have not finished (preCheck/import/importing). Upload cleanup uses it to know when a
// /tmp working dir is safe to remove. The prefix should end in a path separator so it cannot
// match a sibling dir sharing a name prefix.
func CountUnfinishedImportsUnder(ctx context.Context, pool *sql.DB, pathPrefix string) (int, error) {
	var n int
	err := pool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM imports WHERE source_path LIKE ? AND status IN (?, ?, ?)`,
		pathPrefix+"%", StatusPreCheck, StatusImport, StatusImporting).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count unfinished imports under %q: %w", pathPrefix, err)
	}
	return n, nil
}
