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

// discoveryTick is one sweep: reconcile the cache against disk, refill the three work
// queues, and run the rolling health pass over the least-recently-checked items. It holds
// the maintenance lock so it never races a rebuild, and a running guard skips the tick when
// the previous one is still in flight.
func (s *Server) discoveryTick(ctx context.Context) {
	s.discMu.Lock()
	if s.discRunning {
		s.discMu.Unlock()
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

	// Rolling pass: fully process the N least-recently-checked items so a large library is
	// swept as a continuous trickle rather than all at once.
	ids, err := db.OldestUncheckedMedia(ctx, pool, discoveryBatch)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	checked := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		ref, ok := refs[id]
		if !ok {
			continue
		}
		s.reconcileItem(ctx, pool, dataDir, id, ref, now)
		checked++
	}
	s.discMu.Lock()
	s.discLastSweep = now
	s.discMu.Unlock()
	if added > 0 || removed > 0 || checked > 0 {
		s.dlog().Info("discovery sweep complete",
			logging.Fields{"added": added, "removed": removed, "checked": checked})
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
