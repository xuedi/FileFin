package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/urfave/cli/v2"

	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/model"
	"filefin/internal/omdb"
	"filefin/internal/plex"
)

func cmdPlex(c *cli.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if c.NArg() < 1 {
		return errors.New("usage: plex <library.db>")
	}
	dbPath := c.Args().First()

	metaDir := c.String("metadata-dir")
	if metaDir == "" && !c.Bool("no-posters") {
		metaDir = plex.DeriveMetadataDir(dbPath)
	}
	db, err := plex.Open(dbPath, metaDir)
	if err != nil {
		return fmt.Errorf("open plex db: %w", err)
	}
	defer db.Close()

	plexItems, err := db.Items(c.String("section"))
	if err != nil {
		return err
	}

	remaps := parseRemaps(c.StringSlice("remap"))
	catOverride := c.String("category")
	var items []importer.Media
	for _, it := range plexItems {
		m := plexToMedia(it, remaps)
		if catOverride != "" {
			m.Category = catOverride
		}
		items = append(items, m)
	}
	if n := c.Int("limit"); n > 0 && n < len(items) {
		items = items[:n]
	}

	// Prefer OMDb when a key is configured: it tends to be richer and consistent
	// with a freshly imported external film. Plex stays the per-item, per-field
	// fallback so the import always completes even when OMDb is unreachable.
	if key := cfg.APIKeys["omdb"]; key != "" && !c.Bool("no-fetch") {
		enriched, fellBack := enrichWithOMDb(items, omdb.New(key).Lookup)
		lg, closeLog := openLogger(cfg)
		lg.For(logging.Plex).Info(
			fmt.Sprintf("OMDb-enriched %d of %d item(s), %d fell back to Plex", enriched, len(items), fellBack),
			logging.Fields{"enriched": enriched, "total": len(items), "fell_back": fellBack})
		closeLog()
	}

	return planAndApply(c, items, cfg)
}

// omdbLookup looks up a movie by title and year. omdb.Client.Lookup satisfies it;
// tests pass a stub.
type omdbLookup func(title string, year int) (*omdb.Movie, error)

// enrichWithOMDb rewrites each item's Plex-derived meta as OMDb-preferred meta,
// keeping the Plex meta as the per-field fallback. Lookups are deduped by
// title+year within the run; an item whose lookup fails keeps its full Plex meta.
// It returns how many items were OMDb-enriched and how many fell back to Plex.
func enrichWithOMDb(items []importer.Media, lookup omdbLookup) (enriched, fellBack int) {
	type result struct {
		movie *omdb.Movie
		err   error
	}
	seen := map[string]result{}
	for i := range items {
		k := strings.ToLower(items[i].Title) + "\x00" + strconv.Itoa(items[i].Year)
		r, ok := seen[k]
		if !ok {
			r.movie, r.err = lookup(items[i].Title, items[i].Year)
			seen[k] = r
		}
		if r.err != nil || r.movie == nil {
			fellBack++
			continue
		}
		omdbMeta := metaFromOMDb(r.movie, items[i].Title, items[i].Year)
		items[i].Meta = mergeMeta(omdbMeta, items[i].Meta)
		enriched++
	}
	return enriched, fellBack
}

func plexToMedia(it plex.Item, remaps []remap) importer.Media {
	m := importer.Media{
		Category:   it.Section,
		Title:      it.Title,
		Year:       it.Year,
		IsShow:     it.IsShow,
		Meta:       metaFromPlex(it),
		PosterPath: it.PosterPath,
	}
	for _, f := range it.Files {
		m.Files = append(m.Files, importer.SourceFile{
			Path:    applyRemap(f.Path, remaps),
			Season:  f.Season,
			Episode: f.Episode,
		})
	}
	return m
}

func metaFromPlex(it plex.Item) importer.MetaContent {
	mc := importer.MetaContent{Title: it.Title, Description: it.Summary}
	add := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			mc.Metadata = append(mc.Metadata, model.KV{Key: k, Value: v})
		}
	}
	release := it.Release
	if release == "" && it.Year > 0 {
		release = fmt.Sprintf("%d", it.Year)
	}
	add("release", release)
	if it.Runtime > 0 {
		add("runtime", fmt.Sprintf("%d", it.Runtime))
	}
	add("directedBy", strings.Join(it.Directors, ", "))
	add("writtenBy", strings.Join(it.Writers, ", "))
	add("rating", it.Rating)
	add("contentRating", it.ContentRating)
	mc.Actors = it.Actors
	for _, g := range it.Genres {
		mc.Tags = append(mc.Tags, strings.ToLower(g))
	}
	return mc
}
