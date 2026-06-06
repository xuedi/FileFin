// Package jellyfin imports a Jellyfin/Kodi media library that uses the NFO
// metadata format: per-item .nfo XML files plus poster/fanart image sidecars.
// This is the documented, version-stable on-disk format Jellyfin reads and
// writes (https://jellyfin.org/docs/general/server/metadata/nfo/). It reads the
// library tree and never modifies it.
package jellyfin

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"filefin/internal/importer"
	"filefin/internal/model"
)

var videoExts = map[string]bool{
	".mp4": true, ".webm": true, ".mkv": true, ".avi": true,
	".mov": true, ".m4v": true, ".ts": true, ".m2ts": true, ".wmv": true,
}

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".bmp": true, ".tbn": true,
}

var (
	reYear   = regexp.MustCompile(`\((19\d{2}|20\d{2})\)`)
	reSeason = regexp.MustCompile(`(?i)^(season\b|specials$|s\d+$)`)
)

// Scan walks a Jellyfin library directory and returns one importer.Media per
// movie or show, all assigned to the given category.
func Scan(root, category string) ([]importer.Media, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []importer.Media
	var loose []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			if m, ok := scanDir(filepath.Join(root, e.Name()), category); ok {
				out = append(out, m)
			}
			continue
		}
		// A loose movie file directly under root; grouped after the walk so a
		// multi-disc movie's parts land in one folder.
		if videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			loose = append(loose, e.Name())
		}
	}
	return append(out, scanLooseMovies(root, loose, category)...), nil
}

func scanDir(dir, category string) (importer.Media, bool) {
	if isShowDir(dir) {
		return scanShow(dir, category)
	}
	return scanMovieDir(dir, category)
}

func isShowDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && reSeason.MatchString(e.Name()) {
			return true
		}
		if !e.IsDir() && strings.EqualFold(e.Name(), "tvshow.nfo") {
			return true
		}
	}
	return false
}

func scanMovieDir(dir, category string) (importer.Media, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return importer.Media{}, false
	}
	var videos []string
	for _, e := range entries {
		if !e.IsDir() && videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			videos = append(videos, e.Name())
		}
	}
	if len(videos) == 0 {
		return importer.Media{}, false
	}
	folderTitle, folderYear := parseFolderName(filepath.Base(dir))

	// NFO: prefer movie.nfo, else <video>.nfo for the first video.
	var nfo movieNFO
	if !readNFO(filepath.Join(dir, "movie.nfo"), &nfo) {
		readNFO(filepath.Join(dir, stripExt(videos[0])+".nfo"), &nfo)
	}

	title, year := resolveTitleYear(nfo.Title, nfo.Year, nfo.Premiered, folderTitle, folderYear)
	m := importer.Media{
		Category:   category,
		Title:      title,
		Year:       year,
		Meta:       metaFromDetails(nfo.detailsNFO, year),
		PosterPath: findImage(dir, []string{"poster", "folder", "cover", "default"}, []string{"-poster"}),
	}
	for _, v := range videos {
		path := filepath.Join(dir, v)
		m.Files = append(m.Files, importer.SourceFile{Path: path, Subtitles: importer.FindSidecarSubtitles(path)})
	}
	return m, true
}

// scanLooseMovies turns loose video files directly under root into one Media each,
// grouping a multi-disc movie's parts (same name modulo a "CD1"/"Part 2" marker)
// into a single folder. First-seen order is preserved.
func scanLooseMovies(dir string, videos []string, category string) []importer.Media {
	type key struct {
		title string
		year  int
	}
	var order []key
	groups := map[key][]string{}
	for _, v := range videos {
		base := stripExt(v)
		if stripped, _, ok := importer.DetectPart(base); ok {
			base = stripped
		}
		title, year := parseFolderName(base)
		k := key{title, year}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], v)
	}
	var out []importer.Media
	for _, k := range order {
		vs := groups[k]
		var nfo movieNFO
		readNFO(filepath.Join(dir, stripExt(vs[0])+".nfo"), &nfo)
		title, year := resolveTitleYear(nfo.Title, nfo.Year, nfo.Premiered, k.title, k.year)
		m := importer.Media{
			Category: category,
			Title:    title,
			Year:     year,
			Meta:     metaFromDetails(nfo.detailsNFO, year),
		}
		for _, v := range vs {
			path := filepath.Join(dir, v)
			m.Files = append(m.Files, importer.SourceFile{Path: path, Subtitles: importer.FindSidecarSubtitles(path)})
		}
		out = append(out, m)
	}
	return out
}

