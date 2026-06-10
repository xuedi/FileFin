package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Media is one row of the media cache: a media folder written by the importer. The
// importer is its sole writer; a filesystem -> media rebuild (a scanner) comes later.
type Media struct {
	ID          string
	CategoryID  int64
	Path        string
	Year        int
	Title       string
	Description string
	Plot        string
	Poster      string
	Enriched    bool
}

// MediaFile is one file belonging to a media row (season/episode 0 for a movie).
// Container/VideoCodec/AudioCodec are the ffprobe-derived true format, set at import and
// refreshed by the probe agent; they are empty for a row not yet probed (e.g. after a
// cache rebuild), and the playback/optimize decisions fall back to Ext until then.
type MediaFile struct {
	MediaID    string
	Idx        int
	Path       string
	Name       string
	Season     int
	Episode    int
	Ext        string
	Container  string
	VideoCodec string
	AudioCodec string
}

// InsertMedia inserts (or replaces) a media row. REPLACE keeps a reimport of the
// same folder idempotent rather than erroring on the primary key.
func InsertMedia(ctx context.Context, pool *sql.DB, m Media) error {
	_, err := pool.ExecContext(ctx,
		`INSERT OR REPLACE INTO media
            (id, category_id, path, year, title, description, plot, poster, enriched)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.CategoryID, m.Path, m.Year, m.Title, m.Description, m.Plot, m.Poster, m.Enriched)
	if err != nil {
		return fmt.Errorf("insert media %s: %w", m.ID, err)
	}
	return nil
}

// SetMediaEnriched records the agent's enrichment of a folder into the cache row:
// the refreshed description/plot/poster and the enriched flag.
func SetMediaEnriched(ctx context.Context, pool *sql.DB, id, description, plot, poster string) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE media SET description = ?, plot = ?, poster = ?, enriched = 1 WHERE id = ?`,
		description, plot, poster, id)
	if err != nil {
		return fmt.Errorf("set media enriched %s: %w", id, err)
	}
	return nil
}

// UnenrichedMedia returns the media rows still carrying stub metadata (enriched = 0),
// with the fields the enrichment agent needs to look a title up and write it back. The
// owning category id is included so the scan can skip other-media categories.
func UnenrichedMedia(ctx context.Context, pool *sql.DB) ([]Media, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT id, category_id, path, year, title FROM media WHERE enriched = 0 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query unenriched media: %w", err)
	}
	defer rows.Close()
	out := []Media{}
	for rows.Next() {
		var m Media
		if err := rows.Scan(&m.ID, &m.CategoryID, &m.Path, &m.Year, &m.Title); err != nil {
			return nil, fmt.Errorf("scan media: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CategoryOtherMedia reports whether a category's (effective) other-media flag is set,
// for the enricher's per-task guard. An unknown id reads as false.
func CategoryOtherMedia(ctx context.Context, pool *sql.DB, id int64) (bool, error) {
	var other bool
	err := pool.QueryRowContext(ctx, `SELECT other_media FROM categories WHERE id = ?`, id).Scan(&other)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query category other_media %d: %w", id, err)
	}
	return other, nil
}

// InsertMediaFile inserts one file row for a media item. The probed format columns ride
// along from the struct (set by the importer after probing); a rebuild/reconcile leaves
// them empty and the probe agent backfills them later.
func InsertMediaFile(ctx context.Context, pool *sql.DB, f MediaFile) error {
	_, err := pool.ExecContext(ctx,
		`INSERT INTO media_files (media_id, idx, path, name, season, episode, ext, container, video_codec, audio_codec)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.MediaID, f.Idx, f.Path, f.Name, f.Season, f.Episode, f.Ext, f.Container, f.VideoCodec, f.AudioCodec)
	if err != nil {
		return fmt.Errorf("insert media file %s/%d: %w", f.MediaID, f.Idx, err)
	}
	return nil
}

// SetMediaFileFormat records a file's probed true format (container + video/audio codec)
// onto its cache row, used by the probe agent to backfill/refresh a row whose columns
// were empty or stale.
func SetMediaFileFormat(ctx context.Context, pool *sql.DB, mediaID string, idx int, container, vCodec, aCodec string) error {
	_, err := pool.ExecContext(ctx,
		`UPDATE media_files SET container = ?, video_codec = ?, audio_codec = ? WHERE media_id = ? AND idx = ?`,
		container, vCodec, aCodec, mediaID, idx)
	if err != nil {
		return fmt.Errorf("set media file format %s/%d: %w", mediaID, idx, err)
	}
	return nil
}

// CountMediaFiles returns how many file rows a media item already has, so the
// importer can pick the next unique idx when adding another episode/file to an
// existing folder.
func CountMediaFiles(ctx context.Context, pool *sql.DB, mediaID string) (int, error) {
	var n int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_files WHERE media_id = ?`, mediaID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count media files %s: %w", mediaID, err)
	}
	return n, nil
}

// CountMedia returns the total number of media items in the cache (the dashboard's
// library tally).
func CountMedia(ctx context.Context, pool *sql.DB) (int, error) {
	var n int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count media: %w", err)
	}
	return n, nil
}

// CountFiles returns the total number of media files across all items (the dashboard's
// file tally).
func CountFiles(ctx context.Context, pool *sql.DB) (int, error) {
	var n int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_files`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count media files: %w", err)
	}
	return n, nil
}

// DeleteMedia removes one media item and its file rows from the cache, used by the
// discovery reconcile when a folder has vanished from disk. The caller also prunes the
// item's health row and any pending/error queue tasks.
func DeleteMedia(ctx context.Context, pool *sql.DB, id string) error {
	if _, err := pool.ExecContext(ctx, `DELETE FROM media_files WHERE media_id = ?`, id); err != nil {
		return fmt.Errorf("delete media files %s: %w", id, err)
	}
	if _, err := pool.ExecContext(ctx, `DELETE FROM media WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete media %s: %w", id, err)
	}
	return nil
}

// ReplaceMediaFiles swaps a media item's file rows for a fresh set, used by the discovery
// reconcile when a folder's files changed on disk.
func ReplaceMediaFiles(ctx context.Context, pool *sql.DB, mediaID string, files []MediaFile) error {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace media files %s: %w", mediaID, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM media_files WHERE media_id = ?`, mediaID); err != nil {
		return fmt.Errorf("clear media files %s: %w", mediaID, err)
	}
	for _, f := range files {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO media_files (media_id, idx, path, name, season, episode, ext, container, video_codec, audio_codec)
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.MediaID, f.Idx, f.Path, f.Name, f.Season, f.Episode, f.Ext, f.Container, f.VideoCodec, f.AudioCodec); err != nil {
			return fmt.Errorf("insert media file %s/%d: %w", f.MediaID, f.Idx, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace media files %s: %w", mediaID, err)
	}
	return nil
}

// ClearMedia empties the media cache (both tables), for a full rebuild from disk.
func ClearMedia(ctx context.Context, pool *sql.DB) error {
	if _, err := pool.ExecContext(ctx, `DELETE FROM media_files`); err != nil {
		return fmt.Errorf("clear media files: %w", err)
	}
	if _, err := pool.ExecContext(ctx, `DELETE FROM media`); err != nil {
		return fmt.Errorf("clear media: %w", err)
	}
	return nil
}
