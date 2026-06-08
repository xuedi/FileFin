package db

import (
	"context"
	"database/sql"
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
		return nil, err
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
		return nil, err
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
			return nil, err
		}
		ms.HasPoster = hasPoster != 0
		out = append(out, ms)
	}
	return out, rows.Err()
}

// GetMedia returns the media row for an id, or sql.ErrNoRows when absent.
func GetMedia(ctx context.Context, pool *sql.DB, id string) (Media, error) {
	var m Media
	err := pool.QueryRowContext(ctx,
		`SELECT id, category_id, path, year, title, description, plot, poster, enriched
         FROM media WHERE id = ?`, id).
		Scan(&m.ID, &m.CategoryID, &m.Path, &m.Year, &m.Title, &m.Description, &m.Plot, &m.Poster, &m.Enriched)
	return m, err
}

// MediaFiles returns a media item's files, ordered by idx.
func MediaFiles(ctx context.Context, pool *sql.DB, id string) ([]MediaFile, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT media_id, idx, path, name, season, episode, ext FROM media_files WHERE media_id = ? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaFile{}
	for rows.Next() {
		var f MediaFile
		if err := rows.Scan(&f.MediaID, &f.Idx, &f.Path, &f.Name, &f.Season, &f.Episode, &f.Ext); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// AllFiles returns every media file across the cache, for the optimizer planner to
// derive its candidate list.
func AllFiles(ctx context.Context, pool *sql.DB) ([]MediaFile, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT media_id, idx, path, name, season, episode, ext FROM media_files ORDER BY media_id, idx`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaFile{}
	for rows.Next() {
		var f MediaFile
		if err := rows.Scan(&f.MediaID, &f.Idx, &f.Path, &f.Name, &f.Season, &f.Episode, &f.Ext); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FolderPath returns the on-disk media folder for an item, for the live meta.json
// state read/write. It returns "" (no error) when the id is unknown.
func FolderPath(ctx context.Context, pool *sql.DB, id string) (string, error) {
	var p string
	err := pool.QueryRowContext(ctx, `SELECT path FROM media WHERE id = ?`, id).Scan(&p)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return p, err
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
		return "", err
	}
	if poster == "" {
		return "", nil
	}
	return filepath.Join(path, poster), nil
}

// FilePath returns the absolute path and lowercase extension of the n-th file of a media
// item. An unknown id/index yields "" (no error).
func FilePath(ctx context.Context, pool *sql.DB, id string, n int) (path, ext string, err error) {
	err = pool.QueryRowContext(ctx,
		`SELECT path, ext FROM media_files WHERE media_id = ? AND idx = ?`, id, n).Scan(&path, &ext)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return path, ext, err
}
