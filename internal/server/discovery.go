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
// checked batch, so a large library is processed as a continuous trickle. A tick is skipped
// outright while any sweep is still in flight.
func (s *Server) discoveryTick(ctx context.Context) {
	if !s.sweepJob.begin(sweepRolling, discoveryBatch) {
		return
	}
	s.sweep(ctx, discoveryBatch)
}

// sweep reconciles the cache against disk, refills the three work queues, and runs the
// health pass over the limit least-recently-checked items (all of them when limit is 0).
// It holds the maintenance lock so it never races a rebuild. The caller must already have
// claimed s.sweepJob, which both guards against a second sweep and carries the live
// progress the Progress page polls - a timed tick is reported exactly like a forced one,
// because a tick doing subtitle repair can run for minutes and an admin needs to see why
// the "Full health sweep" button is refusing to start.
func (s *Server) sweep(ctx context.Context, limit int) {
	defer s.sweepJob.done()

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
	if _, err := s.refillTMDb(ctx, pool); err != nil {
		s.dlog().Error("discovery TMDb refill failed", logging.Fields{"error": err.Error()})
	}
	if _, err := s.refillPeople(ctx, pool); err != nil {
		s.dlog().Error("discovery people refill failed", logging.Fields{"error": err.Error()})
	}

	// Health pass: process the least-recently-checked items, a batch at a time on the timer
	// so a large library is swept as a continuous trickle, or all of them when forced.
	ids, err := db.OldestUncheckedMedia(ctx, pool, limit)
	if err != nil {
		return
	}
	s.sweepJob.setTotal(len(ids)) // the claim's estimate, now that the real count is known
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
		s.sweepJob.advance(repaired)
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
	if !s.sweepJob.begin(sweepFull, len(refs)) {
		// The timed agent holds the same guard, and its own rolling batch can run for
		// minutes once it has subtitles to extract, so say what is running rather than
		// just refusing.
		http.Error(w, "a "+s.sweepJob.snapshot().Scope+" sweep is already running; watch it on the Progress page",
			http.StatusConflict)
		return
	}
	go s.sweep(context.Background(), 0)
	writeJSON(w, s.sweepJob.snapshot())
}

// handleFullSweepProgress returns the live full-sweep progress for the polling page.
func (s *Server) handleFullSweepProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.sweepJob.snapshot())
}

// The two scopes a sweep can have, as the Progress page labels them.
const (
	sweepRolling = "rolling batch"
	sweepFull    = "whole library"
)

// sweepState is the live progress of whichever sweep is running, timed or forced.
// Subtitles counts the sidecars its repair has written. Running is both the guard against a
// second sweep and what tells the Progress page, which has no local flag of its own, whether
// the snapshot describes a live sweep or the last finished one.
type sweepState struct {
	Scope     string `json:"scope"`
	Total     int    `json:"total"`
	Done      int    `json:"done"`
	Subtitles int    `json:"subtitles"`
	Running   bool   `json:"running"`
	Finished  bool   `json:"finished"`
	Error     string `json:"error"`
}

// sweepTracker owns the sweep progress behind its own mutex (the rebuildTracker pattern).
type sweepTracker struct {
	mu sync.Mutex
	st sweepState
}

// begin claims the tracker for a fresh sweep of the given scope, with a first estimate of
// its item count; ok is false when a sweep is already in flight, which is what makes this
// one guard for the timer and the button alike.
func (t *sweepTracker) begin(scope string, total int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.st.Running {
		return false
	}
	t.st = sweepState{Scope: scope, Running: true, Total: total}
	return true
}

// setTotal replaces the estimate begin was given with the real item count.
func (t *sweepTracker) setTotal(total int) {
	t.mu.Lock()
	t.st.Total = total
	t.mu.Unlock()
}

func (t *sweepTracker) advance(subtitles int) {
	t.mu.Lock()
	t.st.Done++
	t.st.Subtitles = subtitles
	t.mu.Unlock()
}

// done closes the sweep however it ended. Every exit from sweep runs it, so a sweep that
// returns early never leaves the guard claimed.
func (t *sweepTracker) done() {
	t.mu.Lock()
	t.st.Finished, t.st.Running = true, false
	t.mu.Unlock()
}

func (t *sweepTracker) snapshot() sweepState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.st
}
