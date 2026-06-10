// Package jellyfin reads a Jellyfin/Kodi media library that uses the NFO metadata
// format: per-item .nfo XML files plus poster/fanart image sidecars. This is the
// documented, version-stable on-disk format Jellyfin reads and writes
// (https://jellyfin.org/docs/general/server/metadata/nfo/). It only reads the library
// tree and never modifies it.
//
// The scanner is source-neutral, mirroring the Plex source: it returns Items that the
// import front stage turns into preCheck rows (importer.MetaFromJellyfin shapes the
// metadata, the server attaches sidecar subtitles), so this package depends on nothing
// in the importer and carries no import cycle.
package jellyfin

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"filefin/internal/recognize"
)

var videoExts = map[string]bool{
	".mp4": true, ".webm": true, ".mkv": true, ".avi": true,
	".mov": true, ".m4v": true, ".ts": true, ".m2ts": true, ".wmv": true,
}

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".bmp": true, ".tbn": true,
}

// Item is one movie or show discovered in the library. Files are its video files (one
// for a movie, many for a show or an in-folder multi-part movie); Season/Episode are
// set on show files. PosterPath is the absolute path of the chosen poster image, ""
// when none was found.
type Item struct {
	Title      string
	Year       int
	IsShow     bool
	PosterPath string
	Files      []File
	Details    Details
}

// File is one video file of an Item, with the season/episode it was recognised as
// (both 0 for a movie).
type File struct {
	Path    string
	Season  int
	Episode int
}

// Details is the source-neutral metadata extracted from an NFO, shaped into a Meta by
// importer.MetaFromJellyfin.
type Details struct {
	Description   string
	Release       string // the NFO "premiered" date, possibly empty
	Runtime       int
	Directors     []string
	Writers       []string
	Rating        string
	ContentRating string
	Studio        string
	UniqueIDs     []UniqueID
	Actors        []string // each "Name" or "Name (Role)"
	Genres        []string
}

// UniqueID is one external id from an NFO (imdb, tmdb, ...).
type UniqueID struct {
	Type  string
	Value string
}

// Scan walks a Jellyfin library directory and returns one Item per movie or show. A
// read error on the root is returned; per-entry read errors are skipped. The scan honours
// ctx cancellation, returning ctx.Err() between top-level entries.
func Scan(ctx context.Context, root string) ([]Item, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read jellyfin library %s: %w", root, err)
	}
	var out []Item
	var loose []string
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			if m, ok := scanDir(filepath.Join(root, e.Name())); ok {
				out = append(out, m)
			}
			continue
		}
		if videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			loose = append(loose, e.Name())
		}
	}
	return append(out, scanLooseMovies(root, loose)...), nil
}

// scanDir reads dir once and classifies it as a show (a season subfolder or a
// tvshow.nfo) or a movie folder, handing the already-read entries to the matching scanner
// so the directory is not read again.
func scanDir(dir string) (Item, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Item{}, false
	}
	if isShowDir(entries) {
		return scanShow(dir, entries)
	}
	return scanMovieDir(dir, entries)
}

func isShowDir(entries []os.DirEntry) bool {
	for _, e := range entries {
		if e.IsDir() && recognize.IsSeasonDir(e.Name()) {
			return true
		}
		if !e.IsDir() && strings.EqualFold(e.Name(), "tvshow.nfo") {
			return true
		}
	}
	return false
}

func scanMovieDir(dir string, entries []os.DirEntry) (Item, bool) {
	var videos []string
	for _, e := range entries {
		if !e.IsDir() && videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			videos = append(videos, e.Name())
		}
	}
	if len(videos) == 0 {
		return Item{}, false
	}
	folderTitle, folderYear := folderTitleYear(filepath.Base(dir))

	// NFO: prefer movie.nfo, else <video>.nfo for the first video.
	var nfo movieNFO
	if !readNFO(filepath.Join(dir, "movie.nfo"), &nfo) {
		readNFO(filepath.Join(dir, stripExt(videos[0])+".nfo"), &nfo)
	}

	title, year := resolveTitleYear(nfo.Title, nfo.Year, nfo.Premiered, folderTitle, folderYear)
	m := Item{
		Title:      title,
		Year:       year,
		Details:    toDetails(nfo.detailsNFO),
		PosterPath: findImage(entries, dir, []string{"poster", "folder", "cover", "default"}, []string{"-poster"}),
	}
	for _, v := range videos {
		m.Files = append(m.Files, File{Path: filepath.Join(dir, v)})
	}
	return m, true
}

