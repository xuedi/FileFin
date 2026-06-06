// Package optimize pre-transcodes non-browser-native media into direct-play
// `<source-base>.optimized.mp4` copies stored beside the source, so playback needs no
// per-play transcoding. It is the only writer into the data directory outside setup and
// the importers, and runs only when explicitly enabled.
package optimize

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"filefin/internal/logging"
	"filefin/internal/model"
	"filefin/internal/scanner"
	"filefin/internal/sysload"
	"filefin/internal/transcode"
)

const (
	idleRescan = 5 * time.Minute // wait between full passes once the backlog is drained
	busyPoll   = 2 * time.Second // re-check interval while yielding to a live transcode

	rampInterval      = 5 * time.Second // between autoscaler decisions (let a new worker ramp up)
	defaultTargetLoad = 80              // add a CPU worker only while CPU busy% is below this
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

// SweepStaleLocks removes optimizer in-progress temp files (the per-item locks) left
// under dataDir by a crashed run, returning how many it cleared. serve calls it once at
// startup, before any worker runs, so it never removes a live lock.
func SweepStaleLocks(dataDir string) (int, error) {
	n := 0
	err := filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), transcode.OptimizedTmpSuffix) {
			if os.Remove(path) == nil {
				n++
			}
		}
		return nil
	})
	return n, err
}

// Options configures a Worker.
type Options struct {
	DataDir string
	FFmpeg  string
	FFprobe string
	// Encoder is used by the single always-on worker (the GPU encoder when one was
	// detected, else software).
	Encoder transcode.Encoder
	// CPUEncoder is used by the additional CPU-only workers (software/libx264). When
	// zero it falls back to Encoder.
	CPUEncoder transcode.Encoder
	// MaxWorkers caps total concurrent encodes (the always-on worker plus CPU workers).
	// <=1 keeps the optimizer single-threaded (just the always-on worker).
	MaxWorkers int
	// TargetLoad is the CPU busy percentage below which CPU workers may be added; <=0
	// falls back to defaultTargetLoad.
	TargetLoad int
	// Busy reports whether a live transcode is in progress; the worker yields while true
	// so a viewer always gets priority. Nil means never busy.
	Busy func() bool
	// Logger receives optimizer events; may be nil.
	Logger *logging.Logger
}

// Worker grinds the optimize backlog with an adaptive pool of encode workers, sized by
// machine load (see runPool).
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

// RunOnce scans once and processes every pending candidate with the adaptive pool,
// returning when the backlog is drained or ctx is cancelled.
func (w *Worker) RunOnce(ctx context.Context) error {
	scan, err := scanner.Scan(w.opts.DataDir)
	if err != nil {
		return err
	}
	work := WorkList(scan)
	if len(work) > 0 {
		w.runPool(ctx, work)
	}
	return ctx.Err()
}

// runPool runs one always-on worker on opts.Encoder (the GPU when present), which keeps
// pulling and encoding the next file the instant it finishes, plus CPU-only workers on
// opts.CPUEncoder that a manager only ever *adds*: every rampInterval it starts one more
// while CPU is below TargetLoad (and work remains, no live viewer, and total workers are
// under MaxWorkers). Workers are never killed mid-flight - they run until the queue
// drains - so a brief load spike can never spawn-then-instantly-terminate a worker. Each
// candidate is delivered to exactly one worker via the shared channel, so no two ever
// encode the same file.
func (w *Worker) runPool(ctx context.Context, work []Candidate) {
	maxWorkers := w.opts.MaxWorkers
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	target := w.opts.TargetLoad
	if target <= 0 {
		target = defaultTargetLoad
	}
	cpuEnc := w.opts.CPUEncoder
	if cpuEnc.Kind == "" {
		cpuEnc = w.opts.Encoder
	}

	ch := make(chan Candidate, len(work))
	for _, c := range work {
		ch <- c
	}
	close(ch)

	var (
		wg     sync.WaitGroup
		active atomic.Int64
		exited = make(chan struct{}, maxWorkers+1)
	)

	// runWorker starts one worker on enc; it pulls candidates until the queue drains or
	// ctx is cancelled, then exits. Workers are only ever added, never asked to stop early.
	runWorker := func(enc transcode.Encoder) {
		active.Add(1)
		wg.Add(1)
		go func() {
			defer func() {
				active.Add(-1)
				wg.Done()
				select {
				case exited <- struct{}{}:
				default:
				}
			}()
			for {
				if ctx.Err() != nil {
					return
				}
				w.waitWhileBusy(ctx)
				if ctx.Err() != nil {
					return
				}
				c, ok := <-ch
				if !ok {
					return
				}
				_ = w.processOne(ctx, c, enc)
			}
		}()
	}

	runWorker(w.opts.Encoder) // the always-on (GPU) worker

	cpu := sysload.NewCPUSampler()
	cpu.Percent() // prime the baseline; the next reading covers one rampInterval
	ticker := time.NewTicker(rampInterval)
	defer ticker.Stop()

	for active.Load() > 0 {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-exited:
			// a worker drained and left; the loop condition re-checks the active count
		case <-ticker.C:
			if ctx.Err() != nil || len(ch) == 0 {
				continue
			}
			cpuPct, ok := cpu.Percent()
			busy := w.opts.Busy != nil && w.opts.Busy()
			if (!ok || cpuPct < target) && !busy && active.Load() < int64(maxWorkers) {
				runWorker(cpuEnc)
				w.log.Debug("optimizer adding a cpu worker", logging.Fields{"workers": active.Load(), "cpu": cpuPct})
			}
		}
	}
	wg.Wait()
}

// processOne claims the candidate (its temp file doubles as a per-item lock), probes the
// source and, if it genuinely needs re-encoding, transcodes it to the optimized copy via
// that temp file and an atomic rename, using enc (GPU for the always-on worker, software
// for CPU workers). A candidate already claimed by another worker or process is skipped.
// Remux-eligible sources are skipped silently (already cheap to serve).
func (w *Worker) processOne(ctx context.Context, c Candidate, enc transcode.Encoder) error {
	tmp := c.Optimized + ".tmp"
	// Atomic claim: O_EXCL fails if the lock already exists (another worker/process owns
	// this file). The empty file is overwritten by ffmpeg's -y.
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	f.Close()
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tmp) // release the lock on any non-success path
		}
	}()

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
	args := transcode.OptimizeArgs(enc, c.Source, tmp)
	out, err := exec.CommandContext(ctx, w.opts.FFmpeg, args...).CombinedOutput()
	if err != nil {
		w.log.Error("optimize failed for "+film, logging.Fields{"film": film, "error": lastLine(out)})
		return fmt.Errorf("ffmpeg: %w: %s", err, lastLine(out))
	}
	if err := os.Rename(tmp, c.Optimized); err != nil {
		return err
	}
	renamed = true
	outInfo, _ := os.Stat(c.Optimized)
	w.log.Info(fmt.Sprintf("new optimized version of %s in %s", film, c.Optimized), logging.Fields{
		"film": film, "path": c.Optimized, "took_ms": time.Since(start).Milliseconds(),
		"encoder": enc.Kind, "device": enc.Device,
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
