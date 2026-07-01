package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"filefin/internal/config"
	"filefin/internal/db"
	"filefin/internal/logging"
	"filefin/internal/optimize"
	"filefin/internal/sysload"
	"filefin/internal/transcode"
)

// Optimizer worker labels, shown on the Progress page and recorded as the claiming agent
// on each task: the always-on GPU agent(s) and the load-gated CPU pool. With more than one
// GPU each worker's label is suffixed with its device (see gpuLabel).
const (
	workerGPU = "GPU"
	workerCPU = "CPU"
)

// handleActiveOptimize returns the in-flight encodes (with live percent overlaid from
// memory) and the count still waiting, for the Progress page.
func (s *Server) handleActiveOptimize(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	active, err := db.ListActiveTasks(ctx, pool)
	if err != nil {
		http.Error(w, "could not list optimize tasks", http.StatusInternalServerError)
		return
	}
	s.optMu.Lock()
	for i := range active {
		if p, ok := s.optPercent[active[i].ID]; ok {
			active[i].Percent = p
		}
	}
	s.optMu.Unlock()
	pending, err := db.CountPending(ctx, pool)
	if err != nil {
		http.Error(w, "could not count optimize tasks", http.StatusInternalServerError)
		return
	}
	writeJSON(w, queueStatus[db.ActiveTask]{Active: active, Pending: pending})
}

const (
	optimizeRampInterval = 5 * time.Second // between CPU autoscaler decisions
	optimizeBusyPoll     = 2 * time.Second // re-check interval while yielding to a viewer
	optimizeTargetLoad   = 80              // add a CPU worker only below this CPU busy%
)

// olog returns the optimizer-scoped logger.
func (s *Server) olog() *logging.Scoped { return s.logger().For(logging.Optimizer) }

func (s *Server) setOptPercent(id int64, pct int) {
	s.optMu.Lock()
	s.optPercent[id] = pct
	s.optMu.Unlock()
}

func (s *Server) clearOptPercent(id int64) {
	s.optMu.Lock()
	delete(s.optPercent, id)
	s.optMu.Unlock()
}

// startOptimizer launches the supervisor goroutine once per process.
func (s *Server) startOptimizer() {
	s.optimizeStart.Do(func() { go s.optimizeSupervisor() })
}

// signalReconfigOpt asks the supervisor to re-read the mode and relaunch (coalesced).
func (s *Server) signalReconfigOpt() {
	select {
	case s.reconfigOpt <- struct{}{}:
	default:
	}
}

// optimizeSupervisor owns the optimizer's lifecycle. On each reconfig signal it cancels
// the previous run, waits for its goroutines to fully exit, then starts a fresh run from
// the current config.
func (s *Server) optimizeSupervisor() {
	var (
		cancel context.CancelFunc
		wg     *sync.WaitGroup
	)
	for range s.reconfigOpt {
		if cancel != nil {
			cancel()
			wg.Wait()
		}
		ctx, c := context.WithCancel(context.Background())
		cancel = c
		w := &sync.WaitGroup{}
		wg = w
		s.startOptimizeRun(ctx, w)
	}
}

// startOptimizeRun performs crash recovery (sweep stale .tmp locks, re-queue orphaned
// encoding rows) and, when the mode allows, launches the planner and agents under ctx.
func (s *Server) startOptimizeRun(ctx context.Context, wg *sync.WaitGroup) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg == nil || !cfg.SetupComplete() {
		return
	}

	// Recovery runs after the prior run's goroutines have exited (the supervisor waited),
	// so no live lock or in-flight row is touched.
	if n, err := optimize.SweepStaleLocks(cfg.DataDir); err == nil && n > 0 {
		s.olog().Info(fmt.Sprintf("cleared %d stale optimizer lock(s)", n))
	}
	if pool, err := s.ensureDB(ctx); err == nil {
		_ = db.ResetEncodingToPending(ctx, pool)
	}

	mode := cfg.OptimizeModeOr()
	if mode == config.OptimizeNone {
		return
	}

	// The queue is filled by the scan (the "Optimizer scan" button or the discovery agent,
	// see discovery.go); the agents below just drain whatever it holds.
	if mode == config.OptimizeGPU || mode == config.OptimizeAll {
		encs := s.optimizeGPUEncoders(ctx)
		for _, enc := range encs {
			wg.Add(1)
			go s.optimizeGPUAgent(ctx, gpuLabel(enc, len(encs)), enc, wg)
		}
		s.olog().Info("optimizer started", logging.Fields{"mode": mode, "gpus": len(encs), "devices": gpuDevices(encs)})
	}
	if mode == config.OptimizeCPU || mode == config.OptimizeAll {
		wg.Add(1)
		go s.optimizeCPUPool(ctx, wg)
		if mode == config.OptimizeCPU {
			s.olog().Info("optimizer started", logging.Fields{"mode": mode})
		}
	}
}

