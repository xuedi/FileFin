package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"filefin/internal/db"
	"filefin/internal/logging"
	"filefin/internal/subtitle"
	"filefin/internal/transcode"
)

// transcodeOn reports whether on-the-fly transcoding is enabled in the current config.
func (s *Server) transcodeOn() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg == nil || s.cfg.TranscodeOn()
}

// hlsManager returns the transcode session manager, building it lazily on first use
// from the current ffmpeg/ffprobe paths and the detected encoder. It is reset to nil
// when transcoding settings change so the next call rebuilds with the new paths.
func (s *Server) hlsManager() *transcode.Manager {
	s.mu.RLock()
	m, cfg, lg := s.hls, s.cfg, s.lg
	s.mu.RUnlock()
	if m != nil {
		return m
	}
	opts := transcode.Options{Logger: lg}
	if cfg != nil {
		opts.FFmpegPath, opts.FFprobePath = cfg.FFmpeg(), cfg.FFprobe()
	}
	opts.Encoder = transcode.DetectEncoder(context.Background(), opts)
	built := transcode.NewManager(opts)

	s.mu.Lock()
	if s.hls == nil {
		s.hls = built
	} else {
		built.Close() // lost a race; keep the existing manager
		built = s.hls
	}
	s.mu.Unlock()
	return built
}

// resetHLS discards the current transcode manager so playback paths/encoder are
// re-detected from the updated config on the next playback.
func (s *Server) resetHLS() {
	s.mu.Lock()
	m := s.hls
	s.hls = nil
	s.mu.Unlock()
	if m != nil {
		m.Close()
	}
}

// playbackTarget resolves which file playback should serve for a source and its
// extension: a fresh optimized sibling is served directly (no transcode), otherwise the
// source itself with its native transcode requirement.
func playbackTarget(src, ext string) (serve string, needsTranscode bool) {
	if sib, fresh := transcode.OptimizedSibling(src); fresh {
		return sib, false
	}
	return src, transcode.NeedsTranscode(ext)
}

// isOptimizedSibling reports whether a file name is an optimizer artifact (the
// `.optimized.mp4` direct-play copy or its `.tmp` lock), which the scanners must never
// treat as media of its own.
func isOptimizedSibling(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, transcode.OptimizedExt) || strings.HasSuffix(lower, transcode.OptimizedTmpSuffix)
}

// handleStream serves a media file. Browser-native containers (and any source with a
// fresh optimized sibling) are direct-played with byte-range support; everything else
// 307-redirects to its HLS playlist (the player requests that directly, so this branch
// only guards stray callers and the toggle).
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.Error(w, "bad file index", http.StatusBadRequest)
		return
	}
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	path, ext, err := db.FilePath(r.Context(), pool, r.PathValue("id"), n)
	if err != nil || path == "" {
		http.NotFound(w, r)
		return
	}
	serve, needsTranscode := playbackTarget(path, ext)
	if needsTranscode {
		if !s.transcodeOn() {
			http.Error(w, "transcoding disabled", http.StatusUnsupportedMediaType)
			return
		}
		http.Redirect(w, r, r.URL.Path+"/hls/index.m3u8", http.StatusTemporaryRedirect)
		return
	}
	f, err := os.Open(serve)
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
	pool, ok := s.userPool(w, r)
	if !ok {
		return "", "", false
	}
	p, ext, err := db.FilePath(r.Context(), pool, r.PathValue("id"), n)
	if err != nil || p == "" {
		http.NotFound(w, r)
		return "", "", false
	}
	if !s.transcodeOn() || !transcode.NeedsTranscode(ext) {
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
	pool, _ := s.userPool(w, r)
	title := ""
	if pool != nil {
		if m, err := db.GetMedia(r.Context(), pool, r.PathValue("id")); err == nil {
			title = m.Title
		}
	}
	playlist, err := s.hlsManager().Playlist(key, p, title)
	if err != nil {
		s.logger().For(logging.Frontend).Error("transcode failed for "+title,
			logging.Fields{"path": p, "error": err.Error()})
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
	// A not-yet-ready or reaped segment is routine (the client re-requests it); a 503
	// is the whole signal, so it is not logged.
	seg, err := s.hlsManager().Segment(r.Context(), key, r.PathValue("seg"))
	if err != nil {
		http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	http.ServeFile(w, r, seg)
}

// handleSubtitle serves the k-th sidecar subtitle of media file n converted to WebVTT,
// so the browser's native <track> renders it. The source stays SRT on disk; conversion
// is streamed per request.
func (s *Server) handleSubtitle(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.Error(w, "bad file index", http.StatusBadRequest)
		return
	}
	k, err := strconv.Atoi(r.PathValue("k"))
	if err != nil {
		http.Error(w, "bad subtitle index", http.StatusBadRequest)
		return
	}
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()
	folder, err := db.FolderPath(ctx, pool, id)
	if err != nil || folder == "" {
		http.NotFound(w, r)
		return
	}
	path, _, err := db.FilePath(ctx, pool, id, n)
	if err != nil || path == "" {
		http.NotFound(w, r)
		return
	}
	names, err := folderFileNames(folder)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	subs := subtitle.Sidecars(names, base)
	if k < 0 || k >= len(subs) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(folder, subs[k].Name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	_ = subtitle.ToVTT(w, f) // best-effort: a mid-stream error cannot be reported post-headers
}
