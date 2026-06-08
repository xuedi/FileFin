// Package importer turns a staged import row into media on disk: it copies the
// source file into the canonical layout and writes the folder's meta.json. It writes
// only inside the data directory; the source is read-only. The package depends only
// on the data it is handed, never on how a row was produced.
package importer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"filefin/internal/ffprobe"
	"filefin/internal/omdb"
	"filefin/internal/plex"
	"filefin/internal/state"
)

// Meta is a media folder's metadata, serialized to meta.json. The app edits it
// through the GUI, so structured JSON is cleaner than markdown. It carries the OMDb
// field set plus a ffprobe-derived technical block.
type Meta struct {
	Title       string             `json:"title"`
	Year        int                `json:"year"`
	Description string             `json:"description,omitempty"`
	Plot        string             `json:"plot,omitempty"`
	Metadata    map[string]string  `json:"metadata,omitempty"`
	Ratings     map[string]string  `json:"ratings,omitempty"`
	Actors      []string           `json:"actors,omitempty"`
	Tags        []string           `json:"tags,omitempty"`
	Technical   *ffprobe.Technical `json:"technical,omitempty"`
	// Enriched is true once the enrichment agent has filled this folder from OMDb. A
	// freshly imported folder carries stub metadata with Enriched false; the scan
	// queues those, and the agent flips it true after a successful lookup.
	Enriched bool `json:"enriched,omitempty"`
	// State is the per-user playback state, keyed by username. It is written by the
	// playback-state handlers through the same per-folder lock as the rest of Meta;
	// a folder nobody has touched carries no state key (omitempty).
	State map[string]state.UserState `json:"state,omitempty"`
}

// WriteMeta writes meta.json into folder, overwriting any existing file.
func WriteMeta(folder string, m Meta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(folder, "meta.json"), data, 0o644)
}

// ReadMeta parses a media folder's meta.json. A cache rebuild uses it to recover the
// title/year/description it once wrote.
func ReadMeta(folder string) (Meta, error) {
	data, err := os.ReadFile(filepath.Join(folder, "meta.json"))
	if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return Meta{}, err
	}
	return m, nil
}

// StubMeta is the minimal metadata used when no OMDb enrichment is available.
func StubMeta(title string, year int) Meta {
	m := Meta{Title: title, Year: year}
	if year > 0 {
		m.Metadata = map[string]string{"release": strconv.Itoa(year)}
	}
	return m
}

// MetaFromOMDb maps an OMDb result into a Meta. The caller's title/year always win
// over OMDb's so the file matches its folder name. "N/A" values are dropped, genres
// are lowercased into tags.
func MetaFromOMDb(mv *omdb.Movie, title string, year int) Meta {
	m := Meta{Title: title, Year: year, Description: clean(mv.Plot), Enriched: true}

	md := map[string]string{}
	add := func(k, v string) {
		if v := clean(v); v != "" {
			md[k] = v
		}
	}
	release := clean(mv.Released)
	if release == "" && year > 0 {
		release = strconv.Itoa(year)
	}
	add("release", release)
	add("runtime", strings.TrimSuffix(clean(mv.Runtime), " min"))
	add("language", mv.Language)
	add("origin", mv.Country)
	add("directedBy", mv.Director)
	add("writtenBy", mv.Writer)
	add("contentRating", mv.Rated)
	add("awards", mv.Awards)
	add("boxOffice", mv.BoxOffice)
	add("imdbID", mv.ImdbID)
	if len(md) > 0 {
		m.Metadata = md
	}

	rt := map[string]string{}
	rate := func(k, v string) {
		if v := clean(v); v != "" {
			rt[k] = v
		}
	}
	rate("imdb", imdbRatingWithVotes(mv))
	rate("rottenTomatoes", mv.RatingBySource("Rotten Tomatoes"))
	rate("metacritic", metacritic(mv))
	if len(rt) > 0 {
		m.Ratings = rt
	}

	for _, a := range splitList(mv.Actors) {
		m.Actors = append(m.Actors, a)
	}
	for _, g := range splitList(mv.Genre) {
		m.Tags = append(m.Tags, strings.ToLower(g))
	}
	return m
}