func scanShow(dir, category string) (importer.Media, bool) {
	folderTitle, folderYear := parseFolderName(filepath.Base(dir))
	var nfo tvshowNFO
	readNFO(filepath.Join(dir, "tvshow.nfo"), &nfo)
	title, year := resolveTitleYear(nfo.Title, nfo.Year, nfo.Premiered, folderTitle, folderYear)

	m := importer.Media{
		Category:   category,
		Title:      title,
		Year:       year,
		IsShow:     true,
		Meta:       metaFromDetails(nfo.detailsNFO, year),
		PosterPath: findImage(dir, []string{"poster", "folder", "cover", "default"}, []string{"-poster"}),
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
		m.Files = append(m.Files, importer.SourceFile{Path: path, Season: s, Episode: e, Subtitles: importer.FindSidecarSubtitles(path)})
		return nil
	})
	if len(m.Files) == 0 {
		return importer.Media{}, false
	}
	return m, true
}

// episodeNumbers reads season/episode from a sibling episode NFO, falling back
// to parsing the filename (S01E02 / 1x02).
func episodeNumbers(videoPath string) (int, int) {
	var ep episodeNFO
	if readNFO(stripExt(videoPath)+".nfo", &ep) && (ep.Season > 0 || ep.Episode > 0) {
		return ep.Season, ep.Episode
	}
	p := importer.ParseName(filepath.Base(videoPath), true)
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

func metaFromDetails(d detailsNFO, year int) importer.MetaContent {
	mc := importer.MetaContent{Description: firstNonEmpty(d.Plot, d.Outline)}
	add := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			mc.Metadata = append(mc.Metadata, model.KV{Key: k, Value: v})
		}
	}
	release := strings.TrimSpace(d.Premiered)
	if release == "" && year > 0 {
		release = strconv.Itoa(year)
	}
	add("release", release)
	if d.Runtime > 0 {
		add("runtime", strconv.Itoa(d.Runtime))
	}
	add("directedBy", strings.Join(d.Directors, ", "))
	add("writtenBy", strings.Join(d.Credits, ", "))
	if r := bestRating(d); r != "" {
		add("rating", r)
	}
	add("contentRating", d.MPAA)
	if len(d.Studios) > 0 {
		add("studio", d.Studios[0])
	}
	for _, u := range d.UniqueIDs {
		if t := strings.TrimSpace(u.Type); t != "" {
			add(t+"Id", strings.TrimSpace(u.Value))
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
		mc.Actors = append(mc.Actors, name)
	}
	for _, g := range d.Genres {
		if g = strings.TrimSpace(g); g != "" {
			mc.Tags = append(mc.Tags, strings.ToLower(g))
		}
	}
	return mc
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

// resolveTitleYear prefers NFO data, then the premiered date's year, then the
// folder name.
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

// parseFolderName extracts a title and year from a folder/file name that
// contains a "(YYYY)" anywhere (e.g. "The Matrix (1999)" or "(1999) The Matrix").
func parseFolderName(name string) (string, int) {
	year := 0
	if m := reYear.FindStringSubmatch(name); m != nil {
		year, _ = strconv.Atoi(m[1])
		name = reYear.ReplaceAllString(name, "")
	}
	return strings.Trim(strings.TrimSpace(name), " -"), year
}

func findImage(dir string, bases, suffixes []string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
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
