package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
	"golang.org/x/term"

	"filefin/internal/config"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/model"
	"filefin/internal/progress"
	"filefin/internal/transcode"
)

// maxDistinct caps how many distinct values a technical key lists before
// collapsing the overflow into ", N more".
const maxDistinct = 10

// copyProgress returns a per-file copy callback that draws a live braille bar to
// stderr, or nil when stderr is not a terminal (so logs and pipes stay clean).
func copyProgress() importer.ProgressFunc {
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return nil
	}
	return progress.NewReporter(os.Stderr).Track
}

// technicalProvider builds an importer.TechnicalFunc that probes each of a media
// folder's files with ffprobe and aggregates their facts into the `## technical`
// key set. Files that fail to probe are skipped; if every probe fails it returns
// nil (enrichment still records the flag). It does not emit mediaEnriched - the
// importer appends that.
func technicalProvider(cfg *config.Config) importer.TechnicalFunc {
	return func(paths []string) []model.KV {
		var infos []transcode.MediaInfo
		for _, p := range paths {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			mi, err := transcode.Inspect(ctx, cfg.FFprobePath, p)
			cancel()
			if err != nil {
				continue
			}
			infos = append(infos, mi)
		}
		if len(infos) == 0 {
			return nil
		}
		return buildTechnical(infos)
	}
}

// buildTechnical maps probed MediaInfos to `## technical` key/value pairs in a
// fixed key order. Per-key values are the order-preserving distinct set across the
// folder's files (capped via aggregate); fileSize is the summed total and bitrate
// the per-file container rate. Empty keys are omitted.
func buildTechnical(infos []transcode.MediaInfo) []model.KV {
	var kvs []model.KV
	add := func(key string, vals []string) {
		if v := aggregate(vals); v != "" {
			kvs = append(kvs, model.KV{Key: key, Value: v})
		}
	}
	perFile := func(f func(transcode.MediaInfo) string) []string {
		out := make([]string, len(infos))
		for i, mi := range infos {
			out[i] = f(mi)
		}
		return out
	}
	acrossStreams := func(f func(transcode.MediaInfo) []string) []string {
		var out []string
		for _, mi := range infos {
			out = append(out, f(mi)...)
		}
		return out
	}

	add("container", perFile(func(mi transcode.MediaInfo) string { return mi.Container }))
	add("videoCodec", perFile(func(mi transcode.MediaInfo) string { return mi.VideoCodec }))
	add("videoProfile", perFile(func(mi transcode.MediaInfo) string { return mi.VideoProfile }))
	add("resolution", perFile(func(mi transcode.MediaInfo) string {
		if mi.Width > 0 && mi.Height > 0 {
			return fmt.Sprintf("%dx%d", mi.Width, mi.Height)
		}
		return ""
	}))
	add("bitDepth", perFile(func(mi transcode.MediaInfo) string {
		if mi.BitDepth > 0 {
			return strconv.Itoa(mi.BitDepth)
		}
		return ""
	}))
	add("hdr", perFile(func(mi transcode.MediaInfo) string { return mi.HDR }))
	add("frameRate", perFile(func(mi transcode.MediaInfo) string {
		if mi.FrameRate > 0 {
			return formatFrameRate(mi.FrameRate)
		}
		return ""
	}))
	add("audioCodec", perFile(func(mi transcode.MediaInfo) string { return mi.AudioCodec }))
	add("audioChannels", perFile(func(mi transcode.MediaInfo) string { return mi.AudioChannels }))
	add("audioLanguages", acrossStreams(func(mi transcode.MediaInfo) []string { return mi.AudioLanguages }))
	add("subtitleLanguages", acrossStreams(func(mi transcode.MediaInfo) []string { return mi.SubtitleLanguages }))
	add("bitrate", perFile(func(mi transcode.MediaInfo) string {
		if mi.BitRate > 0 {
			return humanBitrate(mi.BitRate)
		}
		return ""
	}))

	var totalSize int64
	for _, mi := range infos {
		totalSize += mi.Size
	}
	if totalSize > 0 {
		kvs = append(kvs, model.KV{Key: "fileSize", Value: humanBytes(totalSize)})
	}
	return kvs
}

// aggregate dedupes vals order-preserving, drops empties, and joins with ", ".
// More than maxDistinct values collapse to the first maxDistinct plus ", N more".
func aggregate(vals []string) string {
	var distinct []string
	seen := map[string]bool{}
	for _, v := range vals {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		distinct = append(distinct, v)
	}
	if len(distinct) == 0 {
		return ""
	}
	if len(distinct) <= maxDistinct {
		return strings.Join(distinct, ", ")
	}
	return strings.Join(distinct[:maxDistinct], ", ") + fmt.Sprintf(", %d more", len(distinct)-maxDistinct)
}

func formatFrameRate(f float64) string {
	s := strconv.FormatFloat(f, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func humanBitrate(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d bit/s", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cbit/s", float64(n)/float64(div), "kMGT"[exp])
}

// planAndApply is shared by the plex and jellyfin commands: it prints a
// new/existing plan for the catalog, then (unless --dry-run) confirms and
// applies it. The command's flags (dry-run/yes/force/no-posters) drive it.
func planAndApply(c *cli.Context, items []importer.Media, cfg *config.Config) error {
	dataDir := cfg.DataDir
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
	tech := technicalProvider(cfg)
	copied, skipped, failed, imported := 0, 0, 0, 0
	for i, p := range plans {
		label := fmt.Sprintf("[%d/%d] %s / (%d) %s", i+1, len(plans), p.m.Category, p.m.Year, p.m.Title)
		multi := len(p.m.Files) > 1
		switch {
		case prog == nil:
			// No TTY: no bars will draw, so print one static line per item.
			fmt.Println(label)
		case multi:
			// A multi-file folder gets a bold header; its files are numbered below.
			boldLine(label, color)
		}
		_, stats, err := p.m.Apply(dataDir, c.Bool("force"), !c.Bool("no-posters"), prog, tech, i+1, len(plans))
		if err != nil {
			fmt.Printf("  error: %v\n", err)
			failed++
			continue
		}
		// A single-file folder whose file was skipped draws no bar, so note it here.
		if prog != nil && !multi && stats.Copied == 0 {
			fmt.Printf("%s  unchanged\n", label)
		}
		if stats.Copied > 0 {
			imported++
		}
		copied += stats.Copied
		skipped += stats.Skipped
	}
	fmt.Printf("Done: %d file(s) copied, %d unchanged, %d item(s) failed.\n", copied, skipped, failed)
	fmt.Printf("Run `%s rebuild` to update the cache.\n", config.AppName)
	// One summary event after the interactive output, so it never tangles with the bars.
	lg, closeLog := openLogger(cfg)
	defer closeLog()
	lg.For(c.Command.Name).Info(fmt.Sprintf("imported %d of %d item(s) from %s", imported, len(plans), c.Command.Name),
		logging.Fields{"imported": imported, "files_copied": copied, "skipped": skipped, "failed": failed})
	return nil
}

// boldLine prints s in bold on a TTY, or plainly otherwise.
func boldLine(s string, color bool) {
	if color {
		fmt.Printf("\033[1m%s\033[0m\n", s)
	} else {
		fmt.Println(s)
	}
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
