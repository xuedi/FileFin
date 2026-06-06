package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/urfave/cli/v2"

	"filefin/internal/config"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/model"
	"filefin/internal/omdb"
)

func cmdImport(c *cli.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	var category, source string
	switch c.NArg() {
	case 1:
		source = c.Args().Get(0)
	case 2:
		category, source = c.Args().Get(0), c.Args().Get(1)
	default:
		return errors.New("usage: import [category] <file>")
	}
	if c.String("category") != "" {
		category = c.String("category")
	}

	src, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	detected := importer.ParseName(filepath.Base(src))

	title := detected.Title
	if c.String("title") != "" {
		title = c.String("title")
	}
	year := detected.Year
	if c.IsSet("year") {
		year = c.Int("year")
	}
	season := detected.Season
	if c.IsSet("season") {
		season = c.Int("season")
	}
	episode := detected.Episode
	if c.IsSet("episode") {
		episode = c.Int("episode")
	}
	part := c.Int("part")

	interactive := !c.Bool("yes")
	if interactive {
		title = promptDefault("Title", title)
		year = promptIntDefault("Year", year)
	}

	if category == "" {
		if !interactive {
			return errors.New("category is required (pass it as the first argument or --category)")
		}
		if category, err = chooseCategory(cfg.DataDir); err != nil {
			return err
		}
	}

	req := importer.Request{
		SourcePath: src,
		DataDir:    cfg.DataDir,
		Category:   category,
		Title:      strings.TrimSpace(title),
		Year:       year,
		Season:     season,
		Episode:    episode,
		Part:       part,
		Move:       c.Bool("move"),
		Force:      c.Bool("force"),
		Progress:   copyProgress(),
	}

	if interactive {
		fmt.Printf("Import into: %s / %s\n", category, importer.FolderName(year, req.Title))
		if !promptYesNo("Proceed?", true) {
			fmt.Println("Aborted.")
			return nil
		}
	}

	res, err := importer.Execute(req)
	if err != nil {
		return err
	}
	if res.Skipped {
		fmt.Printf("Unchanged (same size), skipped: %s\n", res.TargetPath)
	} else {
		fmt.Printf("Imported: %s\n", res.TargetPath)
		lg, closeLog := openLogger(cfg)
		lg.For(logging.Import).Info(fmt.Sprintf("imported %s into %s", req.Title, category),
			logging.Fields{"title": req.Title, "category": category, "path": res.TargetPath})
		closeLog()
	}

	// Enrich the folder once: when it has no mediaEnriched flag yet (or --force),
	// write meta.md and download a poster. Already-enriched folders (e.g. when
	// adding another episode) keep their existing, possibly hand-edited meta.md.
	if c.Bool("force") || !importer.AlreadyEnriched(res.Folder) {
		enrichMeta(cfg, req, res, !c.Bool("no-fetch"))
	}
	fmt.Printf("Run `%s rebuild` to update the cache.\n", config.AppName)
	return nil
}

// enrichMeta writes meta.md for a freshly imported media folder. When an OMDb API
// key is configured and fetching is enabled, it pulls metadata and a poster;
// otherwise (or on failure) it falls back to a minimal stub.
func enrichMeta(cfg *config.Config, req importer.Request, res *importer.Result, fetch bool) {
	key := cfg.APIKeys["omdb"]
	if fetch && key != "" {
		client := omdb.New(key)
		movie, err := client.Lookup(req.Title, req.Year)
		if err != nil {
			fmt.Printf("OMDb lookup failed (%v); writing stub meta.md\n", err)
		} else {
			mc := metaFromOMDb(movie, req.Title, req.Year)
			if err := writeEnrichedMeta(cfg, res, mc); err != nil {
				fmt.Printf("could not write meta.md: %v\n", err)
				return
			}
			fmt.Println("Wrote meta.md from OMDb")
			posterPath := filepath.Join(res.Folder, "poster.jpg")
			if _, statErr := os.Stat(posterPath); movie.ImdbID != "" && statErr != nil {
				if data, _, err := client.Poster(movie.ImdbID, 600); err == nil && len(data) > 0 {
					if err := os.WriteFile(posterPath, data, 0o644); err == nil {
						fmt.Println("Downloaded poster.jpg")
					}
				}
			}
			return
		}
	}
	if err := writeEnrichedMeta(cfg, res, importer.StubMeta(req.Title, req.Year)); err != nil {
		fmt.Printf("could not write meta.md: %v\n", err)
		return
	}
	fmt.Printf("Wrote meta stub: %s\n", filepath.Join(res.Folder, "meta.md"))
}

// writeEnrichedMeta probes the imported file for technical facts, appends the
// mediaEnriched flag, and writes meta.md. The flag is set even when probing
// yields nothing, so enrichment is attempted exactly once.
func writeEnrichedMeta(cfg *config.Config, res *importer.Result, mc importer.MetaContent) error {
	mc.Technical = technicalProvider(cfg)([]string{res.TargetPath})
	mc.Technical = append(mc.Technical, model.KV{Key: "mediaEnriched", Value: "true"})
	return importer.WriteMeta(res.Folder, mc)
}

