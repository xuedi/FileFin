package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"filefin/internal/db"
	"filefin/internal/logging"
)

const (
	discoveryBatch  = 64              // items fully checked per tick (the rolling trickle)
	discoveryWarmup = 5 * time.Second // re-poll for install + cache before the first tick
)

// dlog returns the discovery-scoped logger.
func (s *Server) dlog() *logging.Scoped { return s.logger().For(logging.Discovery) }

// startDiscovery launches the discovery supervisor goroutine once per process.
func (s *Server) startDiscovery() {
	s.discoveryStart.Do(func() { go s.discoverySupervisor() })
}

// signalReconfigDisc asks the supervisor to re-read the interval and relaunch (coalesced).
func (s *Server) signalReconfigDisc() {
	select {
	case s.reconfigDisc <- struct{}{}:
	default:
	}
}

// discoveryInterval returns the configured sweep interval in seconds (0 = off) under lock.
func (s *Server) discoveryInterval() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return 0
	}
	return s.cfg.DiscoveryInterval
}

// discoverySupervisor owns the agent's lifecycle, mirroring the optimizer: on each reconfig
// signal it cancels the previous run, waits for it to exit, then starts a fresh run from
// the current interval. An interval of 0 leaves it idle until the next signal.
func (s *Server) discoverySupervisor() {
	var (
		cancel context.CancelFunc
		wg     *sync.WaitGroup
	)
	for range s.reconfigDisc {
		if cancel != nil {
			cancel()
			wg.Wait()
		}
		ctx, c := context.WithCancel(context.Background())
		cancel = c
		w := &sync.WaitGroup{}
		wg = w
		s.startDiscoveryRun(ctx, w)
	}
}

// setDiscNextRun records when the next scheduled sweep is due (unix seconds; 0 = off).
func (s *Server) setDiscNextRun(unix int64) {
	s.discMu.Lock()
	s.discNextRun = unix
	s.discMu.Unlock()
}

// startDiscoveryRun launches the ticking agent under ctx when an interval is configured.
func (s *Server) startDiscoveryRun(ctx context.Context, wg *sync.WaitGroup) {
	interval := s.discoveryInterval()
	if interval <= 0 {
		s.setDiscNextRun(0) // discovery turned off: clear the countdown
		return
	}
	wg.Add(1)
	go s.discoveryLoop(ctx, time.Duration(interval)*time.Second, wg)
}

// discoveryLoop waits for the install + cache to be ready, then sweeps every interval until
// ctx is cancelled.
func (s *Server) discoveryLoop(ctx context.Context, interval time.Duration, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		s.mu.RLock()
		installed := s.cfg != nil && s.cfg.SetupComplete()
		s.mu.RUnlock()
		if installed {
			if _, err := s.ensureDB(ctx); err == nil {
				break
			}
		}
		if !sleepCtx(ctx, discoveryWarmup) {
			return
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	s.setDiscNextRun(time.Now().Add(interval).Unix())
	s.dlog().Info("discovery agent started", logging.Fields{"interval_s": int(interval.Seconds())})
	// One reconcile right away so a freshly opened or externally emptied cache is brought
	// current at startup, rather than showing an empty library until the first full interval.
	s.discoveryTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.discoveryTick(ctx)
			s.setDiscNextRun(time.Now().Add(interval).Unix())
		}
	}
}

// discoveryTick is one timed sweep: the rolling health pass covers the least-recently-
// checked batch, so a large library is processed as a continuous trickle.
func (s *Server) discoveryTick(ctx context.Context) { s.sweep(ctx, discoveryBatch, nil) }