// MetaFromPlex maps a Plex item into a Meta from Plex's own fields. It is left
// unenriched on purpose: Plex's metadata is the starting point, and the OMDb
// enricher later fills any gaps additively (never overwriting these values). The
// caller's title/year are applied by the importer so the file matches its folder.
func MetaFromPlex(item plex.Item) Meta {
	m := Meta{Title: item.Title, Year: item.Year, Description: strings.TrimSpace(item.Summary)}

	md := map[string]string{}
	add := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			md[k] = v
		}
	}
	release := item.Release
	if release == "" && item.Year > 0 {
		release = strconv.Itoa(item.Year)
	}
	add("release", release)
	if item.Runtime > 0 {
		add("runtime", strconv.Itoa(item.Runtime))
	}
	add("directedBy", strings.Join(item.Directors, ", "))
	add("writtenBy", strings.Join(item.Writers, ", "))
	add("contentRating", item.ContentRating)
	if len(md) > 0 {
		m.Metadata = md
	}
	if r := strings.TrimSpace(item.Rating); r != "" {
		m.Ratings = map[string]string{"plex": r}
	}
	m.Actors = append(m.Actors, item.Actors...)
	for _, g := range item.Genres {
		m.Tags = append(m.Tags, strings.ToLower(g))
	}
	return m
}

// MergeMeta returns base with any fields it is missing filled in from add. It is
// purely additive: every value already present in base is kept, so enrichment only
// fills gaps and never overwrites metadata an import (e.g. Plex) already provided.
// Title/year and the ffprobe technical block always come from base.
func MergeMeta(base, add Meta) Meta {
	out := base
	if out.Description == "" {
		out.Description = add.Description
	}
	if out.Plot == "" {
		out.Plot = add.Plot
	}
	out.Metadata = mergeStringMap(out.Metadata, add.Metadata)
	out.Ratings = mergeStringMap(out.Ratings, add.Ratings)
	if len(out.Actors) == 0 {
		out.Actors = add.Actors
	}
	if len(out.Tags) == 0 {
		out.Tags = add.Tags
	}
	if out.Technical == nil {
		out.Technical = add.Technical
	}
	// State is owned by base and carried through unchanged: an OMDb enrich never
	// contributes state, and out := base already preserves it, so a future add that
	// happened to carry state can never clobber base's.
	return out
}

// mergeStringMap fills keys missing (or blank) in base from add, keeping base's
// existing values. A nil base is created only when add has something to contribute.
func mergeStringMap(base, add map[string]string) map[string]string {
	for k, v := range add {
		if v == "" {
			continue
		}
		if cur, ok := base[k]; !ok || cur == "" {
			if base == nil {
				base = map[string]string{}
			}
			base[k] = v
		}
	}
	return base
}

func imdbRatingWithVotes(m *omdb.Movie) string {
	rating := clean(m.ImdbRating)
	if rating == "" {
		return ""
	}
	if votes := clean(m.ImdbVotes); votes != "" {
		return fmt.Sprintf("%s (%s votes)", rating, votes)
	}
	return rating
}

func metacritic(m *omdb.Movie) string {
	if v := m.RatingBySource("Metacritic"); v != "" {
		return v
	}
	if v := clean(m.Metascore); v != "" {
		return v + "/100"
	}
	return ""
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = clean(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// clean drops OMDb's "N/A" sentinel and trims whitespace.
func clean(s string) string {
	s = strings.TrimSpace(s)
	if s == "N/A" {
		return ""
	}
	return s
}

// CopyFile copies src to dst via a ".part" temp file plus an atomic rename, so a
// partial copy never leaves a usable-looking file. progress, if non-nil, is called
// on every write with the bytes copied so far and the total size.
func CopyFile(src, dst string, progress func(copied, total int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	var total int64
	if fi, err := in.Stat(); err == nil {
		total = fi.Size()
	}

	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	var w io.Writer = out
	if progress != nil {
		w = &progressWriter{w: out, total: total, progress: progress}
	}
	if _, err := io.Copy(w, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

type progressWriter struct {
	w        io.Writer
	total    int64
	copied   int64
	progress func(copied, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.copied += int64(n)
	p.progress(p.copied, p.total)
	return n, err
}
