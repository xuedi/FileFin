package server

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"filefin/internal/db"
	"filefin/internal/ffprobe"
	"filefin/internal/importer"
	"filefin/internal/logging"
)

const (
	probeAgentName    = "PROBE"
	probeRestInterval = 200 * time.Millisecond // brief pause between local probes
	probeIdlePoll     = 5 * time.Second        // re-check interval when the queue is empty
)

// plog returns the probe-scoped logger.
func (s *Server) plog() *logging.Scoped { return s.logger().For(logging.Probe) }

// probeFFprobe returns the configured ffprobe binary path under lock.
func (s *Server) probeFFprobe() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return ""
	}
	return s.cfg.FFprobe()
}

// handleProbeScan is the manual queue refill: it queues a probe task for every media item
// whose cache format columns are missing or whose meta.json technical block is incomplete,
// prunes tasks for items now complete, and reports the candidate and pending counts.
func (s *Server) handleProbeScan(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	queued, err := s.refillProbe(ctx, pool)
	if err != nil {
		http.Error(w, "could not scan for probe work", http.StatusInternalServerError)
		return
	}
	pending, err := db.CountPendingProbe(ctx, pool)
	if err != nil {
		http.Error(w, "could not count probe tasks", http.StatusInternalServerError)
		return
	}
	s.plog().Info("probe scan queued work", logging.Fields{"candidates": queued, "pending": pending})
	writeJSON(w, scanResult{Candidates: queued, Pending: pending})
}

// handleActiveProbe returns the in-flight probes and the count still waiting, for the
// Progress page.
func (s *Server) handleActiveProbe(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	active, err := db.ListActiveProbe(ctx, pool)
	if err != nil {
		http.Error(w, "could not list probe tasks", http.StatusInternalServerError)
		return
	}
	pending, err := db.CountPendingProbe(ctx, pool)
	if err != nil {
		http.Error(w, "could not count probe tasks", http.StatusInternalServerError)
		return
	}
	writeJSON(w, queueStatus[db.ActiveProbe]{Active: active, Pending: pending})
}

// startProbeAgent launches the single probe agent once per process.
func (s *Server) startProbeAgent() {
	s.probeStart.Do(func() { go s.probeLoop() })
}

// probeLoop drains the probe queue one task at a time, resting briefly between local
// probes and idling when there is no work or no config. A single agent runs for the
// process lifetime; a restart re-queues any interrupted task.
func (s *Server) probeLoop() {
	ctx := context.Background()
	recovered := false
	for {
		s.mu.RLock()
		installed := s.cfg != nil
		s.mu.RUnlock()
		if !installed {
			time.Sleep(probeIdlePoll)
			continue
		}
		pool, err := s.ensureDB(ctx)
		if err != nil {
			time.Sleep(probeIdlePoll)
			continue
		}
		if !recovered {
			_ = db.ResetProbingToPending(ctx, pool) // retry tasks interrupted by a restart
			recovered = true
		}
		task, ok, err := db.ClaimNextProbe(ctx, pool, probeAgentName)
		if err != nil || !ok {
			time.Sleep(probeIdlePoll)
			continue
		}
		s.probeOne(ctx, pool, task)
		time.Sleep(probeRestInterval)
	}
}

// probeOne re-probes every file of one media folder, writing the true format onto each
// cache file row and refreshing the folder's meta.json technical block (from the first
// successfully probed file) through the shared per-folder lock so a playback-state write
// cannot race it. A folder whose files all fail to probe fails the task, left visible to
// the admin. A missing meta.json is never fabricated - that is the health agent's concern.
func (s *Server) probeOne(ctx context.Context, pool *sql.DB, task db.ProbeTask) {
	m, err := db.GetMedia(ctx, pool, task.MediaID)
	if err != nil {
		_ = db.FailProbe(ctx, pool, task.ID, "media not found")
		return
	}
	files, err := db.MediaFiles(ctx, pool, task.MediaID)
	if err != nil || len(files) == 0 {
		_ = db.FailProbe(ctx, pool, task.ID, "no files to probe")
		return
	}

	ffprobeBin := s.probeFFprobe()
	var first ffprobe.Technical
	probed := 0
	for _, f := range files {
		tech := ffprobe.Probe(ctx, ffprobeBin, f.Path)
		if tech.Empty() {
			continue
		}
		if probed == 0 {
			first = tech
		}
		probed++
		s.bestEffort(db.SetMediaFileFormat(ctx, pool, task.MediaID, f.Idx,
			tech.Container, tech.VideoCodec, tech.AudioCodec), "set media file format")
	}
	if probed == 0 {
		_ = db.FailProbe(ctx, pool, task.ID, "ffprobe produced no result")
		return
	}

	// Refresh the folder-wide technical block, but only when a meta.json already exists so
	// a probe never creates a titleless one.
	if _, err := importer.ReadMeta(m.Path); err == nil {
		if _, err := s.metaMgr.Update(m.Path, func(cur importer.Meta) importer.Meta {
			t := first
			cur.Technical = &t
			return cur
		}); err != nil {
			_ = db.FailProbe(ctx, pool, task.ID, "write meta: "+err.Error())
			return
		}
	}

	_ = db.FinishProbe(ctx, pool, task.ID)
	s.plog().Info("probed "+m.Title, logging.Fields{"id": m.ID, "files": probed})
}
