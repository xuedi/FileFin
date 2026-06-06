package cache

import (
	"path/filepath"

	"filefin/internal/transcode"
)

// API DTOs returned by the read queries. JSON tags define the API shape.

// CategorySummary is one entry in the category list.
type CategorySummary struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// MediaSummary is a media entry in a category listing.
type MediaSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	HasPoster bool   `json:"hasPoster"`
}

// FileInfo describes one playable file of a media item.
type FileInfo struct {
	Index     int    `json:"index"`
	Name      string `json:"name"`
	Season    int    `json:"season"`
	Episode   int    `json:"episode"`
	Transcode bool   `json:"transcode"` // true if the browser cannot direct-play it
}

// Pair is an ordered metadata key/value.
type Pair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// MediaDetail is the full detail view of a media item.
type MediaDetail struct {
	ID          string     `json:"id"`
	Category    string     `json:"category"`
	Title       string     `json:"title"`
	Year        int        `json:"year"`
	Description string     `json:"description"`
	Plot        string     `json:"plot"`
	HasPoster   bool       `json:"hasPoster"`
	HasBanner   bool       `json:"hasBanner"`
	Files       []FileInfo `json:"files"`
	Metadata    []Pair     `json:"metadata"`
	Technical   []Pair     `json:"technical"`
	Actors      []string   `json:"actors"`
	Tags        []string   `json:"tags"`
}

// Categories returns all categories with their media counts.
func (s *Store) Categories() ([]CategorySummary, error) {
	rows, err := s.db.Query(`
		SELECT c.name, COUNT(m.id)
		FROM categories c LEFT JOIN media m ON m.category = c.name
		GROUP BY c.name ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CategorySummary{}
	for rows.Next() {
		var cs CategorySummary
		if err := rows.Scan(&cs.Name, &cs.Count); err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// MediaByCategory returns the media in a category, ordered by year then title.
func (s *Store) MediaByCategory(category string) ([]MediaSummary, error) {
	rows, err := s.db.Query(
		`SELECT id, title, year, (poster <> '') FROM media WHERE category = ? ORDER BY year, title`,
		category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaSummary{}
	for rows.Next() {
		var ms MediaSummary
		var hasPoster int
		if err := rows.Scan(&ms.ID, &ms.Title, &ms.Year, &hasPoster); err != nil {
			return nil, err
		}
		ms.HasPoster = hasPoster != 0
		out = append(out, ms)
	}
	return out, rows.Err()
}

// MediaDetail returns the full detail for one media item.
func (s *Store) MediaDetail(id string) (*MediaDetail, error) {
	d := &MediaDetail{ID: id, Files: []FileInfo{}, Metadata: []Pair{}, Technical: []Pair{}, Actors: []string{}, Tags: []string{}}
	var poster, banner string
	err := s.db.QueryRow(
		`SELECT category, title, year, description, plot, poster, banner FROM media WHERE id = ?`, id).
		Scan(&d.Category, &d.Title, &d.Year, &d.Description, &d.Plot, &poster, &banner)
	if err != nil {
		return nil, err
	}
	d.HasPoster = poster != ""
	d.HasBanner = banner != ""

	files, err := s.db.Query(`SELECT idx, name, season, episode, path FROM media_files WHERE media_id = ? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	for files.Next() {
		var f FileInfo
		var path string
		if err := files.Scan(&f.Index, &f.Name, &f.Season, &f.Episode, &path); err != nil {
			return nil, err
		}
		f.Transcode = transcode.NeedsTranscode(filepath.Ext(path))
		d.Files = append(d.Files, f)
	}
	if err := files.Err(); err != nil {
		return nil, err
	}

	mrows, err := s.db.Query(`SELECT section, k, v FROM media_meta WHERE media_id = ? ORDER BY section, ord`, id)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var section, k, v string
		if err := mrows.Scan(&section, &k, &v); err != nil {
			return nil, err
		}
		switch section {
		case "metadata":
			d.Metadata = append(d.Metadata, Pair{Key: k, Value: v})
		case "technical":
			d.Technical = append(d.Technical, Pair{Key: k, Value: v})
		case "actor":
			d.Actors = append(d.Actors, k)
		}
	}
	if err := mrows.Err(); err != nil {
		return nil, err
	}

	trows, err := s.db.Query(`SELECT tag FROM tags WHERE media_id = ? ORDER BY tag`, id)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var t string
		if err := trows.Scan(&t); err != nil {
			return nil, err
		}
		d.Tags = append(d.Tags, t)
	}
	return d, trows.Err()
}

// FilePath returns the absolute path of media file n (by index).
func (s *Store) FilePath(id string, idx int) (string, error) {
	var p string
	err := s.db.QueryRow(`SELECT path FROM media_files WHERE media_id = ? AND idx = ?`, id, idx).Scan(&p)
	return p, err
}

// PosterPath returns the poster path for a media item, or "" if none.
func (s *Store) PosterPath(id string) (string, error) {
	var p string
	err := s.db.QueryRow(`SELECT poster FROM media WHERE id = ?`, id).Scan(&p)
	return p, err
}

// BannerPath returns the banner path for a media item, or "" if none.
func (s *Store) BannerPath(id string) (string, error) {
	var p string
	err := s.db.QueryRow(`SELECT banner FROM media WHERE id = ?`, id).Scan(&p)
	return p, err
}
