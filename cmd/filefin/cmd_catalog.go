package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
	"golang.org/x/term"

	"filefin/internal/config"
	"filefin/internal/importer"
)

// planAndApply is shared by the plex and jellyfin commands: it prints a
// new/existing plan for the catalog, then (unless --dry-run) confirms and
// applies it. The command's flags (dry-run/yes/force/no-posters) drive it.
func planAndApply(c *cli.Context, items []importer.Media, dataDir string) error {
	type plan struct {
		m      importer.Media
		exists bool
	}
	var plans []plan
	newCount, existCount := 0, 0
	for _, m := range items {
		_, statErr := os.Stat(m.TargetFolder(dataDir))
		exists := statErr == nil
		plans = append(plans, plan{m: m, exists: exists})
		if exists {
			existCount++
		} else {
			newCount++
		}
	}

	color := term.IsTerminal(int(os.Stdout.Fd()))
	for _, p := range plans {
		printDiffLine(p.m, p.exists, color)
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
		if _, err := p.m.Apply(dataDir, c.Bool("force"), !c.Bool("no-posters")); err != nil {
			fmt.Printf("  error: %s (%d): %v\n", p.m.Title, p.m.Year, err)
			failed++
			continue
		}
		applied++
	}
	fmt.Printf("Imported %d item(s), %d failed.\n", applied, failed)
	fmt.Printf("Run `%s rebuild` to update the cache.\n", config.AppName)
	return nil
}

func printDiffLine(m importer.Media, exists, color bool) {
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
		col, mark, label, m.Category, m.Year, m.Title, len(m.Files), reset)
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