// metaFromOMDb maps an OMDb result into meta.md content, with the full useful
// field set: a `## metadata` block, a `## ratings` block (imdb/RT/Metacritic),
// and the description, actors, and tags. The user's title/year win over OMDb's
// so the file matches its folder name. Keys are emitted in a fixed order.
func metaFromOMDb(m *omdb.Movie, title string, year int) importer.MetaContent {
	mc := importer.MetaContent{Title: title, Description: clean(m.Plot)}
	add := func(k, v string) {
		if v := clean(v); v != "" {
			mc.Metadata = append(mc.Metadata, model.KV{Key: k, Value: v})
		}
	}
	release := clean(m.Released)
	if release == "" && year > 0 {
		release = strconv.Itoa(year) // fall back to the year when OMDb omits a date
	}
	add("release", release)
	add("runtime", strings.TrimSuffix(clean(m.Runtime), " min"))
	add("language", m.Language)
	add("origin", m.Country)
	add("directedBy", m.Director)
	add("writtenBy", m.Writer)
	add("contentRating", m.Rated)
	add("awards", m.Awards)
	add("boxOffice", m.BoxOffice)
	add("imdbID", m.ImdbID)
	if strings.EqualFold(clean(m.Type), "series") {
		add("seasons", m.TotalSeasons)
	}

	rate := func(k, v string) {
		if v := clean(v); v != "" {
			mc.Ratings = append(mc.Ratings, model.KV{Key: k, Value: v})
		}
	}
	rate("imdb", imdbRatingWithVotes(m))
	rate("rottenTomatoes", m.RatingBySource("Rotten Tomatoes"))
	rate("metacritic", metacritic(m))

	for _, a := range splitList(m.Actors) {
		mc.Actors = append(mc.Actors, a)
	}
	for _, g := range splitList(m.Genre) {
		mc.Tags = append(mc.Tags, strings.ToLower(g))
	}
	return mc
}

// imdbRatingWithVotes renders the imdb rating with its vote count when both are
// present ("8.1 (835,123 votes)"), the bare rating when votes are missing, or "".
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

// metacritic prefers the Ratings[] entry (already formatted "84/100"), falling
// back to the bare Metascore.
func metacritic(m *omdb.Movie) string {
	if v := m.RatingBySource("Metacritic"); v != "" {
		return v
	}
	if v := clean(m.Metascore); v != "" {
		return v + "/100"
	}
	return ""
}

// mergeMeta returns primary with gaps filled from fallback: an empty description
// is taken from fallback, empty actors/tags lists are taken whole, and any
// metadata/ratings key missing from primary is appended from fallback (preserving
// fallback's order for the added keys). primary's values always win.
func mergeMeta(primary, fallback importer.MetaContent) importer.MetaContent {
	out := primary
	if clean(out.Description) == "" {
		out.Description = fallback.Description
	}
	if clean(out.Plot) == "" {
		out.Plot = fallback.Plot
	}
	if len(out.Actors) == 0 {
		out.Actors = fallback.Actors
	}
	if len(out.Tags) == 0 {
		out.Tags = fallback.Tags
	}
	out.Metadata = fillKeys(out.Metadata, fallback.Metadata)
	out.Ratings = fillKeys(out.Ratings, fallback.Ratings)
	return out
}

// dropKey returns kvs without any entry whose key equals key.
func dropKey(kvs []model.KV, key string) []model.KV {
	var out []model.KV
	for _, kv := range kvs {
		if kv.Key != key {
			out = append(out, kv)
		}
	}
	return out
}

// fillKeys appends every key from fallback that is absent in primary, keeping
// primary's entries first and in order.
func fillKeys(primary, fallback []model.KV) []model.KV {
	have := make(map[string]bool, len(primary))
	for _, kv := range primary {
		have[kv.Key] = true
	}
	out := primary
	for _, kv := range fallback {
		if !have[kv.Key] {
			out = append(out, kv)
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

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = clean(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// chooseCategory lists existing categories and lets the user pick one by number
// or type a new category name.
func chooseCategory(dataDir string) (string, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return "", err
	}
	var cats []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			cats = append(cats, e.Name())
		}
	}
	sort.Strings(cats)

	if len(cats) > 0 {
		fmt.Println("Existing categories:")
		for i, name := range cats {
			fmt.Printf("  %d) %s\n", i+1, name)
		}
	}
	in, err := prompt("Choose a number or type a new category name: ")
	if err != nil {
		return "", err
	}
	in = strings.TrimSpace(in)
	if in == "" {
		return "", errors.New("no category chosen")
	}
	if n, err := strconv.Atoi(in); err == nil && n >= 1 && n <= len(cats) {
		return cats[n-1], nil
	}
	return in, nil
}

func promptDefault(label, def string) string {
	line, _ := prompt(fmt.Sprintf("%s [%s]: ", label, def))
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptIntDefault(label string, def int) int {
	for {
		s := promptDefault(label, strconv.Itoa(def))
		n, err := strconv.Atoi(s)
		if err == nil {
			return n
		}
		fmt.Println("Please enter a number.")
	}
}

func promptYesNo(label string, def bool) bool {
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	line, _ := prompt(fmt.Sprintf("%s %s ", label, hint))
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return def
	}
	return strings.HasPrefix(line, "y")
}
