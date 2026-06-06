// Package server is the authenticated HTTP layer: it serves the read API,
// streams media from disk, and serves the embedded Svelte frontend.
package server

import (
	"context"
	"io/fs"
	"net/http"
	"strings"

	"filefin/internal/cache"
	"filefin/internal/config"
	"filefin/internal/logging"
	"filefin/internal/state"
	"filefin/internal/transcode"
	"filefin/web"
)

// Server holds the dependencies for the HTTP handlers.
type Server struct {
	cfg      *config.Config
	store    *cache.Store
	sessions *sessionStore
	hls      *transcode.Manager
	stateMgr *state.Manager
	log      *logging.Scoped
}

// userKey identifies the authenticated username stored in a request's context.
type ctxKey int

const userKey ctxKey = iota

// userFrom returns the authenticated username for a request, set by auth.
func userFrom(r *http.Request) string {
	u, _ := r.Context().Value(userKey).(string)
	return u
}

// New constructs a Server. enc is the video encoder transcode sessions use (detected
// once at startup); the zero value falls back to software encoding. lg may be nil.
func New(cfg *config.Config, store *cache.Store, enc transcode.Encoder, lg *logging.Logger) *Server {
	return &Server{
		cfg:      cfg,
		store:    store,
		sessions: newSessionStore(),
		stateMgr: state.NewManager(),
		log:      lg.For(logging.Backend),
		hls: transcode.NewManager(transcode.Options{
			FFmpegPath:  cfg.FFmpegPath,
			FFprobePath: cfg.FFprobePath,
			Encoder:     enc,
			Logger:      lg,
		}),
	}
}

// Close releases server-held resources (active transcode sessions).
func (s *Server) Close() { s.hls.Close() }

// TranscodeActive reports whether any live transcode session is running, so the
// background optimizer can yield to a viewer.
func (s *Server) TranscodeActive() bool { return s.hls.ActiveSessions() > 0 }

// Handler builds the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.Handle("GET /api/me", s.auth(s.handleMe))
	mux.Handle("GET /api/admin/summary", s.adminOnly(s.handleAdminSummary))
	mux.Handle("GET /api/admin/optimizer", s.adminOnly(s.handleAdminOptimizer))
	mux.Handle("GET /api/admin/users", s.adminOnly(s.handleAdminUsers))
	mux.Handle("GET /api/continue", s.auth(s.handleContinue))
	mux.Handle("GET /api/favorites", s.auth(s.handleFavorites))
	mux.Handle("GET /api/completed", s.auth(s.handleCompleted))
	mux.Handle("POST /api/media/{id}/favorite", s.auth(s.handleFavorite))
	mux.Handle("DELETE /api/media/{id}/progress", s.auth(s.handleClearProgress))
	mux.Handle("DELETE /api/media/{id}/watched", s.auth(s.handleClearWatched))
	mux.Handle("GET /api/categories", s.auth(s.handleCategories))
	mux.Handle("GET /api/categories/{cat}/media", s.auth(s.handleCategoryMedia))
	mux.Handle("GET /api/media/{id}", s.auth(s.handleMedia))
	mux.Handle("POST /api/media/{id}/progress", s.auth(s.handleProgress))
	mux.Handle("GET /api/media/{id}/poster", s.auth(s.handlePoster))
	mux.Handle("GET /api/media/{id}/file/{n}", s.auth(s.handleStream))
	mux.Handle("GET /api/media/{id}/file/{n}/hls/index.m3u8", s.auth(s.handleHLSPlaylist))
	mux.Handle("GET /api/media/{id}/file/{n}/hls/{seg}", s.auth(s.handleHLSSegment))
	mux.Handle("/", s.spa())
	return mux
}

// auth guards a handler, requiring a valid session cookie.
func (s *Server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		var user string
		if err == nil {
			user, _ = s.sessions.user(c.Value)
		}
		if err != nil || user == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	})
}

// adminOnly guards a handler so only an authenticated admin reaches it: a logged-out
// caller gets 401, a logged-in non-admin gets 403.
func (s *Server) adminOnly(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		var user string
		if err == nil {
			user, _ = s.sessions.user(c.Value)
		}
		if err != nil || user == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !s.cfg.Users[user].Admin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	})
}

// spa serves the embedded frontend, falling back to index.html for client routes.
func (s *Server) spa() http.Handler {
	sub, _ := fs.Sub(web.Dist, "dist")
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(sub, p); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "frontend not built (run: just web-build)", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
