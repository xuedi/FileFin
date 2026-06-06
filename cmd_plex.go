package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/urfave/cli/v2"

	"filefin/internal/importer"
	"filefin/internal/model"
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
	var items []importer.Media
	for _, it := range plexItems {
		items = append(items, plexToMedia(it, remaps))
	}
	if n := c.Int("limit"); n > 0 && n < len(items) {
		items = items[:n]
	}
	return planAndApply(c, items, cfg.DataDir)
}

func plexToMedia(it plex.Item, remaps []remap) importer.Media {
	m := importer.Media{
		Category:   it.Section,
		Title:      it.Title,
		Year:       it.Year,
		IsShow:     it.IsShow,
		Meta:       metaFromPlex(it),
		PosterPath: it.PosterPath,
		BannerPath: it.BannerPath,
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
