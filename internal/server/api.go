package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"filefin/internal/cache"
	"filefin/internal/logging"
	"filefin/internal/state"
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
	if user := userFrom(r); user != "" {
		for i := range media {
			if all, err := state.Load(media[i].FolderPath); err == nil {
				media[i].Watched = all[user].Watched
			}
		}
	}
	writeJSON(w, media)
}

// mediaWhere returns every media item whose per-user state satisfies keep, newest-first by
// the folder's state.md mtime. State is read live, so a folder with no state file is
// simply skipped (keep is never called for it).
func (s *Server) mediaWhere(user string, keep func(state.UserState) bool) ([]cache.MediaSummary, error) {
	media, err := s.store.AllMedia()
	if err != nil {
		return nil, err
	}
	type scored struct {
		ms    cache.MediaSummary
		mtime int64
	}
	var hits []scored
	for _, m := range media {
		all, err := state.Load(m.FolderPath)
		if err != nil {
			continue
		}
		us, ok := all[user]
		if !ok || !keep(us) {
			continue
		}
		var mt int64
		if fi, err := os.Stat(filepath.Join(m.FolderPath, state.FileName)); err == nil {
			mt = fi.ModTime().UnixNano()
		}
		hits = append(hits, scored{ms: m, mtime: mt})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mtime > hits[j].mtime })
	out := make([]cache.MediaSummary, len(hits))
	for i, h := range hits {
		out[i] = h.ms
	}
	return out, nil
}

// handleContinue lists the user's in-progress media (a resume pointer but not yet fully
// watched) across the whole library, most recently played first.
func (s *Server) handleContinue(w http.ResponseWriter, r *http.Request) {
	out, err := s.mediaWhere(userFrom(r), func(us state.UserState) bool {
		return us.Progress != nil && !us.Watched
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

// handleFavorites lists the user's favorited media across the whole library.
func (s *Server) handleFavorites(w http.ResponseWriter, r *http.Request) {
	out, err := s.mediaWhere(userFrom(r), func(us state.UserState) bool { return us.Favorite })
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

// handleCompleted lists the media the user has fully watched across the whole library.
func (s *Server) handleCompleted(w http.ResponseWriter, r *http.Request) {
	out, err := s.mediaWhere(userFrom(r), func(us state.UserState) bool { return us.Watched })
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

// handleFavorite sets or clears the favorite flag for a media folder.
func (s *Server) handleFavorite(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	var req struct {
		Favorite bool `json:"favorite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	folder, err := s.store.FolderPath(r.PathValue("id"))
	if err != nil || folder == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	err = s.stateMgr.Update(folder, user, func(st state.UserState) state.UserState {
		st.Favorite = req.Favorite
		return st
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleClearProgress drops the resume pointer for a media folder, removing it from the
// user's "continue watching". The watched flag is left untouched.
func (s *Server) handleClearProgress(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	folder, err := s.store.FolderPath(r.PathValue("id"))
	if err != nil || folder == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	err = s.stateMgr.Update(folder, user, func(st state.UserState) state.UserState {
		st.Progress = nil
		return st
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleClearWatched marks a media folder unwatched for the user, removing it from
// "completed". The resume pointer is cleared too, so a leftover pointer does not bounce
// the item into "continue watching"; the favorite flag is left untouched.
func (s *Server) handleClearWatched(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	folder, err := s.store.FolderPath(r.PathValue("id"))
	if err != nil || folder == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	err = s.stateMgr.Update(folder, user, func(st state.UserState) state.UserState {
		st.Watched = false
		st.Progress = nil
		return st
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.MediaDetail(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.fillWatchState(userFrom(r), d)
	writeJSON(w, d)
}

// fileRefs builds the on-disk file references for a detail's ordered files.
func fileRefs(d *cache.MediaDetail) []string {
	keys := make([]state.FileKey, len(d.Files))
	for i, f := range d.Files {
		keys[i] = state.FileKey{Season: f.Season, Episode: f.Episode}
	}
	return state.Refs(keys)
}

// fillWatchState reads the user's state.md live and folds the derived watch view into d.
// Best-effort: a read error leaves the (zero) "unwatched" defaults in place.
func (s *Server) fillWatchState(user string, d *cache.MediaDetail) {
	if user == "" || d.FolderPath == "" {
		return
	}
	all, err := state.Load(d.FolderPath)
	if err != nil {
		return
	}
	v := state.View(all[user], fileRefs(d))
	d.Watched = v.Watched
	d.Favorite = all[user].Favorite
	d.ContinueIndex = v.ContinueIndex
	d.ContinueSeconds = v.ContinueSeconds
	for i := range d.Files {
		if i < len(v.PerFile) {
			d.Files[i].Watched = v.PerFile[i]
		}
	}
}

// handleProgress records a playback report into the user's state.md for a media folder.
func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	var req struct {
		File     int     `json:"file"`
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
		Event    string  `json:"event"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	d, err := s.store.MediaDetail(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	refs := fileRefs(d)
	if req.File < 0 || req.File >= len(refs) {
		http.Error(w, "bad file index", http.StatusBadRequest)
		return
	}
	var becameWatched bool
	err = s.stateMgr.Update(d.FolderPath, user, func(st state.UserState) state.UserState {
		before := st.Watched
		out := state.Apply(st, refs, req.File, req.Position, req.Duration)
		becameWatched = !before && out.Watched
		return out
	})
	if err != nil {
		s.log.Error("could not record progress for "+d.Title, logging.Fields{"path": d.FolderPath, "error": err.Error()})
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if becameWatched {
		s.log.Info(user+" watched "+d.Title, logging.Fields{"id": d.ID})
	}
	w.WriteHeader(http.StatusNoContent)
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