// scanLooseMovies turns each loose video file directly under root into its own movie
// Item. Multi-disc grouping of loose files (the old DetectPart behaviour) is not ported
// yet, since the new recognise package has no part detector; foldered multi-part movies
// are still grouped by scanMovieDir.
func scanLooseMovies(dir string, videos []string) []Item {
	var out []Item
	for _, v := range videos {
		title, year := folderTitleYear(stripExt(v))
		var nfo movieNFO
		readNFO(filepath.Join(dir, stripExt(v)+".nfo"), &nfo)
		title, year = resolveTitleYear(nfo.Title, nfo.Year, nfo.Premiered, title, year)
		out = append(out, Item{
			Title:   title,
			Year:    year,
			Details: toDetails(nfo.detailsNFO),
			Files:   []File{{Path: filepath.Join(dir, v)}},
		})
	}
	return out
}

func scanShow(dir string, entries []os.DirEntry) (Item, bool) {
	folderTitle, folderYear := folderTitleYear(filepath.Base(dir))
	var nfo tvshowNFO
	readNFO(filepath.Join(dir, "tvshow.nfo"), &nfo)
	title, year := resolveTitleYear(nfo.Title, nfo.Year, nfo.Premiered, folderTitle, folderYear)

	m := Item{
		Title:      title,
		Year:       year,
		IsShow:     true,
		Details:    toDetails(nfo.detailsNFO),
		PosterPath: findImage(entries, dir, []string{"poster", "folder", "cover", "default"}, []string{"-poster"}),
	}
	// Collect episode files anywhere under the show directory.
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !videoExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		s, e := episodeNumbers(path)
		m.Files = append(m.Files, File{Path: path, Season: s, Episode: e})
		return nil
	})
	if len(m.Files) == 0 {
		return Item{}, false
	}
	return m, true
}

// episodeNumbers reads season/episode from a sibling episode NFO, falling back to
// parsing the filename (S01E02 / 1x02 / bare trailing number).
func episodeNumbers(videoPath string) (int, int) {
	var ep episodeNFO
	if readNFO(stripExt(videoPath)+".nfo", &ep) && (ep.Season > 0 || ep.Episode > 0) {
		return ep.Season, ep.Episode
	}
	p := recognize.ParseName(filepath.Base(videoPath), true)
	return p.Season, p.Episode
}

// --- NFO XML types ---

type xmlUniqueID struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type xmlActor struct {
	Name string `xml:"name"`
	Role string `xml:"role"`
}

type xmlRatings struct {
	Rating []struct {
		Default bool    `xml:"default,attr"`
		Value   float64 `xml:"value"`
	} `xml:"rating"`
}

// detailsNFO holds the fields shared by movie and tvshow NFO files.
type detailsNFO struct {
	Title     string        `xml:"title"`
	Year      int           `xml:"year"`
	Premiered string        `xml:"premiered"`
	Plot      string        `xml:"plot"`
	Outline   string        `xml:"outline"`
	Runtime   int           `xml:"runtime"`
	MPAA      string        `xml:"mpaa"`
	Genres    []string      `xml:"genre"`
	Studios   []string      `xml:"studio"`
	Countries []string      `xml:"country"`
	Directors []string      `xml:"director"`
	Credits   []string      `xml:"credits"`
	Rating    float64       `xml:"rating"`
	Ratings   xmlRatings    `xml:"ratings"`
	UniqueIDs []xmlUniqueID `xml:"uniqueid"`
	Actors    []xmlActor    `xml:"actor"`
}

