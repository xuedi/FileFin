package server

import (
	"net/http"

	"filefin/internal/db"
)

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

	s.mu.RLock()
	total, admins := len(s.cfg.Users), 0
	for _, u := range s.cfg.Users {
		if u.Admin {
			admins++
		}
	}
	mode := s.cfg.OptimizeModeOr()
	s.mu.RUnlock()

	writeJSON(w, dashboardView{
		Library:   libraryStats{Categories: categories, Media: media, Files: files},
		Users:     userStats{Total: total, Admins: admins},
		Optimizer: optimizerStats{Mode: mode, Pending: optPending, Active: len(optActive)},
		Enrich:    pendingStat{Pending: enrichPending},
		Imports:   activeStat{Active: len(importsActive)},
	})
}

// dashboardView is the typed admin-dashboard summary, replacing the nested map payload.
type dashboardView struct {
	Library   libraryStats   `json:"library"`
	Users     userStats      `json:"users"`
	Optimizer optimizerStats `json:"optimizer"`
	Enrich    pendingStat    `json:"enrich"`
	Imports   activeStat     `json:"imports"`
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
