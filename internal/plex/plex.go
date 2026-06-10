// Package plex reads a Plex library database (read-only) and turns it into a
// catalog of items that map onto FileFin's media-folder layout. It opens the
// database read-only and never writes to it or to the Plex media/metadata.
package plex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// metadata_type values used by Plex: 1 movie, 2 show, 4 episode.
const (
	typeMovie   = 1
	typeShow    = 2
	typeEpisode = 4
)

// SourceFile is one media file to copy, with its season/episode (0 for movies)
// and any external subtitle streams Plex recorded for it.
type SourceFile struct {
	Path      string
	Season    int
	Episode   int
	Subtitles []Subtitle
}

// Subtitle is one external subtitle stream from Plex's media_streams table. Path
// is the decoded local path (the file:// url stripped and percent-decoded);
// Language is Plex's tag (often empty); Codec is the format ("srt", "ass", ...).
type Subtitle struct {
	Path     string
	Language string
	Codec    string
}

// Item is one media folder's worth of data extracted from Plex.
type Item struct {
	Section       string // Plex section name -> FileFin category
	IsShow        bool
	Title         string
	Year          int
	Summary       string
	Release       string // YYYY-MM-DD when known
	Runtime       int    // minutes
	Rating        string
	ContentRating string
	Genres        []string
	Directors     []string
	Writers       []string
	Actors        []string
	PosterPath    string // resolved absolute path in the Plex metadata bundle, or ""
	Files         []SourceFile
}

// Section is one Plex library, used by the check step before any staging.
type Section struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"` // "movie" | "show"
	Count int    `json:"count"`
}

// DB is an open read-only handle to a Plex library database.
type DB struct {
	db          *sql.DB
	metadataDir string
}

// DeriveMetadataDir guesses the Plex "Metadata" directory from the database path
// (<plex-home>/Plug-in Support/Databases/library.db -> <plex-home>/Metadata).
func DeriveMetadataDir(dbPath string) string {
	home := filepath.Dir(filepath.Dir(filepath.Dir(dbPath)))
	return filepath.Join(home, "Metadata")
}