// optimizeGPUEncoders detects every usable GPU encoder (one per render node that can
// encode, vaapi; else a single software encoder) so a multi-GPU host runs one always-on
// worker per card. The result always has at least one element.
func (s *Server) optimizeGPUEncoders(ctx context.Context) []transcode.Encoder {
	s.mu.RLock()
	cfg, lg := s.cfg, s.lg
	s.mu.RUnlock()
	opts := transcode.Options{Logger: lg}
	if cfg != nil {
		opts.FFmpegPath, opts.FFprobePath = cfg.FFmpeg(), cfg.FFprobe()
	}
	return transcode.DetectEncoders(ctx, opts)
}

// gpuLabel is the claiming-agent label for a GPU worker, shown on the Progress page. With a
// single GPU it is the plain "GPU"; with several it is suffixed with the device base name
// (e.g. "GPU:renderD128") so concurrent workers are distinguishable.
func gpuLabel(enc transcode.Encoder, total int) string {
	if total <= 1 || enc.Device == "" {
		return workerGPU
	}
	return workerGPU + ":" + filepath.Base(enc.Device)
}

// gpuDevices lists the device path (or encoder kind, for the software fallback) of each
// encoder, for the "optimizer started" log line.
func gpuDevices(encs []transcode.Encoder) []string {
	out := make([]string, len(encs))
	for i, e := range encs {
		if e.Device != "" {
			out[i] = e.Device
		} else {
			out[i] = string(e.Kind)
		}
	}
	return out
}

// handleOptimizeScan is the manual queue refill: it walks the cached media files, queues
// a task for every source that needs a direct-play copy and lacks a fresh one, prunes
// stale rows, and reports how many candidates were found and how many are now pending. The
// discovery agent (discovery.go) runs the same refill on a timer.
func (s *Server) handleOptimizeScan(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	candidates, err := s.optimizeRefill(ctx)
	if err != nil {
		http.Error(w, "could not scan for optimize work", http.StatusInternalServerError)
		return
	}
	pending, err := db.CountPending(ctx, pool)
	if err != nil {
		http.Error(w, "could not count optimize tasks", http.StatusInternalServerError)
		return
	}
	s.olog().Info("optimizer scan queued work", logging.Fields{"candidates": candidates, "pending": pending})
	writeJSON(w, scanResult{Candidates: candidates, Pending: pending})
}

// optimizeRefill upserts a pending task per candidate and prunes pending/error tasks for
// files that now have a fresh sibling or are browser-native. It returns the candidate
// count.
func (s *Server) optimizeRefill(ctx context.Context) (int, error) {
	pool, err := s.ensureDB(ctx)
	if err != nil {
		return 0, err
	}
	files, err := db.AllFiles(ctx, pool)
	if err != nil {
		return 0, err
	}
	cands := optimize.Candidates(files)
	wanted := make(map[string]bool, len(cands))
	failed := 0
	for _, c := range cands {
		if err := db.UpsertPendingTask(ctx, pool, c.MediaID, c.FileIdx, c.Source, c.Optimized); err != nil {
			failed++
			continue
		}
		wanted[c.MediaID+"|"+strconv.Itoa(c.FileIdx)] = true
	}
	for _, f := range files {
		if !wanted[f.MediaID+"|"+strconv.Itoa(f.Idx)] {
			s.bestEffort(db.PruneTask(ctx, pool, f.MediaID, f.Idx), "prune optimize task")
		}
	}
	if failed > 0 {
		s.olog().Error("some optimize tasks could not be queued", logging.Fields{"failed": failed})
	}
	return len(cands), nil
}

// optimizeGPUAgent is an always-on worker bound to one GPU encoder: it grabs the next
// pending task the instant it finishes and idles between empties, until ctx is cancelled.
// label identifies the worker (and the GPU it runs on) on the Progress page.
func (s *Server) optimizeGPUAgent(ctx context.Context, label string, enc transcode.Encoder, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}
		if !s.optimizeOnce(ctx, label, enc) {
			if !sleepCtx(ctx, optimizeBusyPoll) {
				return
			}
		}
	}
}

// optimizeCPUPool is the CPU autoscaler: it only ever *adds* software workers, one per
// ramp interval, while there is pending work, no live viewer, CPU headroom, and fewer
// than NumCPU workers. Each worker drains until the queue empties and exits; the pool
// re-adds on the next refill. Workers are never killed mid-flight (a load spike cannot
// thrash a partial encode).
func (s *Server) optimizeCPUPool(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	maxWorkers := runtime.NumCPU()
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	enc := transcode.SoftwareEncoder()
	var active atomic.Int64
	cpu := sysload.NewCPUSampler()
	cpu.Percent() // prime the baseline; the next reading covers one ramp interval
	ticker := time.NewTicker(optimizeRampInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pool, err := s.ensureDB(ctx)
		if err != nil {
			continue
		}
		pending, err := db.CountPending(ctx, pool)
		if err != nil || pending == 0 {
			continue
		}
		cpuPct, ok := cpu.Percent()
		if s.optimizeBusy() || active.Load() >= int64(maxWorkers) || (ok && cpuPct >= optimizeTargetLoad) {
			continue
		}
		active.Add(1)
		wg.Add(1)
		go func() {
			defer func() {
				active.Add(-1)
				wg.Done()
			}()
			for s.optimizeOnce(ctx, workerCPU, enc) {
				if ctx.Err() != nil {
					return
				}
			}
		}()
		s.olog().Debug("optimizer adding a cpu worker", logging.Fields{"workers": active.Load(), "cpu": cpuPct})
	}
}

