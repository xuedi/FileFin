package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v2"
	"golang.org/x/term"

	"filefin/internal/config"
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

	items, err := db.Items(c.String("section"))
	if err != nil {
		return err
	}

	remaps := parseRemaps(c.StringSlice("remap"))
	for i := range items {
		for j := range items[i].Files {
			items[i].Files[j].Path = applyRemap(items[i].Files[j].Path, remaps)
		}
	}
	if n := c.Int("limit"); n > 0 && n < len(items) {
		items = items[:n]
	}

	// Plan: classify each item as new or already present in the data dir.
	type plan struct {
		item   plex.Item
		folder string
		exists bool
	}
	var plans []plan
	newCount, existCount := 0, 0
	for _, it := range items {
		folder := filepath.Join(cfg.DataDir, it.Section, importer.FolderName(it.Year, it.Title))
		_, statErr := os.Stat(folder)
		exists := statErr == nil
		plans = append(plans, plan{item: it, folder: folder, exists: exists})
		if exists {
			existCount++
		} else {
			newCount++
		}
	}

	color := term.IsTerminal(int(os.Stdout.Fd()))
	for _, p := range plans {
		printDiffLine(p.item, p.exists, color)
	}
	fmt.Printf("\n%d new, %d existing, %d total\n", newCount, existCount, len(plans))

	if c.Bool("dry-run") {
		fmt.Println("(dry run; nothing written)")
		return nil
	}
	toApply := newCount
	if c.Bool("force") {
		toApply = len(plans)
	}
	if toApply == 0 {
		fmt.Println("Nothing to import.")
		return nil
	}
	if !c.Bool("yes") && !promptYesNo(fmt.Sprintf("Import %d item(s)?", toApply), false) {
		fmt.Println("Aborted.")
		return nil
	}

	applied, failed := 0, 0
	for _, p := range plans {
		if p.exists && !c.Bool("force") {
			continue
		}
		if err := applyPlexItem(p.item, cfg.DataDir, c.Bool("force"), !c.Bool("no-posters")); err != nil {
			fmt.Printf("  error: %s (%d): %v\n", p.item.Title, p.item.Year, err)
			failed++
			continue
		}
		applied++
	}
	fmt.Printf("Imported %d item(s), %d failed.\n", applied, failed)
	fmt.Printf("Run `%s rebuild` to update the cache.\n", config.AppName)
	return nil
}

func printDiffLine(it plex.Item, exists, color bool) {
	mark, label := "+", "new"
	col, reset := "", ""
	if exists {
		mark, label = "~", "exists"
		if color {
			col, reset = "\033[33m", "\033[0m" // yellow
		}
	} else if color {
		col, reset = "\033[32m", "\033[0m" // green
	}
	fmt.Printf("%s%s [%s] %s / (%d) %s  - %d file(s)%s\n",
		col, mark, label, it.Section, it.Year, it.Title, len(it.Files), reset)
}

func applyPlexItem(it plex.Item, dataDir string, force, posters bool) error {
	if len(it.Files) == 0 {
		return errors.New("no media files")
	}
	var folder string
	for _, f := range it.Files {
		res, err := importer.Execute(importer.Request{
			SourcePath: f.Path,
			DataDir:    dataDir,
			Category:   it.Section,
			Title:      it.Title,
			Year:       it.Year,
			Season:     f.Season,
			Episode:    f.Episode,
			Force:      force,
		})
		if err != nil {
			return err
		}
		folder = res.Folder
	}
	if err := importer.WriteMeta(folder, metaFromPlex(it)); err != nil {
		return err
	}
	if posters {
		if it.PosterPath != "" {
			_ = copyFileTo(it.PosterPath, filepath.Join(folder, "poster.jpg"))
		}
		if it.BannerPath != "" {
			_ = copyFileTo(it.BannerPath, filepath.Join(folder, "banner.jpg"))
		}
	}
	return nil
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

type remap struct{ from, to string }

func parseRemaps(specs []string) []remap {
	var out []remap
	for _, s := range specs {
		if i := strings.Index(s, "="); i > 0 {
			out = append(out, remap{from: s[:i], to: s[i+1:]})
		}
	}
	return out
}

func applyRemap(path string, remaps []remap) string {
	for _, r := range remaps {
		if strings.HasPrefix(path, r.from) {
			return r.to + path[len(r.from):]
		}
	}
	return path
}

func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
