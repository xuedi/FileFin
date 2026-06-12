// Package server is the whole runtime: it boots in install mode when no config
// exists, serves the embedded Svelte frontend plus a small JSON API, and rebinds to
// the configured port once the user completes setup.
package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"filefin/internal/config"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/transcode"
	"filefin/web"
)

// Server holds the live state shared across rebinds. cfg is nil in install mode.
type Server struct {
	mu       sync.RWMutex
	cfg      *config.Config
	sessions *sessionStore
	reload   chan struct{}

	db *sql.DB // lazily opened SQLite cache pool (nil until an admin page is entered)

	// metaMgr serializes meta.json writes (rich metadata + per-user playback state)
	// across the importer, the enricher, and the playback-state handlers, so no
	// concurrent write can drop a section.
	metaMgr *importer.Manager
	// hls is the on-the-fly transcode session manager, lazily built on first playback
	// and discarded when transcoding settings change so the new paths take effect.
	hls *transcode.Manager

	// lg is the live structured logger; logCloser closes a file output on swap. Both
	// are guarded by mu. lg is nil-safe so callers never guard.
	lg        *logging.Logger
	logCloser io.Closer

	// progress holds live copy byte counts for in-flight imports, keyed by import id.
	// It is fresher than the periodic DB mirror and read by the Progress endpoint.
	progMu      sync.Mutex
	progress    map[int64]progressEntry
	pollerStart sync.Once

	// optimizer orchestration. reconfigOpt nudges the supervisor to re-read the mode and
	// relaunch its agents; optPercent holds live encode percents (fresher than the DB
	// mirror) keyed by optimize-task id.
	optimizeStart sync.Once
	reconfigOpt   chan struct{}
	optMu         sync.Mutex
	optPercent    map[int64]int

	// enrichStart guards the single OMDb enrichment agent goroutine.
	enrichStart sync.Once

	// thumbnailStart guards the single thumbnail agent goroutine.
	thumbnailStart sync.Once

	// probeStart guards the single format-probe agent goroutine.
	probeStart sync.Once

	// discovery orchestration. The supervisor (optimizer pattern) re-arms its ticker on a
	// reconfigDisc signal when the interval setting changes. maintMu serializes the cache
	// mutations of a discovery tick against a full rebuild so the two never overlap;
	// discRunning skips a tick while the previous one is still going; discLastSweep records
	// the last completed sweep time (unix seconds) for the dashboard.
	discoveryStart sync.Once
	reconfigDisc   chan struct{}
	maintMu        sync.Mutex
	discMu         sync.Mutex
	discRunning    bool
	discLastSweep  int64

	// plexStaging/jellyfinStaging each track a single in-flight library staging job's
	// live progress (one job at a time per source, like the optimizer percent map). The
	// frontend polls them.
	plexStaging     stagingTracker
	jellyfinStaging stagingTracker
}

func New() *Server {
	return &Server{
		sessions:     newSessionStore(),
		reload:       make(chan struct{}, 1),
		progress:     map[int64]progressEntry{},
		metaMgr:      importer.NewManager(),
		reconfigOpt:  make(chan struct{}, 1),
		optPercent:   map[int64]int{},
		reconfigDisc: make(chan struct{}, 1),
	}
}

// logger returns the live logger under lock (nil-safe for callers).
func (s *Server) logger() *logging.Logger {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lg
}

// configureLogger builds or live-reconfigures the logger from cfg (nil => defaults of
// info/STDOUT). A bad output keeps the current destination and only applies the level,
// so a typo in Settings never silences the app.
func (s *Server) configureLogger(cfg *config.Config) {
	level, output := "", ""
	if cfg != nil {
		level, output = cfg.LogLevel, cfg.LogOutput
	}
	lvl, err := logging.ParseLevel(level)
	if err != nil {
		lvl = logging.Info
	}
	w, closer, outErr := logging.ResolveOutput(output)
	if outErr != nil {
		s.mu.Lock()
		if s.lg == nil {
			lg, c, _ := logging.Open("", "") // stdout/info bootstrap
			s.lg, s.logCloser = lg, c
		}
		s.lg.SetLevel(lvl)
		s.mu.Unlock()
		s.logger().For(logging.Backend).Error("log output unavailable, keeping current destination",
			logging.Fields{"output": output, "error": outErr.Error()})
		return
	}
	s.mu.Lock()
	old := s.logCloser
	if s.lg == nil {
		s.lg = logging.New(lvl, w)
	} else {
		s.lg.SetLevel(lvl)
		s.lg.SetOutput(w)
	}
	s.logCloser = closer
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

// Run serves until the process is stopped. After install (or any config change) the
// reload signal makes it shut the current listener and rebind with the new config,
// which is how the chosen port takes effect without a restart.
func Run() error {
	s := New()
	s.startPoller()
	for {
		var cfg *config.Config
		if config.Exists() {
			c, err := config.Load()
			if err != nil {
				return err
			}
			cfg = c
		}
		s.mu.Lock()
		s.cfg = cfg
		s.mu.Unlock()
		s.configureLogger(cfg)
		s.startOptimizer()
		s.startEnrichAgent()
		s.startThumbnailAgent()
		s.startProbeAgent()
		s.startDiscovery()
		s.signalReconfigOpt()
		s.signalReconfigDisc()

		port := config.DefaultPort
		mode := "install"
		if cfg != nil {
			port, mode = cfg.Port, "app"
		}
		srv := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: s.handler()}
		errCh := make(chan error, 1)
		go func() { errCh <- srv.ListenAndServe() }()
		s.logger().For(logging.Backend).Info(
			fmt.Sprintf("serving on http://localhost:%d", port),
			logging.Fields{"port": port, "mode": mode})

		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		case <-s.reload:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(ctx)
			cancel()
		}
	}
}

