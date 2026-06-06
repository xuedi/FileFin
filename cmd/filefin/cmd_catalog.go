package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
	"golang.org/x/term"

	"filefin/internal/config"
	"filefin/internal/importer"
	"filefin/internal/progress"
)

// copyProgress returns a per-file copy callback that draws a live braille bar to
// stderr, or nil when stderr is not a terminal (so logs and pipes stay clean).
func copyProgress() importer.ProgressFunc {
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return nil
	}
	return progress.NewReporter(os.Stderr).Track
}

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
	if len(plans) == 0 {
		fmt.Println("Nothing to import.")
		return nil
	}
	// Every item is processed: existing media folders are revisited so changed files
	// get re-copied, while unchanged files and present sidecars are skipped per-file.
	if !c.Bool("yes") && !promptYesNo(fmt.Sprintf("Process %d item(s)? (unchanged files are skipped)", len(plans)), false) {
		fmt.Println("Aborted.")
		return nil
	}

	prog := copyProgress()
	copied, skipped, failed := 0, 0, 0
	for i, p := range plans {
		fmt.Printf("[%d/%d] %s / (%d) %s\n", i+1, len(plans), p.m.Category, p.m.Year, p.m.Title)
		_, stats, err := p.m.Apply(dataDir, c.Bool("force"), !c.Bool("no-posters"), prog)
		if err != nil {
			fmt.Printf("  error: %v\n", err)
			failed++
			continue
		}
		copied += stats.Copied
		skipped += stats.Skipped
	}
	fmt.Printf("Done: %d file(s) copied, %d unchanged, %d item(s) failed.\n", copied, skipped, failed)
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
