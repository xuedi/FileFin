// Package server is the authenticated HTTP layer: it serves the read API,
// streams media from disk, and serves the embedded Svelte frontend.
package server

import (
	"io/fs"
	"net/http"
	"strings"

	"filefin/internal/cache"
	"filefin/internal/config"
	"filefin/web"
)

// Server holds the dependencies for the HTTP handlers.
type Server struct {
	cfg      *config.Config
	store    *cache.Store
	sessions *sessionStore
}

// New constructs a Server.
func New(cfg *config.Config, store *cache.Store) *Server {
	return &Server{cfg: cfg, store: store, sessions: newSessionStore()}
}

// Handler builds the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.Handle("GET /api/categories", s.auth(s.handleCategories))
	mux.Handle("GET /api/categories/{cat}/media", s.auth(s.handleCategoryMedia))
	mux.Handle("GET /api/media/{id}", s.auth(s.handleMedia))
	mux.Handle("GET /api/media/{id}/poster", s.auth(s.handlePoster))
	mux.Handle("GET /api/media/{id}/banner", s.auth(s.handleBanner))
	mux.Handle("GET /api/media/{id}/file/{n}", s.auth(s.handleStream))
	mux.Handle("/", s.spa())
	return mux
}

// auth guards a handler, requiring a valid session cookie.
func (s *Server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || !s.sessions.valid(c.Value) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
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