// handler builds the routes for the current mode. Install routes are always present;
// authenticated app routes only once a config exists.
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/install", s.handleInstall)
	mux.HandleFunc("GET /api/install/browse", s.handleBrowse)

	s.mu.RLock()
	installed := s.cfg != nil
	s.mu.RUnlock()
	if installed {
		mux.HandleFunc("POST /api/login", s.handleLogin)
		mux.HandleFunc("POST /api/logout", s.handleLogout)
		mux.Handle("GET /api/me", s.auth(s.handleMe))
		mux.Handle("GET /api/categories", s.auth(s.handleListCategories))

		// End-user library, detail, status, and playback.
		mux.Handle("GET /api/home", s.auth(s.handleHome))
		mux.Handle("GET /api/category/{id}/media", s.auth(s.handleCategoryMedia))
		mux.Handle("GET /api/media/{id}", s.auth(s.handleMediaDetail))
		mux.Handle("GET /api/media/{id}/poster", s.auth(s.handlePoster))
		mux.Handle("POST /api/media/{id}/favorite", s.auth(s.handleFavorite))
		mux.Handle("POST /api/media/{id}/progress", s.auth(s.handleProgress))
		mux.Handle("DELETE /api/media/{id}/progress", s.auth(s.handleClearProgress))
		mux.Handle("DELETE /api/media/{id}/watched", s.auth(s.handleClearWatched))
		mux.Handle("GET /api/media/{id}/file/{n}", s.auth(s.handleStream))
		mux.Handle("GET /api/media/{id}/file/{n}/hls/index.m3u8", s.auth(s.handleHLSPlaylist))
		mux.Handle("GET /api/media/{id}/file/{n}/hls/{seg}", s.auth(s.handleHLSSegment))
		mux.Handle("GET /api/media/{id}/file/{n}/sub/{k}", s.auth(s.handleSubtitle))
		mux.Handle("GET /api/admin/categories", s.admin(s.handleListCategories))
		mux.Handle("POST /api/admin/categories", s.admin(s.handleCreateCategory))
		mux.Handle("POST /api/admin/categories/reorder", s.admin(s.handleReorderCategories))
		mux.Handle("PUT /api/admin/categories/{name}", s.admin(s.handleSetAlias))
		mux.Handle("DELETE /api/admin/categories/{name}", s.admin(s.handleDeleteCategory))
		mux.Handle("GET /api/admin/browse", s.admin(s.handleAdminBrowse))
		mux.Handle("GET /api/admin/summary", s.admin(s.handleSummary))
		mux.Handle("GET /api/admin/users", s.admin(s.handleListUsers))
		mux.Handle("POST /api/admin/users", s.admin(s.handleCreateUser))
		mux.Handle("PUT /api/admin/users/{id}", s.admin(s.handleUpdateUser))
		mux.Handle("POST /api/admin/import/upload/begin", s.admin(s.handleUploadBegin))
		mux.Handle("POST /api/admin/import/upload/file", s.admin(s.handleUploadFile))
		mux.Handle("POST /api/admin/import/upload/assess", s.admin(s.handleUploadAssess))
		mux.Handle("GET /api/admin/settings", s.admin(s.handleGetSettings))
		mux.Handle("POST /api/admin/settings/format", s.admin(s.handleSetFormat))
		mux.Handle("POST /api/admin/settings/import-folder", s.admin(s.handleSetImportFolder))
		mux.Handle("POST /api/admin/settings/omdb-key", s.admin(s.handleSetOMDBKey))
		mux.Handle("POST /api/admin/settings/logging", s.admin(s.handleSetLogging))
		mux.Handle("POST /api/admin/settings/transcoding", s.admin(s.handleSetTranscoding))
		mux.Handle("POST /api/admin/settings/subtitle-language", s.admin(s.handleSetSubtitleLanguage))
		mux.Handle("POST /api/admin/settings/optimizer", s.admin(s.handleSetOptimizer))
		mux.Handle("POST /api/admin/settings/discovery", s.admin(s.handleSetDiscovery))
		mux.Handle("GET /api/admin/health", s.admin(s.handleHealth))
		mux.Handle("POST /api/admin/discovery/run", s.admin(s.handleRunDiscovery))
		mux.Handle("GET /api/admin/optimize/active", s.admin(s.handleActiveOptimize))
		mux.Handle("POST /api/admin/optimize/scan", s.admin(s.handleOptimizeScan))
		mux.Handle("GET /api/admin/enrich/active", s.admin(s.handleActiveEnrich))
		mux.Handle("POST /api/admin/enrich/scan", s.admin(s.handleEnrichScan))
		mux.Handle("GET /api/admin/thumbnail/active", s.admin(s.handleActiveThumbnail))
		mux.Handle("POST /api/admin/thumbnail/scan", s.admin(s.handleThumbnailScan))
		mux.Handle("GET /api/admin/probe/active", s.admin(s.handleActiveProbe))
		mux.Handle("POST /api/admin/probe/scan", s.admin(s.handleProbeScan))
		mux.Handle("POST /api/admin/rebuild", s.admin(s.handleRebuild))
		mux.Handle("POST /api/admin/import/assess", s.admin(s.handleAssess))
		mux.Handle("GET /api/admin/import/plex/default", s.admin(s.handlePlexDefault))
		mux.Handle("POST /api/admin/import/plex/check", s.admin(s.handlePlexCheck))
		mux.Handle("POST /api/admin/import/plex/resolve", s.admin(s.handlePlexResolve))
		mux.Handle("POST /api/admin/import/plex/prepare", s.admin(s.handlePlexPrepare))
		mux.Handle("GET /api/admin/import/plex/progress", s.admin(s.handlePlexProgress))
		mux.Handle("POST /api/admin/import/jellyfin/prepare", s.admin(s.handleJellyfinPrepare))
		mux.Handle("GET /api/admin/import/jellyfin/progress", s.admin(s.handleJellyfinProgress))
		mux.Handle("POST /api/admin/import/start", s.admin(s.handleStartImport))
		mux.Handle("GET /api/admin/imports", s.admin(s.handleListImports))
		mux.Handle("GET /api/admin/imports/active", s.admin(s.handleActiveImports))
		mux.Handle("PUT /api/admin/imports/{id}", s.admin(s.handleUpdateImport))
		mux.Handle("DELETE /api/admin/imports/{id}", s.admin(s.handleDeleteImport))
	}
	mux.Handle("/", s.spa())
	return mux
}

