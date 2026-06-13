package server

import (
	"encoding/json"
	"net/http"
	"time"

	"filefin/internal/db"
)

// fmtUnix renders a unix-seconds timestamp as the canonical YYYY-MM-DD HH:MM:SS, or "" for
// a zero time (never happened). Local time, never locale-formatted.
func fmtUnix(sec int64) string {
	if sec == 0 {
		return ""
	}
	return time.Unix(sec, 0).Format("2006-01-02 15:04:05")
}

// handleSummary aggregates the admin dashboard's overview in one cheap call: library
// totals, account counts, and the optimizer / enrich / import queue state. It derives
// everything from the cache plus the in-memory config; no long-lived state is kept.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pool, err := s.ensureDB(ctx)
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}

	categories, err := db.CountCategories(ctx, pool)
	if err != nil {
		http.Error(w, "could not read library stats", http.StatusInternalServerError)
		return
	}
	media, err := db.CountMedia(ctx, pool)
	if err != nil {
		http.Error(w, "could not read library stats", http.StatusInternalServerError)
		return
	}
	files, err := db.CountFiles(ctx, pool)
	if err != nil {
		http.Error(w, "could not read library stats", http.StatusInternalServerError)
		return
	}
	optPending, err := db.CountPending(ctx, pool)
	if err != nil {
		http.Error(w, "could not read optimizer state", http.StatusInternalServerError)
		return
	}
	optActive, err := db.ListActiveTasks(ctx, pool)
	if err != nil {
		http.Error(w, "could not read optimizer state", http.StatusInternalServerError)
		return
	}
	enrichPending, err := db.CountPendingEnrich(ctx, pool)
	if err != nil {
		http.Error(w, "could not read enrich state", http.StatusInternalServerError)
		return
	}
	importsActive, err := db.ListActiveImports(ctx, pool)
	if err != nil {
		http.Error(w, "could not read import state", http.StatusInternalServerError)
		return
	}
	healthIssues, err := db.CountUnhealthy(ctx, pool)
	if err != nil {
		http.Error(w, "could not read health state", http.StatusInternalServerError)
		return
	}
	healthUnchecked, err := db.CountUncheckedMedia(ctx, pool)
	if err != nil {
		http.Error(w, "could not read health state", http.StatusInternalServerError)
		return
	}

	s.mu.RLock()
	total, admins := len(s.cfg.Users), 0
	for _, u := range s.cfg.Users {
		if u.Admin {
			admins++
		}
	}
	mode := s.cfg.OptimizeModeOr()
	interval := s.cfg.DiscoveryInterval
	s.mu.RUnlock()
	s.discMu.Lock()
	lastSweep := s.discLastSweep
	s.discMu.Unlock()

	writeJSON(w, dashboardView{
		Library:   libraryStats{Categories: categories, Media: media, Files: files},
		Users:     userStats{Total: total, Admins: admins},
		Optimizer: optimizerStats{Mode: mode, Pending: optPending, Active: len(optActive)},
		Enrich:    pendingStat{Pending: enrichPending},
		Imports:   activeStat{Active: len(importsActive)},
		Health: healthStats{
			Issues:    healthIssues,
			Unchecked: healthUnchecked,
			LastSweep: fmtUnix(lastSweep),
			Discovery: discoveryLabel(interval),
		},
	})
}

// taskBacklog is the per-type count of outstanding background tasks (queued + running) for
// the Settings -> System "Tasks" box.
type taskBacklog struct {
	Imports   int `json:"imports"`
	Optimize  int `json:"optimize"`
	Enrich    int `json:"enrich"`
	Thumbnail int `json:"thumbnail"`
	Probe     int `json:"probe"`
}

// handleTaskBacklog returns how many background tasks are outstanding per agent type. It is
// a cheap overview (a handful of COUNTs); per-type reads are best-effort so one failing
// query degrades a single number to zero rather than failing the whole box.
func (s *Server) handleTaskBacklog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pool, err := s.ensureDB(ctx)
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	imports, _ := db.CountUnfinishedImports(ctx, pool)
	optP, _ := db.CountPending(ctx, pool)
	optA, _ := db.ListActiveTasks(ctx, pool)
	enrP, _ := db.CountPendingEnrich(ctx, pool)
	enrA, _ := db.ListActiveEnrich(ctx, pool)
	thP, _ := db.CountPendingThumbnail(ctx, pool)
	thA, _ := db.ListActiveThumbnail(ctx, pool)
	prP, _ := db.CountPendingProbe(ctx, pool)
	prA, _ := db.ListActiveProbe(ctx, pool)
	writeJSON(w, taskBacklog{
		Imports:   imports,
		Optimize:  optP + len(optA),
		Enrich:    enrP + len(enrA),
		Thumbnail: thP + len(thA),
		Probe:     prP + len(prA),
	})
}

// handleHealth returns the items currently flagged with issues (each issue decoded for the
// wire), for the admin health panel.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pool, err := s.ensureDB(ctx)
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	rows, err := db.ListUnhealthy(ctx, pool)
	if err != nil {
		http.Error(w, "could not read health", http.StatusInternalServerError)
		return
	}
	items := make([]healthItem, 0, len(rows))
	for _, r := range rows {
		var issues []Issue
		if r.Issues != "" {
			_ = json.Unmarshal([]byte(r.Issues), &issues)
		}
		items = append(items, healthItem{
			ID: r.MediaID, Title: r.Title, Issues: issues, LastChecked: fmtUnix(r.LastCheckedAt),
		})
	}
	writeJSON(w, healthView{Items: items})
}

// dashboardView is the typed admin-dashboard summary, replacing the nested map payload.
type dashboardView struct {
	Library   libraryStats   `json:"library"`
	Users     userStats      `json:"users"`
	Optimizer optimizerStats `json:"optimizer"`
	Enrich    pendingStat    `json:"enrich"`
	Imports   activeStat     `json:"imports"`
	Health    healthStats    `json:"health"`
}

// healthStats is the dashboard's discovery/health overview: how many items carry issues,
// how many are still unchecked (the sweep backlog), the last completed sweep time, and the
// configured sweep interval label.
type healthStats struct {
	Issues    int    `json:"issues"`
	Unchecked int    `json:"unchecked"`
	LastSweep string `json:"lastSweep"`
	Discovery string `json:"discovery"`
}

// healthView is the admin health panel: the list of flagged items.
type healthView struct {
	Items []healthItem `json:"items"`
}

// healthItem is one flagged media item with its decoded issues and last-checked time.
type healthItem struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Issues      []Issue `json:"issues"`
	LastChecked string  `json:"lastChecked"`
}

type libraryStats struct {
	Categories int `json:"categories"`
	Media      int `json:"media"`
	Files      int `json:"files"`
}

type userStats struct {
	Total  int `json:"total"`
	Admins int `json:"admins"`
}

type optimizerStats struct {
	Mode    string `json:"mode"`
	Pending int    `json:"pending"`
	Active  int    `json:"active"`
}

type pendingStat struct {
	Pending int `json:"pending"`
}

type activeStat struct {
	Active int `json:"active"`
}
