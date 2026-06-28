package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
)

// MediaSummary is a media entry in a listing. FolderPath is the on-disk media folder,
// used by the server to read the per-user state from meta.json live.
type MediaSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	HasPoster  bool   `json:"hasPoster"`
	Watched    bool   `json:"watched"`
	FolderPath string `json:"-"`
}

// ListMediaByCategory returns the media in a category (by id), ordered by year then
// title (chronological browse order).
func ListMediaByCategory(ctx context.Context, pool *sql.DB, categoryID int64) ([]MediaSummary, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT id, title, year, (poster <> ''), path FROM media WHERE category_id = ? ORDER BY year, title`,
		categoryID)
	if err != nil {
		return nil, fmt.Errorf("query media by category %d: %w", categoryID, err)
	}
	return scanSummaries(rows)
}

// ListMediaInCategorySubtree returns the media in a category and all its descendants,
// ordered by year then title. Categories nest, so a watch-list import scoped to a parent
// (e.g. "Anime") must reach items filed under its children; the exact-id ListMediaByCategory
// would miss them.
func ListMediaInCategorySubtree(ctx context.Context, pool *sql.DB, rootID int64) ([]MediaSummary, error) {
	rows, err := pool.QueryContext(ctx,
		`WITH RECURSIVE subtree(id) AS (
		    SELECT id FROM categories WHERE id = ?
		    UNION ALL
		    SELECT c.id FROM categories c JOIN subtree s ON c.parent_id = s.id
		)
		SELECT id, title, year, (poster <> ''), path FROM media
		WHERE category_id IN (SELECT id FROM subtree) ORDER BY year, title`,
		rootID)
	if err != nil {
		return nil, fmt.Errorf("query media in category subtree %d: %w", rootID, err)
	}
	return scanSummaries(rows)
}

// AllMedia returns every media item with its folder path, for cross-library views (the
// per-user home page) that filter by live state. The home rows re-sort by the per-user
// updated time, so this order only sets the tie-break; year then title keeps it
// consistent with the per-category browse order.
func AllMedia(ctx context.Context, pool *sql.DB) ([]MediaSummary, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT id, title, year, (poster <> ''), path FROM media ORDER BY year, title`)
	if err != nil {
		return nil, fmt.Errorf("query all media: %w", err)
	}
	return scanSummaries(rows)
}

func scanSummaries(rows *sql.Rows) ([]MediaSummary, error) {
	defer rows.Close()
	out := []MediaSummary{}
	for rows.Next() {
		var ms MediaSummary
		var hasPoster int
		if err := rows.Scan(&ms.ID, &ms.Title, &ms.Year, &hasPoster, &ms.FolderPath); err != nil {
			return nil, fmt.Errorf("scan media summary: %w", err)
		}
		ms.HasPoster = hasPoster != 0
		out = append(out, ms)
	}
	return out, rows.Err()
}

// AllMediaIDs returns every cached media id, for the discovery reconcile to diff the
// cache against the on-disk folder set.
func AllMediaIDs(ctx context.Context, pool *sql.DB) ([]string, error) {
	return queryRows(ctx, pool, `SELECT id FROM media`,
		func(r *sql.Rows) (string, error) {
			var id string
			return id, r.Scan(&id)
		})
}

// GetMedia returns the media row for an id, or sql.ErrNoRows when absent.
func GetMedia(ctx context.Context, pool *sql.DB, id string) (Media, error) {
	var m Media
	err := pool.QueryRowContext(ctx,
		`SELECT id, category_id, path, year, title, description, plot, poster, enriched, language, country, director, writer
         FROM media WHERE id = ?`, id).
		Scan(&m.ID, &m.CategoryID, &m.Path, &m.Year, &m.Title, &m.Description, &m.Plot, &m.Poster, &m.Enriched,
			&m.Language, &m.Country, &m.Director, &m.Writer)
	if err == sql.ErrNoRows {
		return Media{}, err
	}
	if err != nil {
		return Media{}, fmt.Errorf("get media %s: %w", id, err)
	}
	return m, nil
}

// fileColumns is the canonical media_files column order shared by every file query and
// the scanMediaFile scanner.
const fileColumns = `media_id, idx, path, name, season, episode, ext, container, video_codec, audio_codec`

// scanMediaFile scans one media_files row in the canonical column order.
func scanMediaFile(r *sql.Rows) (MediaFile, error) {
	var f MediaFile
	return f, r.Scan(&f.MediaID, &f.Idx, &f.Path, &f.Name, &f.Season, &f.Episode, &f.Ext,
		&f.Container, &f.VideoCodec, &f.AudioCodec)
}

// MediaFiles returns a media item's files, ordered by idx.
func MediaFiles(ctx context.Context, pool *sql.DB, id string) ([]MediaFile, error) {
	return queryRows(ctx, pool,
		`SELECT `+fileColumns+` FROM media_files WHERE media_id = ? ORDER BY idx`,
		scanMediaFile, id)
}

// AllFiles returns every media file across the cache, for the optimizer planner to
// derive its candidate list.
func AllFiles(ctx context.Context, pool *sql.DB) ([]MediaFile, error) {
	return queryRows(ctx, pool,
		`SELECT `+fileColumns+` FROM media_files ORDER BY media_id, idx`,
		scanMediaFile)
}

// MediaMissingFormat returns the ids of media items that have at least one file whose
// probed format columns are still empty, for the probe agent's queue refill.
func MediaMissingFormat(ctx context.Context, pool *sql.DB) ([]string, error) {
	return queryRows(ctx, pool,
		`SELECT DISTINCT media_id FROM media_files WHERE container = '' OR video_codec = '' ORDER BY media_id`,
		func(r *sql.Rows) (string, error) {
			var id string
			return id, r.Scan(&id)
		})
}

// FolderPath returns the on-disk media folder for an item, for the live meta.json
// state read/write. It returns "" (no error) when the id is unknown.
func FolderPath(ctx context.Context, pool *sql.DB, id string) (string, error) {
	var p string
	err := pool.QueryRowContext(ctx, `SELECT path FROM media WHERE id = ?`, id).Scan(&p)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query folder path %s: %w", id, err)
	}
	return p, nil
}

// PosterPath returns the absolute poster path for a media item, or "" when none (or the
// id is unknown). The poster column stores only the basename beside the folder path.
func PosterPath(ctx context.Context, pool *sql.DB, id string) (string, error) {
	var path, poster string
	err := pool.QueryRowContext(ctx, `SELECT path, poster FROM media WHERE id = ?`, id).Scan(&path, &poster)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query poster path %s: %w", id, err)
	}
	if poster == "" {
		return "", nil
	}
	return filepath.Join(path, poster), nil
}

// FileAt returns the n-th file of a media item with its probed format, for the playback
// serve decision. ok is false (no error) for an unknown id/index.
func FileAt(ctx context.Context, pool *sql.DB, id string, n int) (MediaFile, bool, error) {
	f := MediaFile{MediaID: id, Idx: n}
	err := pool.QueryRowContext(ctx,
		`SELECT path, name, season, episode, ext, container, video_codec, audio_codec
         FROM media_files WHERE media_id = ? AND idx = ?`, id, n).
		Scan(&f.Path, &f.Name, &f.Season, &f.Episode, &f.Ext, &f.Container, &f.VideoCodec, &f.AudioCodec)
	if err == sql.ErrNoRows {
		return MediaFile{}, false, nil
	}
	if err != nil {
		return MediaFile{}, false, fmt.Errorf("query file %s/%d: %w", id, n, err)
	}
	return f, true, nil
}
