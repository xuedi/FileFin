// Package plex reads a Plex library database (read-only) and turns it into a
// catalog of items that map onto FileFin's "(YYYY) Title/" layout. It opens the
// database read-only and never writes to it or to the Plex media/metadata.
package plex

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SourceFile is one media file to copy, with its season/episode (0 for movies).
type SourceFile struct {
	Path    string
	Season  int
	Episode int
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
		return nil, err
	}
	if err := d.Ping(); err != nil {
		// Fall back to a plain read-only open if the URI form is rejected.
		d.Close()
		if d, err = sql.Open("sqlite", dbPath); err != nil {
			return nil, err
		}
		if err := d.Ping(); err != nil {
			d.Close()
			return nil, err
		}
	}
	return &DB{db: d, metadataDir: metadataDir}, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// Items returns all movies and shows, optionally limited to one section by name.
func (d *DB) Items(section string) ([]Item, error) {
	movies, err := d.query(false, section)
	if err != nil {
		return nil, err
	}
	shows, err := d.query(true, section)
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

func (d *DB) query(shows bool, section string) ([]Item, error) {
	mtype, typeDir := 1, "Movies"
	if shows {
		mtype, typeDir = 2, "TV Shows"
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

	rows, err := d.db.Query(q, args...)
	if err != nil {
		return nil, err
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
			return nil, err
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
		files, err := d.files(raws[i].id, shows)
		if err != nil {
			return nil, err
		}
		out[i].Files = files
	}
	return out, nil
}

func (d *DB) files(itemID int64, shows bool) ([]SourceFile, error) {
	var q string
	if shows {
		q = `SELECT COALESCE(season."index",0), COALESCE(ep."index",0), mp.file
			FROM metadata_items ep
			JOIN metadata_items season ON season.id = ep.parent_id
			JOIN metadata_items show ON show.id = season.parent_id
			JOIN media_items m ON m.metadata_item_id = ep.id
			JOIN media_parts mp ON mp.media_item_id = m.id
			WHERE show.id = ? AND ep.metadata_type = 4 AND ep.deleted_at IS NULL AND mp.deleted_at IS NULL
			ORDER BY season."index", ep."index", mp.file`
	} else {
		q = `SELECT 0, 0, mp.file
			FROM media_items m JOIN media_parts mp ON mp.media_item_id = m.id
			WHERE m.metadata_item_id = ? AND mp.deleted_at IS NULL
			ORDER BY mp."index", mp.file`
	}
	rows, err := d.db.Query(q, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []SourceFile
	for rows.Next() {
		var f SourceFile
		if err := rows.Scan(&f.Season, &f.Episode, &f.Path); err != nil {
			return nil, err
		}
		if strings.TrimSpace(f.Path) != "" {
			files = append(files, f)
		}
	}
	return files, rows.Err()
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
