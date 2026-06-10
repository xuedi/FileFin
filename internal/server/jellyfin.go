package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
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
	req, err := decodeJSON[jellyfinPrepareReq](w, r)
	if err != nil || strings.TrimSpace(req.SourceDir) == "" {
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
	if !s.jellyfinStaging.begin() {
		http.Error(w, "a Jellyfin import is already in progress", http.StatusConflict)
		return
	}

	go s.runJellyfinStaging(req)
	w.WriteHeader(http.StatusAccepted)
}

// handleJellyfinProgress returns the live staging job state for the polling frontend.
func (s *Server) handleJellyfinProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.jellyfinStaging.snapshot())
}

// runJellyfinStaging is the background staging walk. It resolves the target category
// (creating it from the typed name when asked), scans the NFO library into source-neutral
// staged items (the item's NFO metadata blob and sidecar subtitles), then hands them to
// the shared stageImport driver.
func (s *Server) runJellyfinStaging(req jellyfinPrepareReq) {
	ctx := context.Background()
	finishErr := func(msg string) {
		s.jellyfinStaging.fail(msg)
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

	scanned, err := jellyfin.Scan(ctx, req.SourceDir)
	if err != nil {
		finishErr("could not read the Jellyfin library: " + err.Error())
		return
	}

	var items []stagedItem
	for _, it := range scanned {
		blob := ""
		if b, err := json.Marshal(importer.MetaFromJellyfin(it)); err == nil {
			blob = string(b)
		}
		for _, f := range it.Files {
			items = append(items, stagedItem{
				categoryID: cat.ID,
				sourcePath: f.Path,
				title:      it.Title, year: it.Year, season: f.Season, episode: f.Episode,
				subtitles: jellyfinSubsJSON(f.Path), poster: it.PosterPath,
				metaBlob: blob,
			})
		}
	}

	s.stageImport(ctx, pool, &s.jellyfinStaging, items, db.OriginJellyfin, "Jellyfin")
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
