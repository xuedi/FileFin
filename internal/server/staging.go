package server

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"

	"filefin/internal/db"
	"filefin/internal/logging"
)

// stagingJobState is a single in-flight library staging job's live progress, shared by
// the Plex and Jellyfin sources. running is not serialized; it guards against starting a
// second job while one is active.
type stagingJobState struct {
	Total    int    `json:"total"`
	Done     int    `json:"done"`
	Staged   int    `json:"staged"`
	Missing  int    `json:"missing"`
	Finished bool   `json:"finished"`
	Error    string `json:"error"`
	running  bool
}

// stagingTracker owns one source's staging-job progress behind its own mutex. The Plex
// and Jellyfin sources each hold one; it replaces the per-source mutex + state + advance
// helper that used to be duplicated for each.
type stagingTracker struct {
	mu  sync.Mutex
	job stagingJobState
}

// begin marks a fresh job running; ok is false when one is already in flight.
func (t *stagingTracker) begin() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job.running {
		return false
	}
	t.job = stagingJobState{running: true}
	return true
}

// setTotal records the file-count denominator for the progress poll.
func (t *stagingTracker) setTotal(n int) {
	t.mu.Lock()
	t.job.Total = n
	t.mu.Unlock()
}

// advance records one file processed, staged or not.
func (t *stagingTracker) advance(staged bool) {
	t.mu.Lock()
	t.job.Done++
	if staged {
		t.job.Staged++
	} else {
		t.job.Missing++
	}
	t.mu.Unlock()
}

// fail ends the job with an error message.
func (t *stagingTracker) fail(msg string) {
	t.mu.Lock()
	t.job.Error = msg
	t.job.Finished = true
	t.job.running = false
	t.mu.Unlock()
}

// done ends the job successfully (the counts were accumulated by advance).
func (t *stagingTracker) done() {
	t.mu.Lock()
	t.job.Finished = true
	t.job.running = false
	t.mu.Unlock()
}

// snapshot returns the current progress for the polling frontend.
func (t *stagingTracker) snapshot() stagingJobState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.job
}

// stagedItem is one media file ready to become a preCheck row, independent of which
// source (Plex DB, Jellyfin NFO tree) produced it. The source resolves the category,
// applies any path remap, marshals the meta blob, and gathers subtitles up front; the
// driver only inserts.
type stagedItem struct {
	categoryID int64
	sourcePath string
	title      string
	year       int
	season     int
	episode    int
	subtitles  string // JSON-encoded []importer.Subtitle
	poster     string
	metaBlob   string // marshalled meta.json (stored as api_json)
}

// stageImport inserts a preCheck row per locatable staged item, tracking progress on
// tracker and honouring ctx cancellation between items. Originals are never touched:
// delete_after is forced off, since both library sources import from a collection the
// user keeps. origin records the producer and label names it in the log. A missing source
// file or a dropped insert (which would mean the file silently never imports) is logged
// and counted as not staged rather than reported as a phantom success.
func (s *Server) stageImport(ctx context.Context, pool *sql.DB, tracker *stagingTracker, items []stagedItem, origin, label string) {
	tracker.setTotal(len(items))
	// One batch is under review at a time, so a batch abandoned earlier is replaced rather
	// than mixed into this one - the preCheck page shows what this staging just produced.
	s.bestEffort(db.ClearStagedImports(ctx, pool), "clear staged imports")
	staged, missing := 0, 0
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		if !fileExists(it.sourcePath) {
			missing++
			tracker.advance(false)
			continue
		}
		if _, err := db.InsertImport(ctx, pool, db.Import{
			CategoryID: it.categoryID,
			SourcePath: it.sourcePath, Filename: filepath.Base(it.sourcePath),
			Title: it.title, Year: it.year, Season: it.season, Episode: it.episode,
			Subtitles: it.subtitles, Poster: it.poster,
			APIJSON: it.metaBlob, Origin: origin,
			Status: db.StatusPreCheck, DeleteAfter: false,
		}); err != nil {
			s.logger().For(logging.Import).Error("could not stage "+label+" file for import",
				logging.Fields{"file": it.sourcePath, "error": err.Error()})
			missing++
			tracker.advance(false)
			continue
		}
		staged++
		tracker.advance(true)
	}
	tracker.done()
	s.logger().For(logging.Import).Info(fmt.Sprintf("staged %d %s file(s) for import", staged, label),
		logging.Fields{"staged": staged, "missing": missing})
}
