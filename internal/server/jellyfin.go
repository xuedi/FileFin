package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/jellyfin"
	"filefin/internal/library"
	"filefin/internal/logging"
)

// jellyfinPrepareReq is the staging request, copied into the background goroutine. The
// source is a Jellyfin/Kodi NFO library directory on the server. A target category is
// either selected (CategoryID) or created from Category when Create is set.
type jellyfinPrepareReq struct {
	SourceDir  string `json:"sourceDir"`
	CategoryID int64  `json:"categoryId"`
	Create     bool   `json:"create"`
	Category   string `json:"category"`
}

// handleJellyfinPrepare starts the single background Jellyfin staging job and returns
// immediately. The job walks the NFO library, builds a meta.json blob per item from its
// NFO fields (no OMDb), and writes a preCheck row per video file; the frontend polls
// /jellyfin/progress and redirects to the preCheck page when it finishes.
func (s *Server) handleJellyfinPrepare(w http.ResponseWriter, r *http.Request) {
	var req jellyfinPrepareReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.SourceDir) == "" {
		http.Error(w, "a source folder is required", http.StatusBadRequest)
		return
	}
	if fi, err := os.Stat(req.SourceDir); err != nil || !fi.IsDir() {
		http.Error(w, "source folder must be an existing directory", http.StatusBadRequest)
		return
	}
	if !req.Create && req.CategoryID == 0 {
		http.Error(w, "select a category", http.StatusBadRequest)
		return
	}
	s.jellyfinMu.Lock()
	if s.jellyfinJob.running {
		s.jellyfinMu.Unlock()
		http.Error(w, "a Jellyfin import is already in progress", http.StatusConflict)
		return
	}
	s.jellyfinJob = stagingJobState{running: true}
	s.jellyfinMu.Unlock()

	go s.runJellyfinStaging(req)
	w.WriteHeader(http.StatusAccepted)
}

// handleJellyfinProgress returns the live staging job state for the polling frontend.
func (s *Server) handleJellyfinProgress(w http.ResponseWriter, r *http.Request) {
	s.jellyfinMu.Lock()
	job := s.jellyfinJob
	s.jellyfinMu.Unlock()
	writeJSON(w, job)
}

// runJellyfinStaging is the background staging walk. It resolves the target category
// (creating it from the typed name when asked), scans the NFO library, counts files for
// the progress denominator, then stages a preCheck row per locatable file with the
// item's NFO metadata blob and any sidecar subtitles. Originals are never touched:
// delete_after is forced off, like Plex.
func (s *Server) runJellyfinStaging(req jellyfinPrepareReq) {
	ctx := context.Background()
	finishErr := func(msg string) {
		s.jellyfinMu.Lock()
		s.jellyfinJob.Error = msg
		s.jellyfinJob.Finished = true
		s.jellyfinJob.running = false
		s.jellyfinMu.Unlock()
		s.logger().For(logging.Import).Error("Jellyfin import staging failed", logging.Fields{"error": msg})
	}

	pool, err := s.ensureDB(ctx)
	if err != nil {
		finishErr("cache unavailable")
		return
	}

	var cat library.Category
	if req.Create {
		cat, err = s.createCategoryFromName(ctx, pool, req.Category)
		if err != nil {
			finishErr("could not create category: " + err.Error())
			return
		}
	} else {
		var ok bool
		cat, ok = s.categoryByID(req.CategoryID)
		if !ok {
			finishErr("unknown category")
			return
		}
	}

	items, err := jellyfin.Scan(req.SourceDir)
	if err != nil {
		finishErr("could not read the Jellyfin library: " + err.Error())
		return
	}
	total := 0
	for _, it := range items {
		total += len(it.Files)
	}
	s.jellyfinMu.Lock()
	s.jellyfinJob.Total = total
	s.jellyfinMu.Unlock()

	staged, missing := 0, 0
	for _, it := range items {
		blob := ""
		if b, err := json.Marshal(importer.MetaFromJellyfin(it)); err == nil {
			blob = string(b)
		}
		for _, f := range it.Files {
			if !fileExists(f.Path) {
				missing++
				s.jellyfinAdvance(false)
				continue
			}
			_, _ = db.InsertImport(ctx, pool, db.Import{
				CategoryID: cat.ID,
				SourcePath: f.Path, Filename: filepath.Base(f.Path),
				Title: it.Title, Year: it.Year, Season: f.Season, Episode: f.Episode,
				Subtitles: jellyfinSubsJSON(f.Path), Poster: it.PosterPath,
				APIJSON: blob, Origin: db.OriginJellyfin,
				Status: db.StatusPreCheck, DeleteAfter: false,
			})
			staged++
			s.jellyfinAdvance(true)
		}
	}

	s.jellyfinMu.Lock()
	s.jellyfinJob.Done = total
	s.jellyfinJob.Staged = staged
	s.jellyfinJob.Missing = missing
	s.jellyfinJob.Finished = true
	s.jellyfinJob.running = false
	s.jellyfinMu.Unlock()
	s.logger().For(logging.Import).Info(fmt.Sprintf("staged %d Jellyfin file(s) for import", staged),
		logging.Fields{"staged": staged, "missing": missing})
}

// jellyfinAdvance records one file processed (staged or missing) for the progress poll.
func (s *Server) jellyfinAdvance(staged bool) {
	s.jellyfinMu.Lock()
	s.jellyfinJob.Done++
	if staged {
		s.jellyfinJob.Staged++
	} else {
		s.jellyfinJob.Missing++
	}
	s.jellyfinMu.Unlock()
}

// jellyfinSubsJSON encodes the sidecar subtitles beside a source video as the
// importer's Subtitle list, so they ride along into the media folder like every other
// source. Empty when there are none.
func jellyfinSubsJSON(videoPath string) string {
	subs := importer.FindSidecarSubtitles(videoPath)
	if len(subs) == 0 {
		return ""
	}
	b, err := json.Marshal(subs)
	if err != nil {
		return ""
	}
	return string(b)
}