type movieNFO struct {
	XMLName xml.Name `xml:"movie"`
	detailsNFO
}

type tvshowNFO struct {
	XMLName xml.Name `xml:"tvshow"`
	detailsNFO
}

type episodeNFO struct {
	XMLName xml.Name `xml:"episodedetails"`
	Title   string   `xml:"title"`
	Season  int      `xml:"season"`
	Episode int      `xml:"episode"`
}

func readNFO(path string, v any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return xml.Unmarshal(data, v) == nil
}

// toDetails maps the raw NFO fields into the source-neutral Details.
func toDetails(d detailsNFO) Details {
	out := Details{
		Description:   strings.TrimSpace(firstNonEmpty(d.Plot, d.Outline)),
		Release:       strings.TrimSpace(d.Premiered),
		Runtime:       d.Runtime,
		Directors:     trimList(d.Directors),
		Writers:       trimList(d.Credits),
		Rating:        bestRating(d),
		ContentRating: strings.TrimSpace(d.MPAA),
	}
	if len(d.Studios) > 0 {
		out.Studio = strings.TrimSpace(d.Studios[0])
	}
	for _, u := range d.UniqueIDs {
		if t := strings.TrimSpace(u.Type); t != "" {
			out.UniqueIDs = append(out.UniqueIDs, UniqueID{Type: t, Value: strings.TrimSpace(u.Value)})
		}
	}
	for _, a := range d.Actors {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		if role := strings.TrimSpace(a.Role); role != "" {
			name += " (" + role + ")"
		}
		out.Actors = append(out.Actors, name)
	}
	for _, g := range d.Genres {
		if g = strings.TrimSpace(g); g != "" {
			out.Genres = append(out.Genres, g)
		}
	}
	return out
}

func bestRating(d detailsNFO) string {
	for _, r := range d.Ratings.Rating {
		if r.Default && r.Value > 0 {
			return strconv.FormatFloat(r.Value, 'f', 1, 64)
		}
	}
	if len(d.Ratings.Rating) > 0 && d.Ratings.Rating[0].Value > 0 {
		return strconv.FormatFloat(d.Ratings.Rating[0].Value, 'f', 1, 64)
	}
	if d.Rating > 0 {
		return strconv.FormatFloat(d.Rating, 'f', 1, 64)
	}
	return ""
}

// resolveTitleYear prefers NFO data, then the premiered date's year, then the folder
// name.
func resolveTitleYear(nfoTitle string, nfoYear int, premiered, folderTitle string, folderYear int) (string, int) {
	title := strings.TrimSpace(nfoTitle)
	if title == "" {
		title = folderTitle
	}
	year := nfoYear
	if year == 0 && len(premiered) >= 4 {
		if y, err := strconv.Atoi(premiered[:4]); err == nil {
			year = y
		}
	}
	if year == 0 {
		year = folderYear
	}
	return title, year
}

// folderTitleYear derives a title and year from a folder/file name using the shared
// recognize parser (handling "The Matrix (1999)", "(1999) The Matrix", and bare-year
// release names alike), so the Jellyfin scanner does not carry its own name regexes.
func folderTitleYear(name string) (string, int) {
	p := recognize.ParseName(name, false)
	return p.Title, p.Year
}

func findImage(entries []os.DirEntry, dir string, bases, suffixes []string) string {
	for _, want := range bases {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			base := strings.ToLower(stripExt(e.Name()))
			if base == want && imageExts[strings.ToLower(filepath.Ext(e.Name()))] {
				return filepath.Join(dir, e.Name())
			}
		}
	}
	for _, suf := range suffixes {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			base := strings.ToLower(stripExt(e.Name()))
			if strings.HasSuffix(base, suf) && imageExts[strings.ToLower(filepath.Ext(e.Name()))] {
				return filepath.Join(dir, e.Name())
			}
		}
	}
	return ""
}

func stripExt(name string) string { return strings.TrimSuffix(name, filepath.Ext(name)) }

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func trimList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