// optimizeOnce yields to any live viewer, then claims and processes a single pending task
// on enc. It returns true if it processed one, false when none was available (or ctx is
// done).
func (s *Server) optimizeOnce(ctx context.Context, label string, enc transcode.Encoder) bool {
	s.optimizeWaitWhileBusy(ctx)
	if ctx.Err() != nil {
		return false
	}
	pool, err := s.ensureDB(ctx)
	if err != nil {
		return false
	}
	task, ok, err := db.ClaimNextTask(ctx, pool, label)
	if err != nil || !ok {
		return false
	}
	s.runOptimizeTask(ctx, pool, task, enc)
	return true
}

// runOptimizeTask encodes one claimed task to a direct-play copy: it takes the per-item
// .tmp lock (O_EXCL), probes the source, skips remux-eligible ones, encodes with live
// progress, then atomic-renames into place. A cancellation leaves the row encoding for
// the next run's recovery to re-queue.
func (s *Server) runOptimizeTask(ctx context.Context, pool *sql.DB, task db.OptimizeTask, enc transcode.Encoder) {
	tmp := task.OptimizedPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return // another owner holds the lock; recovery will re-queue if it dies
		}
		_ = db.FailTask(ctx, pool, task.ID, err.Error())
		return
	}
	f.Close()
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tmp)
		}
	}()

	film := strings.TrimSuffix(filepath.Base(task.SourcePath), filepath.Ext(task.SourcePath))
	s.mu.RLock()
	ffmpeg, ffprobe := s.cfg.FFmpeg(), s.cfg.FFprobe()
	s.mu.RUnlock()

	streams, err := transcode.Probe(ctx, ffprobe, task.SourcePath)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		_ = db.FailTask(ctx, pool, task.ID, err.Error())
		s.olog().Error("optimize probe failed for "+film, logging.Fields{"film": film, "error": err.Error()})
		return
	}
	if transcode.RemuxEligible(streams) {
		_ = db.FinishTask(ctx, pool, task.ID) // already cheap to serve; no copy needed
		return
	}

	start := time.Now()
	var lastMirror time.Time
	err = optimize.Encode(ctx, optimize.EncodeOptions{
		FFmpeg: ffmpeg, Encoder: enc, Source: task.SourcePath, Output: tmp, Duration: streams.Duration,
		OnProgress: func(pct int) {
			s.setOptPercent(task.ID, pct)
			if now := time.Now(); now.Sub(lastMirror) >= time.Second {
				lastMirror = now
				s.bestEffort(db.UpdateTaskPercent(ctx, pool, task.ID, pct), "optimize percent mirror")
			}
		},
	})
	s.clearOptPercent(task.ID)
	if err != nil {
		if ctx.Err() != nil {
			return // cancelled: leave the row for recovery rather than marking it failed
		}
		_ = db.FailTask(ctx, pool, task.ID, err.Error())
		s.olog().Error("optimize failed for "+film, logging.Fields{"film": film, "error": err.Error()})
		return
	}
	if err := os.Rename(tmp, task.OptimizedPath); err != nil {
		_ = db.FailTask(ctx, pool, task.ID, err.Error())
		return
	}
	renamed = true
	_ = db.FinishTask(ctx, pool, task.ID)
	s.olog().Info(fmt.Sprintf("new optimized version of %s", film), logging.Fields{
		"film": film, "path": task.OptimizedPath, "took_ms": time.Since(start).Milliseconds(),
		"encoder": enc.Kind, "device": enc.Device, "src_codec": streams.VideoCodec,
	})
}

// optimizeBusy reports whether a live transcode session is in progress, so background
// encoding yields to viewers. It never builds the HLS manager (a nil one is not busy).
func (s *Server) optimizeBusy() bool {
	s.mu.RLock()
	m := s.hls
	s.mu.RUnlock()
	return m != nil && m.ActiveSessions() > 0
}

func (s *Server) optimizeWaitWhileBusy(ctx context.Context) {
	for s.optimizeBusy() {
		if !sleepCtx(ctx, optimizeBusyPoll) {
			return
		}
	}
}

// sleepCtx sleeps for d, returning false if ctx is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