// sweep reconciles the cache against disk, refills the three work queues, and runs the
// health pass over the limit least-recently-checked items (all of them when limit is 0).
// It holds the maintenance lock so it never races a rebuild, and a running guard skips the
// sweep when the previous one is still in flight. job, when set, carries live progress for
// the forced full sweep.
func (s *Server) sweep(ctx context.Context, limit int, job *sweepTracker) {
	if job != nil {
		defer job.done()
	}
	s.discMu.Lock()
	if s.discRunning {
		s.discMu.Unlock()
		if job != nil {
			job.fail("a sweep is already running")
		}
		return
	}
	s.discRunning = true
	s.discMu.Unlock()
	defer func() {
		s.discMu.Lock()
		s.discRunning = false
		s.discMu.Unlock()
	}()

	s.maintMu.Lock()
	defer s.maintMu.Unlock()

	pool, err := s.ensureDB(ctx)
	if err != nil {
		return
	}
	dataDir := s.dataDir()
	if dataDir == "" {
		return
	}

	refs, err := onDiskMediaRefs(dataDir)
	if err != nil {
		s.dlog().Error("discovery could not read data folder", logging.Fields{"error": err.Error()})
		return
	}
	added, removed := s.reconcileDiff(ctx, pool, dataDir, refs)

	// Refill the three work queues for the whole library; each is idempotent, so this
	// coexists with the manual scan buttons.
	if _, err := s.optimizeRefill(ctx); err != nil {
		s.dlog().Error("discovery optimize refill failed", logging.Fields{"error": err.Error()})
	}
	if _, err := s.refillEnrich(ctx, pool); err != nil {
		s.dlog().Error("discovery enrich refill failed", logging.Fields{"error": err.Error()})
	}
	// Self-heal past failures: re-queue error tasks last tried longer ago than the retry
	// interval, so a transient OMDb miss is retried without a full rebuild. Discovery-only;
	// the manual scan stays never-enriched-only.
	cutoff := time.Now().Unix() - int64(enrichRetryInterval.Seconds())
	if n, err := db.RequeueStaleEnrichErrors(ctx, pool, cutoff); err != nil {
		s.dlog().Error("discovery enrich retry failed", logging.Fields{"error": err.Error()})
	} else if n > 0 {
		s.dlog().Info("discovery re-queued stale enrich failures", logging.Fields{"count": n})
	}
	if _, err := s.refillThumbnail(ctx, pool); err != nil {
		s.dlog().Error("discovery thumbnail refill failed", logging.Fields{"error": err.Error()})
	}
	if _, err := s.refillProbe(ctx, pool); err != nil {
		s.dlog().Error("discovery probe refill failed", logging.Fields{"error": err.Error()})
	}

	// Health pass: process the least-recently-checked items, a batch at a time on the timer
	// so a large library is swept as a continuous trickle, or all of them when forced.
	ids, err := db.OldestUncheckedMedia(ctx, pool, limit)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	checked, repaired := 0, 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		ref, ok := refs[id]
		if !ok {
			continue
		}
		repaired += s.reconcileItem(ctx, pool, dataDir, id, ref, now)
		checked++
		if job != nil {
			job.advance(repaired)
		}
	}
	s.discMu.Lock()
	s.discLastSweep = now
	s.discMu.Unlock()
	if added > 0 || removed > 0 || checked > 0 {
		s.dlog().Info("discovery sweep complete",
			logging.Fields{"added": added, "removed": removed, "checked": checked, "subtitles": repaired})
	}
}

// handleRunDiscovery triggers an immediate sweep in the background (the "Run discovery now"
// button). The running guard makes a press during an in-flight sweep a no-op.
func (s *Server) handleRunDiscovery(w http.ResponseWriter, r *http.Request) {
	go s.discoveryTick(context.Background())
	writeJSON(w, struct {
		Started bool `json:"started"`
	}{true})
}

// handleFullSweep runs the health pass over the whole library at once instead of waiting
// out the rolling trickle - the brute-force companion to "force now", and the repair action
// for folders whose embedded subtitles were never externalised. It returns immediately and
// the maintenance page polls handleFullSweepProgress for the bar; the folder count on disk
// is the denominator, so the bar has a total before the sweep starts.
func (s *Server) handleFullSweep(w http.ResponseWriter, r *http.Request) {
	if _, err := s.ensureDB(r.Context()); err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	refs, _ := onDiskMediaRefs(s.dataDir())
	if !s.sweepJob.begin(len(refs)) {
		http.Error(w, "sweep already running", http.StatusConflict)
		return
	}
	go s.sweep(context.Background(), 0, &s.sweepJob)
	writeJSON(w, s.sweepJob.snapshot())
}

// handleFullSweepProgress returns the live full-sweep progress for the polling page.
func (s *Server) handleFullSweepProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.sweepJob.snapshot())
}

// sweepState is a forced full sweep's live progress, polled by the maintenance page.
// Subtitles counts the sidecars extracted along the way. running is not serialized; it
// guards against starting a second full sweep while one is in flight.
type sweepState struct {
	Total     int    `json:"total"`
	Done      int    `json:"done"`
	Subtitles int    `json:"subtitles"`
	Finished  bool   `json:"finished"`
	Error     string `json:"error"`
	running   bool
}

// sweepTracker owns the full-sweep progress behind its own mutex (the rebuildTracker pattern).
type sweepTracker struct {
	mu sync.Mutex
	st sweepState
}

// begin marks a fresh sweep running with its item denominator; ok is false when one is
// already in flight.
func (t *sweepTracker) begin(total int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.st.running {
		return false
	}
	t.st = sweepState{running: true, Total: total}
	return true
}

func (t *sweepTracker) advance(subtitles int) {
	t.mu.Lock()
	t.st.Done++
	t.st.Subtitles = subtitles
	t.mu.Unlock()
}

func (t *sweepTracker) fail(msg string) {
	t.mu.Lock()
	t.st.Error = msg
	t.mu.Unlock()
}

// done closes the sweep however it ended, keeping any error already recorded. Every exit
// from sweep runs it, so a sweep that returns early never leaves the button stuck.
func (t *sweepTracker) done() {
	t.mu.Lock()
	t.st.Finished, t.st.running = true, false
	t.mu.Unlock()
}

func (t *sweepTracker) snapshot() sweepState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.st
}
