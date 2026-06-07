package cache

import (
	"path/filepath"

	"filefin/internal/subtitle"
	"filefin/internal/transcode"
)

// API DTOs returned by the read queries. JSON tags define the API shape.

// CategorySummary is one entry in the category list.
type CategorySummary struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// MediaSummary is a media entry in a category listing. Watched is per-user and filled
// live from state.md by the server; FolderPath is the on-disk folder for that read.
type MediaSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	HasPoster  bool   `json:"hasPoster"`
	Watched    bool   `json:"watched"`
	FolderPath string `json:"-"`
}

// SubtitleInfo describes one external subtitle track of a media file. The on-disk
// path is never exposed; the client fetches the track by its Index.
type SubtitleInfo struct {
	Index int    `json:"index"`
	Lang  string `json:"lang"`
	Label string `json:"label"`
}

// FileInfo describes one playable file of a media item.
type FileInfo struct {
	Index     int            `json:"index"`
	Name      string         `json:"name"`
	Season    int            `json:"season"`
	Episode   int            `json:"episode"`
	Transcode bool           `json:"transcode"` // true if the browser cannot direct-play it
	Watched   bool           `json:"watched"`   // per-user, filled live from state.md by the server
	Subtitles []SubtitleInfo `json:"subtitles"`
}

// Pair is an ordered metadata key/value.
type Pair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// MediaDetail is the full detail view of a media item. The watch fields (Watched,
// ContinueIndex, ContinueSeconds, and each FileInfo.Watched) are per-user and filled
// live from state.md by the server, not the cache.
type MediaDetail struct {
	ID              string     `json:"id"`
	Category        string     `json:"category"`
	Title           string     `json:"title"`
	Year            int        `json:"year"`
	Description     string     `json:"description"`
	Plot            string     `json:"plot"`
	HasPoster       bool       `json:"hasPoster"`
	Files           []FileInfo `json:"files"`
	Metadata        []Pair     `json:"metadata"`
	Ratings         []Pair     `json:"ratings"`
	Technical       []Pair     `json:"technical"`
	Actors          []string   `json:"actors"`
	Tags            []string   `json:"tags"`
	FolderPath      string     `json:"-"` // on-disk media folder, for the live state.md read/write
	Watched         bool       `json:"watched"`
	Favorite        bool       `json:"favorite"`
	ContinueIndex   int        `json:"continueIndex"`
	ContinueSeconds int        `json:"continueSeconds"`
}

// Stats are library-wide totals for the admin dashboard.
type Stats struct {
	Categories int `json:"categories"`
	Media      int `json:"media"`
	Files      int `json:"files"`
}

// Stats returns library-wide counts.
func (s *Store) Stats() (Stats, error) {
	var st Stats
	row := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM categories),
		(SELECT COUNT(*) FROM media),
		(SELECT COUNT(*) FROM media_files)`)
	if err := row.Scan(&st.Categories, &st.Media, &st.Files); err != nil {
		return Stats{}, err
	}
	return st, nil
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
		`SELECT id, title, year, (poster <> ''), path FROM media WHERE category = ? ORDER BY year, title`,
		category)
	if err != nil {
		return nil, err
	}
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

// AllMedia returns every media item with its folder path, for cross-library views
// (e.g. the per-user "continue watching" home page) that filter by live state.
func (s *Store) AllMedia() ([]MediaSummary, error) {
	rows, err := s.db.Query(`SELECT id, title, year, (poster <> ''), path FROM media ORDER BY year, title`)
	if err != nil {
		return nil, err
	}
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

// MediaDetail returns the full detail for one media item.
func (s *Store) MediaDetail(id string) (*MediaDetail, error) {
	d := &MediaDetail{ID: id, Files: []FileInfo{}, Metadata: []Pair{}, Ratings: []Pair{}, Technical: []Pair{}, Actors: []string{}, Tags: []string{}}
	var poster string
	err := s.db.QueryRow(
		`SELECT category, title, year, description, plot, poster, path FROM media WHERE id = ?`, id).
		Scan(&d.Category, &d.Title, &d.Year, &d.Description, &d.Plot, &poster, &d.FolderPath)
	if err != nil {
		return nil, err
	}
	d.HasPoster = poster != ""

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
		if f.Transcode {
			// A fresh optimized copy makes the file direct-play, so the client skips HLS.
			if _, fresh := transcode.OptimizedSibling(path); fresh {
				f.Transcode = false
			}
		}
		f.Subtitles = []SubtitleInfo{}
		d.Files = append(d.Files, f)
	}
	if err := files.Err(); err != nil {
		return nil, err
	}

	srows, err := s.db.Query(`SELECT file_idx, idx, lang FROM subtitles WHERE media_id = ? ORDER BY file_idx, idx`, id)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		var fileIdx, subIdx int
		var lang string
		if err := srows.Scan(&fileIdx, &subIdx, &lang); err != nil {
			return nil, err
		}
		for i := range d.Files {
			if d.Files[i].Index == fileIdx {
				d.Files[i].Subtitles = append(d.Files[i].Subtitles, SubtitleInfo{Index: subIdx, Lang: lang, Label: subtitle.Label(lang)})
				break
			}
		}
	}
	if err := srows.Err(); err != nil {
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
		case "ratings":
			d.Ratings = append(d.Ratings, Pair{Key: k, Value: v})
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

// FilePath returns the absolute path of the source media file n (by index). HLS
// transcoding always works from the source, never the optimized copy.
func (s *Store) FilePath(id string, idx int) (string, error) {
	var p string
	err := s.db.QueryRow(`SELECT path FROM media_files WHERE media_id = ? AND idx = ?`, id, idx).Scan(&p)
	return p, err
}

// SubtitlePath returns the on-disk path of subtitle subIdx of media file fileIdx.
func (s *Store) SubtitlePath(id string, fileIdx, subIdx int) (string, error) {
	var p string
	err := s.db.QueryRow(
		`SELECT path FROM subtitles WHERE media_id = ? AND file_idx = ? AND idx = ?`, id, fileIdx, subIdx).Scan(&p)
	return p, err
}

// PlaybackPath returns the best file to serve for media file idx and whether it still
// needs transcoding. A fresh optimized copy beside the source is preferred (direct-play);
// otherwise the source, which needs transcoding when the browser cannot play it directly.
func (s *Store) PlaybackPath(id string, idx int) (path string, needsTranscode bool, err error) {
	src, err := s.FilePath(id, idx)
	if err != nil {
		return "", false, err
	}
	if opt, fresh := transcode.OptimizedSibling(src); fresh {
		return opt, false, nil
	}
	return src, transcode.NeedsTranscode(filepath.Ext(src)), nil
}

// Title returns a media item's title, or "" if unknown.
func (s *Store) Title(id string) (string, error) {
	var t string
	err := s.db.QueryRow(`SELECT title FROM media WHERE id = ?`, id).Scan(&t)
	return t, err
}

// FolderPath returns the on-disk media folder for an item, used to read/write its
// per-user state.md sidecar.
func (s *Store) FolderPath(id string) (string, error) {
	var p string
	err := s.db.QueryRow(`SELECT path FROM media WHERE id = ?`, id).Scan(&p)
	return p, err
}

// PosterPath returns the poster path for a media item, or "" if none.
func (s *Store) PosterPath(id string) (string, error) {
	var p string
	err := s.db.QueryRow(`SELECT poster FROM media WHERE id = ?`, id).Scan(&p)
	return p, err
}
