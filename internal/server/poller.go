package server

import (
	"context"
	"fmt"
	"time"

	"filefin/internal/db"
	"filefin/internal/logging"
)

// progressEntry is a single import's live copy byte counts.
type progressEntry struct {
	copied int64
	total  int64
}

func (s *Server) setProgress(id, copied, total int64) {
	s.progMu.Lock()
	s.progress[id] = progressEntry{copied: copied, total: total}
	s.progMu.Unlock()
}

func (s *Server) clearProgress(id int64) {
	s.progMu.Lock()
	delete(s.progress, id)
	s.progMu.Unlock()
}

// startPoller launches the single background import agent (the poller), once per process.
// Every ~5s, when a config exists, it drains import-status rows one at a time. The Start
// button only flips statuses; the poller picks them up on its next tick.
func (s *Server) startPoller() {
	s.pollerStart.Do(func() {
		go s.pollLoop()
	})
}

func (s *Server) pollLoop() {
	recovered := false
	for {
		time.Sleep(5 * time.Second)
		s.mu.RLock()
		installed := s.cfg != nil
		s.mu.RUnlock()
		if !installed {
			continue
		}
		ctx := context.Background()
		pool, err := s.ensureDB(ctx)
		if err != nil {
			continue
		}
		// Once the cache is first available, requeue any import left mid-copy by a
		// previous run (restart, SIGHUP reload, or crash) so it is not stranded.
		if !recovered {
			recovered = true
			if n, err := db.ResetInterruptedImports(ctx, pool); err == nil && n > 0 {
				s.logger().For(logging.Import).Info(fmt.Sprintf("resumed %d interrupted import(s)", n))
			}
		}
		rows, err := db.ListImports(ctx, pool, db.StatusImport)
		if err != nil {
			continue
		}
		for _, row := range rows {
			s.importOne(ctx, pool, row)
		}
		s.sweepUploadDirs(ctx, pool)
	}
}
