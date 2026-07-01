package server

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"filefin/internal/db"
	"filefin/internal/logging"
	"filefin/internal/thumbnail"
)

const (
	thumbnailAgentName    = "WEBP"
	thumbnailRestInterval = 200 * time.Millisecond // brief pause between local encodes
	thumbnailIdlePoll     = 5 * time.Second        // re-check interval when the queue is empty
)

// tlog returns the thumbnail-scoped logger.
func (s *Server) tlog() *logging.Scoped { return s.logger().For(logging.Thumbnail) }

// thumbnailFFmpeg returns the configured ffmpeg binary path under lock.
func (s *Server) thumbnailFFmpeg() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return ""
	}
	return s.cfg.FFmpeg()
}

// handleThumbnailScan is the manual queue refill: it walks the cached media and queues a
// task for every folder whose sized posters are missing or stale (or, for an other-media
// folder with no poster, that needs a frame-derived one), prunes tasks for folders now
// complete, and reports the candidate and pending counts.
func (s *Server) handleThumbnailScan(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	candidates, err := s.refillThumbnail(ctx, pool)
	if err != nil {
		http.Error(w, "could not scan for thumbnail work", http.StatusInternalServerError)
		return
	}
	pending, err := db.CountPendingThumbnail(ctx, pool)
	if err != nil {
		http.Error(w, "could not count thumbnail tasks", http.StatusInternalServerError)
		return
	}
	s.tlog().Info("thumbnail scan queued work", logging.Fields{"candidates": candidates, "pending": pending})
	writeJSON(w, scanResult{Candidates: candidates, Pending: pending})
}

// thumbnailCandidate decides whether a media folder needs (re)generation: a folder with a
// base poster is a candidate when either sized variant is missing or older than it; an
// other-media folder with no poster is a candidate (frame-extraction path); a normal
// folder with no poster is skipped (nothing to derive from).
func (s *Server) thumbnailCandidate(m db.MediaPoster, otherMedia bool) bool {
	if m.Poster == "" {
		return otherMedia
	}
	base := filepath.Join(m.Path, m.Poster)
	detail := filepath.Join(m.Path, thumbnail.DetailName())
	tile := filepath.Join(m.Path, thumbnail.TileName())
	return variantStale(base, detail) || variantStale(base, tile)
}

// variantStale reports whether a sized variant is missing or older than its base poster.
func variantStale(base, variant string) bool {
	vi, err := os.Stat(variant)
	if err != nil {
		return true // missing
	}
	bi, err := os.Stat(base)
	if err != nil {
		return false // no base to compare against; leave the variant alone
	}
	return vi.ModTime().Before(bi.ModTime())
}

// handleActiveThumbnail returns the in-flight thumbnail jobs and the count still waiting,
// for the Progress page.
func (s *Server) handleActiveThumbnail(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	active, err := db.ListActiveThumbnail(ctx, pool)
	if err != nil {
		http.Error(w, "could not list thumbnail tasks", http.StatusInternalServerError)
		return
	}
	pending, err := db.CountPendingThumbnail(ctx, pool)
	if err != nil {
		http.Error(w, "could not count thumbnail tasks", http.StatusInternalServerError)
		return
	}
	writeJSON(w, queueStatus[db.ActiveThumbnail]{Active: active, Pending: pending})
}

// startThumbnailAgent launches the single thumbnail agent once per process.
func (s *Server) startThumbnailAgent() {
	s.thumbnailStart.Do(func() { go s.thumbnailLoop() })
}

// thumbnailLoop drains the thumbnail queue one task at a time, resting briefly between
// local encodes and idling when there is no work or no config. A single agent runs for
// the process lifetime; a restart re-queues any interrupted task.
func (s *Server) thumbnailLoop() {
	ctx := context.Background()
	recovered := false
	for {
		s.mu.RLock()
		installed := s.cfg != nil && s.cfg.SetupComplete()
		s.mu.RUnlock()
		if !installed {
			time.Sleep(thumbnailIdlePoll)
			continue
		}
		pool, err := s.ensureDB(ctx)
		if err != nil {
			time.Sleep(thumbnailIdlePoll)
			continue
		}
		if !recovered {
			_ = db.ResetGeneratingToPending(ctx, pool) // retry tasks interrupted by a restart
			recovered = true
		}
		task, ok, err := db.ClaimNextThumbnail(ctx, pool, thumbnailAgentName)
		if err != nil || !ok {
			time.Sleep(thumbnailIdlePoll)
			continue
		}
		s.thumbnailOne(ctx, pool, task)
		time.Sleep(thumbnailRestInterval)
	}
}

// thumbnailOne builds one folder's sized posters. For an other-media folder with no base
// poster it first extracts a cropped video frame as poster.webp (flipping HasPoster true
// via the cache row), then derives the detail and tile variants from the base poster. Any
// ffmpeg failure fails the task, left visible to the admin.
func (s *Server) thumbnailOne(ctx context.Context, pool *sql.DB, task db.ThumbnailTask) {
	ffmpeg := s.thumbnailFFmpeg()
	if ffmpeg == "" {
		_ = db.FailThumbnail(ctx, pool, task.ID, "ffmpeg not configured")
		return
	}
	m, err := db.GetMedia(ctx, pool, task.MediaID)
	if err != nil {
		_ = db.FailThumbnail(ctx, pool, task.ID, "media not found")
		return
	}

	poster := m.Poster
	if poster == "" {
		if !task.OtherMedia {
			_ = db.FinishThumbnail(ctx, pool, task.ID) // nothing to derive from
			return
		}
		files, err := db.MediaFiles(ctx, pool, task.MediaID)
		if err != nil || len(files) == 0 {
			_ = db.FailThumbnail(ctx, pool, task.ID, "no video file for frame poster")
			return
		}
		base := filepath.Join(m.Path, "poster.webp")
		if err := thumbnail.FramePoster(ctx, ffmpeg, files[0].Path, base); err != nil {
			_ = db.FailThumbnail(ctx, pool, task.ID, "frame poster: "+err.Error())
			return
		}
		poster = "poster.webp"
		_ = db.SetMediaPoster(ctx, pool, task.MediaID, poster)
	}

	base := filepath.Join(m.Path, poster)
	if err := thumbnail.Detail(ctx, ffmpeg, base, filepath.Join(m.Path, thumbnail.DetailName())); err != nil {
		_ = db.FailThumbnail(ctx, pool, task.ID, "detail poster: "+err.Error())
		return
	}
	if err := thumbnail.Tile(ctx, ffmpeg, base, filepath.Join(m.Path, thumbnail.TileName())); err != nil {
		_ = db.FailThumbnail(ctx, pool, task.ID, "tile poster: "+err.Error())
		return
	}
	_ = db.FinishThumbnail(ctx, pool, task.ID)
	s.tlog().Info("generated thumbnails for "+m.Title, logging.Fields{"id": m.ID})
}