// Open opens the Plex database read-only. metadataDir is where poster bundles
// live; pass "" to skip artwork resolution.
func Open(dbPath, metadataDir string) (*DB, error) {
	// Build a proper file: URI so paths with spaces (e.g. "Plex Media Server")
	// are escaped. immutable=1 avoids -wal access on read-only mounts.
	uri := (&url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro&immutable=1"}).String()
	d, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("open plex db (uri): %w", err)
	}
	if uriErr := d.Ping(); uriErr != nil {
		// Fall back to a plain read-only open if the URI form is rejected.
		d.Close()
		d, err = sql.Open("sqlite", dbPath)
		if err != nil {
			return nil, fmt.Errorf("open plex db: %w", errors.Join(uriErr, err))
		}
		if pingErr := d.Ping(); pingErr != nil {
			d.Close()
			return nil, fmt.Errorf("ping plex db: %w", errors.Join(uriErr, pingErr))
		}
	}
	return &DB{db: d, metadataDir: metadataDir}, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// Sections returns the movie and show libraries with a cheap item count each, for
// the check step. The count is the number of top-level items (movies, or shows)
// the section holds.
func (d *DB) Sections(ctx context.Context) ([]Section, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT ls.name, ls.section_type,
		(SELECT COUNT(*) FROM metadata_items mi
		   WHERE mi.library_section_id = ls.id
		     AND mi.metadata_type IN (?, ?) AND mi.deleted_at IS NULL)
		FROM library_sections ls
		WHERE ls.section_type IN (?, ?)
		ORDER BY ls.name`, typeMovie, typeShow, typeMovie, typeShow)
	if err != nil {
		return nil, fmt.Errorf("query plex sections: %w", err)
	}
	defer rows.Close()
	var out []Section
	for rows.Next() {
		var name string
		var stype, count int
		if err := rows.Scan(&name, &stype, &count); err != nil {
			return nil, fmt.Errorf("scan plex section: %w", err)
		}
		kind := "movie"
		if stype == typeShow {
			kind = "show"
		}
		out = append(out, Section{Name: name, Kind: kind, Count: count})
	}
	return out, rows.Err()
}

// SampleFiles returns up to n media-file paths spread across the given sections
// (and, for shows, across different show folders), so the path resolver stats a
// representative set rather than many files from one directory. The spread is
// achieved by taking one file per top-level item and round-robining across
// sections.
func (d *DB) SampleFiles(ctx context.Context, sections []string, n int) ([]string, error) {
	if n <= 0 || len(sections) == 0 {
		return nil, nil
	}
	perSection := make(map[string][]string, len(sections))
	for _, sec := range sections {
		files, err := d.sectionSampleFiles(ctx, sec, n)
		if err != nil {
			return nil, err
		}
		perSection[sec] = files
	}
	// Round-robin across sections so the sample is spread, not drawn from one library.
	var out []string
	for i := 0; len(out) < n; i++ {
		progressed := false
		for _, sec := range sections {
			files := perSection[sec]
			if i < len(files) {
				out = append(out, files[i])
				progressed = true
				if len(out) >= n {
					return out, nil
				}
			}
		}
		if !progressed {
			break
		}
	}
	return out, nil
}

// sectionSampleFiles returns one representative file per top-level item in a
// section (one per movie, one per show), deduplicated by parent directory so the
// sample spreads across folders. It caps work with a generous LIMIT.
func (d *DB) sectionSampleFiles(ctx context.Context, section string, want int) ([]string, error) {
	const cap = 500
	// One file per movie, and one file per show (the first episode found), so a
	// show contributes a single folder probe rather than many sibling episodes.
	q := `SELECT mp.file FROM metadata_items mi
		JOIN library_sections ls ON ls.id = mi.library_section_id
		JOIN media_items m ON m.metadata_item_id = mi.id
		JOIN media_parts mp ON mp.media_item_id = m.id
		WHERE ls.name = ? AND mi.metadata_type = ? AND mi.deleted_at IS NULL AND mp.deleted_at IS NULL
		GROUP BY mi.id
		UNION
		SELECT mp.file FROM metadata_items show
		JOIN library_sections ls ON ls.id = show.library_section_id
		JOIN metadata_items season ON season.parent_id = show.id
		JOIN metadata_items ep ON ep.parent_id = season.id
		JOIN media_items m ON m.metadata_item_id = ep.id
		JOIN media_parts mp ON mp.media_item_id = m.id
		WHERE ls.name = ? AND show.metadata_type = ? AND ep.metadata_type = ?
		  AND show.deleted_at IS NULL AND ep.deleted_at IS NULL AND mp.deleted_at IS NULL
		GROUP BY show.id
		LIMIT ?`
	rows, err := d.db.QueryContext(ctx, q, section, typeMovie, section, typeShow, typeEpisode, cap)
	if err != nil {
		return nil, fmt.Errorf("query sample files %q: %w", section, err)
	}
	defer rows.Close()
	seenDir := map[string]bool{}
	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, fmt.Errorf("scan sample file: %w", err)
		}
		if strings.TrimSpace(f) == "" {
			continue
		}
		dir := filepath.Dir(f)
		if seenDir[dir] {
			continue
		}
		seenDir[dir] = true
		out = append(out, f)
		if len(out) >= want {
			break
		}
	}
	return out, rows.Err()
}

// Items returns all movies and shows, optionally limited to one section by name.
func (d *DB) Items(ctx context.Context, section string) ([]Item, error) {
	movies, err := d.query(ctx, false, section)
	if err != nil {
		return nil, err
	}
	shows, err := d.query(ctx, true, section)
	if err != nil {
		return nil, err
	}
	return append(movies, shows...), nil
}

const itemCols = `mi.id, ls.name, COALESCE(mi.title,''), mi.year,
	COALESCE(mi.summary,''), COALESCE(CAST(mi.originally_available_at AS TEXT),''),
	COALESCE(mi.duration,0), mi.rating, COALESCE(mi.content_rating,''),
	COALESCE(mi.tags_genre,''), COALESCE(mi.tags_director,''),
	COALESCE(mi.tags_writer,''), COALESCE(mi.tags_star,''),
	COALESCE(mi.hash,''), COALESCE(mi.user_thumb_url,'')`

func (d *DB) query(ctx context.Context, shows bool, section string) ([]Item, error) {
	mtype, typeDir := typeMovie, "Movies"
	if shows {
		mtype, typeDir = typeShow, "TV Shows"
	}
	q := `SELECT ` + itemCols + `
		FROM metadata_items mi JOIN library_sections ls ON ls.id = mi.library_section_id
		WHERE mi.metadata_type = ? AND mi.deleted_at IS NULL`
	args := []any{mtype}
	if section != "" {
		q += ` AND ls.name = ?`
		args = append(args, section)
	}
	q += ` ORDER BY ls.name, mi.title`

	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query plex items: %w", err)
	}
	defer rows.Close()

	var out []Item
	type raw struct {
		id   int64
		hash string
	}
	var raws []raw
	for rows.Next() {
		var (
			id                            int64
			section, title                string
			year                          sql.NullInt64
			summary, oad                  string
			duration                      int64
			rating                        sql.NullFloat64
			contentRating                 string
			genre, director, writer, star string
			hash, thumb                   string
		)
		if err := rows.Scan(&id, &section, &title, &year, &summary, &oad, &duration, &rating,
			&contentRating, &genre, &director, &writer, &star, &hash, &thumb); err != nil {
			return nil, fmt.Errorf("scan plex item: %w", err)
		}
		it := Item{
			Section:       section,
			IsShow:        shows,
			Title:         title,
			Year:          int(year.Int64),
			Summary:       strings.TrimSpace(summary),
			Release:       toDate(oad),
			Runtime:       int(duration / 60000),
			ContentRating: contentRating,
			Genres:        splitTags(genre),
			Directors:     splitTags(director),
			Writers:       splitTags(writer),
			Actors:        splitTags(star),
			PosterPath:    d.artwork(typeDir, hash, thumb),
		}
		if rating.Valid {
			it.Rating = strconv.FormatFloat(rating.Float64, 'f', 1, 64)
		}
		out = append(out, it)
		raws = append(raws, raw{id: id, hash: hash})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		files, err := d.files(ctx, raws[i].id, shows)
		if err != nil {
			return nil, err
		}
		out[i].Files = files
	}
	return out, nil
}

func (d *DB) files(ctx context.Context, itemID int64, shows bool) ([]SourceFile, error) {
	var q string
	if shows {
		q = `SELECT COALESCE(season."index",0), COALESCE(ep."index",0), mp.file, mp.id
			FROM metadata_items ep
			JOIN metadata_items season ON season.id = ep.parent_id
			JOIN metadata_items show ON show.id = season.parent_id
			JOIN media_items m ON m.metadata_item_id = ep.id
			JOIN media_parts mp ON mp.media_item_id = m.id
			WHERE show.id = ? AND ep.metadata_type = 4 AND ep.deleted_at IS NULL AND mp.deleted_at IS NULL
			ORDER BY season."index", ep."index", mp.file`
	} else {
		q = `SELECT 0, 0, mp.file, mp.id
			FROM media_items m JOIN media_parts mp ON mp.media_item_id = m.id
			WHERE m.metadata_item_id = ? AND mp.deleted_at IS NULL
			ORDER BY mp."index", mp.file`
	}
	rows, err := d.db.QueryContext(ctx, q, itemID)
	if err != nil {
		return nil, fmt.Errorf("query plex files %d: %w", itemID, err)
	}
	defer rows.Close()
	var files []SourceFile
	var partIDs []int64
	for rows.Next() {
		var f SourceFile
		var partID int64
		if err := rows.Scan(&f.Season, &f.Episode, &f.Path, &partID); err != nil {
			return nil, fmt.Errorf("scan plex file: %w", err)
		}
		if strings.TrimSpace(f.Path) != "" {
			files = append(files, f)
			partIDs = append(partIDs, partID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range files {
		subs, err := d.subtitles(ctx, partIDs[i])
		if err != nil {
			return nil, err
		}
		files[i].Subtitles = subs
	}
	return files, nil
}

// subtitles returns the external subtitle streams (those carrying a file url) for
// one media part, with the url decoded to a local path.
func (d *DB) subtitles(ctx context.Context, partID int64) ([]Subtitle, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT COALESCE(url,''), COALESCE(language,''), COALESCE(codec,'')
		FROM media_streams
		WHERE stream_type_id = 3 AND url <> '' AND media_part_id = ?`, partID)
	if err != nil {
		return nil, fmt.Errorf("query plex subtitles %d: %w", partID, err)
	}
	defer rows.Close()
	var out []Subtitle
	for rows.Next() {
		var rawURL, lang, codec string
		if err := rows.Scan(&rawURL, &lang, &codec); err != nil {
			return nil, fmt.Errorf("scan plex subtitle: %w", err)
		}
		path := decodeFileURL(rawURL)
		if path == "" {
			continue
		}
		out = append(out, Subtitle{Path: path, Language: strings.TrimSpace(lang), Codec: strings.TrimSpace(codec)})
	}
	return out, rows.Err()
}

