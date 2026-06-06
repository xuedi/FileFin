// Package optimize pre-transcodes non-browser-native media into direct-play
// `<source-base>.optimized.mp4` copies stored beside the source, so playback needs no
// per-play transcoding. It is the only writer into the data directory outside setup and
// the importers, and runs only when explicitly enabled.
package optimize

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"filefin/internal/logging"
	"filefin/internal/model"
	"filefin/internal/scanner"
	"filefin/internal/transcode"
)

const (
	idleRescan = 5 * time.Minute // wait between full passes once the backlog is drained
	busyPoll   = 2 * time.Second // re-check interval while yielding to a live transcode
)

// Candidate is one source file that should be optimized, with the target path of its
// optimized copy.
type Candidate struct {
	Source    string
	Optimized string
}

// WorkList derives the pending work from a scan: source files the browser cannot play
// directly that lack a fresh optimized copy. Remux-eligible files are not filtered here
// (that needs ffprobe) - the worker skips them after probing.
func WorkList(scan *model.Scan) []Candidate {
	var out []Candidate
	for _, cat := range scan.Categories {
		for _, m := range cat.Media {
			for _, f := range m.Files {
				if !transcode.NeedsTranscode(f.Ext) {
					continue
				}
				opt, fresh := transcode.OptimizedSibling(f.Path)
				if fresh {
					continue
				}
				out = append(out, Candidate{Source: f.Path, Optimized: opt})
			}
		}
	}
	return out
}

// Options configures a Worker.
type Options struct {
	DataDir string
	FFmpeg  string
	FFprobe string
	Encoder transcode.Encoder
	// Busy reports whether a live transcode is in progress; the worker yields while true
	// so a viewer always gets priority. Nil means never busy.
	Busy func() bool
	// Logger receives optimizer events; may be nil.
	Logger *logging.Logger
}

// Worker grinds the optimize backlog, one file at a time, with the configured encoder.
type Worker struct {
	opts Options
	log  *logging.Scoped
}

// NewWorker constructs a Worker, defaulting tool paths.
func NewWorker(opts Options) *Worker {
	if opts.FFmpeg == "" {
		opts.FFmpeg = "ffmpeg"
	}
	if opts.FFprobe == "" {
		opts.FFprobe = "ffprobe"
	}
	return &Worker{opts: opts, log: opts.Logger.For(logging.Optimizer)}
}

// Run loops: process the backlog, then sleep and rescan for new work, until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	for {
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("optimize pass failed", logging.Fields{"error": err.Error()})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(idleRescan):
		}
	}
}

// RunOnce scans once and processes every pending candidate, returning when the backlog
// is drained or ctx is cancelled.
func (w *Worker) RunOnce(ctx context.Context) error {
	scan, err := scanner.Scan(w.opts.DataDir)
	if err != nil {
		return err
	}
	for _, c := range WorkList(scan) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.waitWhileBusy(ctx)
		_ = w.processOne(ctx, c)
	}
	return nil
}

// processOne probes the source and, if it genuinely needs re-encoding, transcodes it to
// the optimized copy via a temp file and an atomic rename. Remux-eligible sources are
// skipped silently (already cheap to serve, and they recur every pass).
func (w *Worker) processOne(ctx context.Context, c Candidate) error {
	film := strings.TrimSuffix(filepath.Base(c.Source), filepath.Ext(c.Source))
	streams, err := transcode.Probe(ctx, w.opts.FFprobe, c.Source)
	if err != nil {
		w.log.Error("probe failed for "+film, logging.Fields{"film": film, "error": err.Error()})
		return err
	}
	if transcode.RemuxEligible(streams) {
		return nil
	}
	srcInfo, _ := os.Stat(c.Source)
	start := time.Now()
	tmp := c.Optimized + ".tmp"
	args := transcode.OptimizeArgs(w.opts.Encoder, c.Source, tmp)
	out, err := exec.CommandContext(ctx, w.opts.FFmpeg, args...).CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		w.log.Error("optimize failed for "+film, logging.Fields{"film": film, "error": lastLine(out)})
		return fmt.Errorf("ffmpeg: %w: %s", err, lastLine(out))
	}
	if err := os.Rename(tmp, c.Optimized); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	outInfo, _ := os.Stat(c.Optimized)
	w.log.Info(fmt.Sprintf("new optimized version of %s in %s", film, c.Optimized), logging.Fields{
		"film": film, "path": c.Optimized, "took_ms": time.Since(start).Milliseconds(),
		"encoder": w.opts.Encoder.Kind, "device": w.opts.Encoder.Device,
		"src_codec": streams.VideoCodec, "src_bytes": fileSize(srcInfo), "out_bytes": fileSize(outInfo),
	})
	return nil
}

func fileSize(fi os.FileInfo) int64 {
	if fi == nil {
		return 0
	}
	return fi.Size()
}

// waitWhileBusy blocks until no live transcode is active or ctx is cancelled.
func (w *Worker) waitWhileBusy(ctx context.Context) {
	for w.opts.Busy != nil && w.opts.Busy() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(busyPoll):
		}
	}
}

// lastLine returns the last non-empty line of ffmpeg output for a compact error.
func lastLine(b []byte) string {
	s := strings.TrimRight(string(b), "\r\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