// spa serves the embedded frontend, falling back to index.html for client routes.
func (s *Server) spa() http.Handler {
	sub, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if f, err := sub.Open(name); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		data, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "frontend not built", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
}

// bestEffort logs and swallows the error of a deliberately best-effort write: a cache
// mirror of the config (the source of truth) or a throttled progress update. These never
// gate whether work happens, so a failure is noted at debug level and never surfaced. The
// named helper makes that intent greppable instead of a bare "_ = db.X(...)".
func (s *Server) bestEffort(err error, what string) {
	if err == nil {
		return
	}
	s.logger().For(logging.Backend).Debug("best-effort "+what+" failed", logging.Fields{"error": err.Error()})
}

// maxBodyBytes caps a decoded JSON request body. The admin API takes only small JSON
// payloads, so a generous 1 MiB ceiling guards a public server against a giant body
// without rejecting anything legitimate. (File uploads use their own streaming path.)
const maxBodyBytes = 1 << 20

// decodeJSON reads and decodes a JSON request body into T, capping the body size so a
// hostile client cannot stream an unbounded body into memory. A decode error is the
// caller's cue to reply 400; any field-level validation happens on the returned value.
func decodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var v T
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	err := json.NewDecoder(r.Body).Decode(&v)
	return v, err
}

// scanResult is the response of the enrich/optimize/thumbnail scan endpoints: how many
// candidates the scan found and how many tasks are now pending.
type scanResult struct {
	Candidates int `json:"candidates"`
	Pending    int `json:"pending"`
}

// queueStatus is the response of the enrich/optimize/thumbnail "active" endpoints: the
// in-flight jobs and the count still waiting. T is the queue's active-row type.
type queueStatus[T any] struct {
	Active  []T `json:"active"`
	Pending int `json:"pending"`
}

// authResult is the response of login and /me: the authenticated user and its flags.
type authResult struct {
	User  string `json:"user"`
	Admin bool   `json:"admin"`
	Alias string `json:"alias"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