// decodeFileURL turns a Plex external-subtitle url ("file:///mnt/...%20...") into a
// local filesystem path. A non-file url or an undecodable one yields "".
func decodeFileURL(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(s, "file://") {
		return ""
	}
	s = strings.TrimPrefix(s, "file://")
	if dec, err := url.PathUnescape(s); err == nil {
		return dec
	}
	return s
}

// artwork resolves a "metadata://" thumb/art URL to a file path inside the Plex
// metadata bundle, or "" if it cannot be resolved or read.
func (d *DB) artwork(typeDir, hash, url string) string {
	p := artworkPath(d.metadataDir, typeDir, hash, url)
	if p == "" {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func artworkPath(metadataDir, typeDir, hash, url string) string {
	if metadataDir == "" || hash == "" {
		return ""
	}
	bundle := filepath.Join(metadataDir, typeDir, hash[:1], hash[1:]+".bundle")
	switch {
	case strings.HasPrefix(url, "metadata://"):
		// Agent-combined artwork: Contents/_combined/<posters|art>/<name>
		return filepath.Join(bundle, "Contents", "_combined", strings.TrimPrefix(url, "metadata://"))
	case strings.HasPrefix(url, "upload://"):
		// User-uploaded artwork: Uploads/<posters|art>/<name>
		return filepath.Join(bundle, "Uploads", strings.TrimPrefix(url, "upload://"))
	default:
		return ""
	}
}

func splitTags(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "|") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// toDate normalizes Plex's originally_available_at (a unix epoch in this schema,
// occasionally a date string) into YYYY-MM-DD.
func toDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(n, 0).UTC().Format("2006-01-02")
	}
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
