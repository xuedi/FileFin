package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"filefin/internal/config"
	"filefin/internal/db"
	"filefin/internal/logging"
)

// handleState tells the frontend whether first-run setup is still needed.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	needsSetup := s.cfg == nil
	s.mu.RUnlock()
	writeJSON(w, struct {
		NeedsSetup bool `json:"needsSetup"`
	}{needsSetup})
}

// handleInstall is the first-run setup: it creates the admin user and the config on
// the chosen port, then fires reload so Run rebinds there. Allowed only while no
// config exists. The cache is always local SQLite, so there is nothing to configure.
func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	already := s.cfg != nil
	s.mu.RUnlock()
	if already {
		http.Error(w, "already installed", http.StatusConflict)
		return
	}

	req, err := decodeJSON[struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Port     int    `json:"port"`
		DataDir  string `json:"dataDir"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		http.Error(w, "username and password are required", http.StatusBadRequest)
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		req.Port = config.DefaultPort
	}
	req.DataDir = filepath.Clean(strings.TrimSpace(req.DataDir))
	if !filepath.IsAbs(req.DataDir) {
		http.Error(w, "a data folder is required", http.StatusBadRequest)
		return
	}
	if fi, err := os.Stat(req.DataDir); err != nil || !fi.IsDir() {
		http.Error(w, "data folder must be an existing directory", http.StatusBadRequest)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	cfg := &config.Config{
		Port: req.Port,
		Users: map[string]config.User{req.Username: {
			Hash: string(hash), Admin: true, CreatedAt: time.Now().Unix(),
		}},
		DataDir: req.DataDir,
	}
	if err := config.Save(cfg); err != nil {
		http.Error(w, "could not write config", http.StatusInternalServerError)
		return
	}

	// A fresh install starts with a fresh cache: drop any leftover disposable cache from a
	// previous install so its stale rows never carry over. Best-effort - the cache is
	// rebuilt from the data dir on demand regardless.
	if err := db.RemoveCache(); err != nil {
		s.logger().For(logging.Backend).Error("could not remove the old cache on install",
			logging.Fields{"error": err.Error()})
	}

	writeJSON(w, struct {
		Port int `json:"port"`
	}{req.Port})
	select {
	case s.reload <- struct{}{}:
	default:
	}
}
