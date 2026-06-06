package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"filefin/internal/optimize"
	"filefin/internal/scanner"
	"filefin/internal/state"
)

// handleMe reports the current session's identity, so the SPA knows who is logged in
// (and whether to show the admin toggle) after a page refresh.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	writeJSON(w, map[string]any{"user": user, "admin": s.cfg.Users[user].Admin})
}

// optimizerItem is one entry of the derived optimizer queue.
type optimizerItem struct {
	Source    string `json:"source"`    // relative to the data dir for readability
	Optimized string `json:"optimized"` // relative to the data dir
	State     string `json:"state"`     // "active" (its .tmp lock exists) or "pending"
}

// optimizerQueue derives the pending optimize work live from a filesystem scan: each
// candidate the optimizer would still process, classed "active" when its in-progress
// .tmp lock currently exists, else "pending". Matches the optimizer's own WorkList so the
// admin view reflects exactly what it will do.
func (s *Server) optimizerQueue() ([]optimizerItem, error) {
	scan, err := scanner.Scan(s.cfg.DataDir)
	if err != nil {
		return nil, err
	}
	work := optimize.WorkList(scan)
	out := make([]optimizerItem, 0, len(work))
	for _, c := range work {
		state := "pending"
		if _, err := os.Stat(c.Optimized + ".tmp"); err == nil {
			state = "active"
		}
		out = append(out, optimizerItem{
			Source:    s.relToData(c.Source),
			Optimized: s.relToData(c.Optimized),
			State:     state,
		})
	}
	return out, nil
}

func (s *Server) relToData(p string) string {
	if rel, err := filepath.Rel(s.cfg.DataDir, p); err == nil {
		return rel
	}
	return p
}

// handleAdminSummary returns the dashboard stats: library totals, account counts, and
// optimizer status.
func (s *Server) handleAdminSummary(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	admins := 0
	for _, u := range s.cfg.Users {
		if u.Admin {
			admins++
		}
	}
	pending, active := 0, 0
	if s.cfg.OptimizeEnabled {
		q, err := s.optimizerQueue()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, it := range q {
			if it.State == "active" {
				active++
			} else {
				pending++
			}
		}
	}
	writeJSON(w, map[string]any{
		"library": stats,
		"users":   map[string]int{"total": len(s.cfg.Users), "admins": admins},
		"optimizer": map[string]any{
			"enabled":    s.cfg.OptimizeEnabled,
			"maxWorkers": s.cfg.OptimizeMaxWorkers,
			"pending":    pending,
			"active":     active,
		},
	})
}

// adminUser is one account's row on the admin Users page.
type adminUser struct {
	User      string `json:"user"`
	Admin     bool   `json:"admin"`
	Completed int    `json:"completed"`
	Favorites int    `json:"favorites"`
}

// handleAdminUsers returns a per-account summary: completed (watched) and favorite counts,
// tallied live across every folder's state.md, for every configured user.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	rows := map[string]*adminUser{}
	for name, u := range s.cfg.Users {
		rows[name] = &adminUser{User: name, Admin: u.Admin}
	}
	media, err := s.store.AllMedia()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, m := range media {
		all, err := state.Load(m.FolderPath)
		if err != nil {
			continue
		}
		for name, us := range all {
			row, ok := rows[name]
			if !ok {
				continue // state for a user no longer in the config
			}
			if us.Watched {
				row.Completed++
			}
			if us.Favorite {
				row.Favorites++
			}
		}
	}
	out := make([]adminUser, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].User < out[j].User })
	writeJSON(w, out)
}

// handleAdminOptimizer returns the full derived optimizer queue.
func (s *Server) handleAdminOptimizer(w http.ResponseWriter, r *http.Request) {
	items := []optimizerItem{}
	if s.cfg.OptimizeEnabled {
		q, err := s.optimizerQueue()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = q
	}
	writeJSON(w, map[string]any{"enabled": s.cfg.OptimizeEnabled, "items": items})
}
