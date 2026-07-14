package server

import (
	"crypto/subtle"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"filefin/internal/config"
	"filefin/internal/db"
	"filefin/internal/logging"
	"filefin/internal/version"
)

// handleState tells the frontend whether first-run setup is still needed, and the running
// binary's version (shown in the UI, so it always reflects the actual deployed build). Setup
// is "needed" until an admin account exists; a pending config (port + token, no users) still
// needs setup. The setup token is never exposed here - it reaches the browser only via the
// install URL the CLI prints.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	needsSetup := s.cfg == nil || !s.cfg.SetupComplete()
	s.mu.RUnlock()
	writeJSON(w, struct {
		NeedsSetup bool   `json:"needsSetup"`
		Version    string `json:"version"`
	}{needsSetup, version.Version})
}

// validSetupToken constant-time-compares a presented token against the pending config's setup
// token. An empty configured token (or nil config) never matches, so the gate fails closed.
func validSetupToken(cfg *config.Config, token string) bool {
	if cfg == nil || cfg.SetupToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(cfg.SetupToken)) == 1
}

// handleInstall is the first-run setup: gated by the one-time setup token, it creates the admin
// user, clears the token, and persists, then fires reload so Run rebuilds the handler without
// the install routes (the port was already fixed at bootstrap). Mounted only while setup is
// pending. The cache is always local SQLite, so there is nothing to configure.
func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg == nil || cfg.SetupComplete() {
		http.Error(w, "already installed", http.StatusConflict)
		return
	}

	req, err := decodeJSON[struct {
		Username string `json:"username"`
		Password string `json:"password"`
		DataDir  string `json:"dataDir"`
		Token    string `json:"token"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// The setup token gates the whole endpoint: it arrives as the X-Setup-Token header (what
	// the SPA sends) or, as a fallback, the body's token field.
	token := r.Header.Get("X-Setup-Token")
	if token == "" {
		token = req.Token
	}
	if !validSetupToken(cfg, token) {
		http.Error(w, "invalid or missing setup token", http.StatusForbidden)
		return
	}

	req.Username = config.NormalizeUsername(req.Username)
	if req.Username == "" || req.Password == "" {
		http.Error(w, "username and password are required", http.StatusBadRequest)
		return
	}
	if msg := passwordPolicyError(req.Password); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
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
	// Start from the pending config so the bootstrap-chosen port and bind address carry over;
	// only the admin user and data folder are added and the token cleared.
	next := *cfg
	next.Users = map[string]config.User{req.Username: {
		Hash: string(hash), Admin: true, CreatedAt: time.Now().Unix(),
	}}
	next.DataDir = req.DataDir
	next.ClearSetupToken()
	if err := config.Save(&next); err != nil {
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
	}{next.Port})
	select {
	case s.reload <- struct{}{}:
	default:
	}
}
