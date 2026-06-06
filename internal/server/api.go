package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"filefin/internal/logging"
	"filefin/internal/transcode"
)

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.store.Categories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, cats)
}

func (s *Server) handleCategoryMedia(w http.ResponseWriter, r *http.Request) {
	media, err := s.store.MediaByCategory(r.PathValue("cat"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, media)
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.MediaDetail(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, d)
}

func (s *Server) handlePoster(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.PosterPath(r.PathValue("id"))
	if err != nil || p == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, p)
}

// handleStream serves a media file. Browser-native containers are direct-played with
// byte-range support; everything else is redirected to its HLS playlist (the player
// requests that directly, so this branch only guards stray callers and the toggle).
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.Error(w, "bad file index", http.StatusBadRequest)
		return
	}
	p, needsTranscode, err := s.store.PlaybackPath(r.PathValue("id"), n)
	if err != nil || p == "" {
		http.NotFound(w, r)
		return
	}
	if needsTranscode {
		if !s.cfg.TranscodeEnabled {
			http.Error(w, "transcoding disabled", http.StatusUnsupportedMediaType)
			return
		}
		http.Redirect(w, r, r.URL.Path+"/hls/index.m3u8", http.StatusTemporaryRedirect)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// streamTarget resolves the media file for an HLS request and enforces the toggle and
// the transcode-eligibility of the file. It returns the absolute path and a session key.
func (s *Server) streamTarget(w http.ResponseWriter, r *http.Request) (path, key string, ok bool) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.Error(w, "bad file index", http.StatusBadRequest)
		return "", "", false
	}
	p, err := s.store.FilePath(r.PathValue("id"), n)
	if err != nil || p == "" {
		http.NotFound(w, r)
		return "", "", false
	}
	if !s.cfg.TranscodeEnabled || !transcode.NeedsTranscode(filepath.Ext(p)) {
		http.Error(w, "not transcodable", http.StatusUnsupportedMediaType)
		return "", "", false
	}
	return p, r.PathValue("id") + "/" + r.PathValue("n"), true
}

func (s *Server) handleHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	p, key, ok := s.streamTarget(w, r)
	if !ok {
		return
	}
	title, _ := s.store.Title(r.PathValue("id"))
	playlist, err := s.hls.Playlist(key, p, title)
	if err != nil {
		s.log.Error("transcode failed for "+title, logging.Fields{"path": p, "error": err.Error()})
		http.Error(w, "transcode failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	_, _ = w.Write(playlist)
}

func (s *Server) handleHLSSegment(w http.ResponseWriter, r *http.Request) {
	_, key, ok := s.streamTarget(w, r)
	if !ok {
		return
	}
	// A not-yet-ready or reaped segment is routine (the client re-requests the
	// playlist); a 503 is the whole signal, so it is not logged.
	seg, err := s.hls.Segment(key, r.PathValue("seg"))
	if err != nil {
		http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	http.ServeFile(w, r, seg)
}
