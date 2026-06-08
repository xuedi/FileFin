package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"filefin/internal/config"
	"filefin/internal/db"
)

// userDTO is one account as the admin Users page sees it. The password hash is never
// exposed.
type userDTO struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Alias       string `json:"alias"`
	Admin       bool   `json:"admin"`
	Blocked     bool   `json:"blocked"`
	CreatedAt   int64  `json:"createdAt"`
	LastLoginAt int64  `json:"lastLoginAt"`
}

func dto(username string, u config.User) userDTO {
	return userDTO{
		ID: u.ID, Username: username, Alias: u.Alias, Admin: u.Admin,
		Blocked: u.Blocked, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt,
	}
}

// dbUser maps a config user (plus its username) into the cache mirror's row shape.
func dbUser(username string, u config.User) db.User {
	return db.User{
		ID: u.ID, Username: username, Alias: u.Alias, Admin: u.Admin,
		Blocked: u.Blocked, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt,
	}
}

// handleListUsers returns every account, sorted by id, read from the config (the source
// of truth). The hash is never included.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	out := make([]userDTO, 0, len(s.cfg.Users))
	for name, u := range s.cfg.Users {
		out = append(out, dto(name, u))
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, out)
}

// handleCreateUser adds an account: it mints the id from the SQLite cache, writes the
// user into the config (the source of truth), and returns the new row.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Alias    string `json:"alias"`
		Password string `json:"password"`
		Admin    bool   `json:"admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(email) {
		http.Error(w, "a valid email is required", http.StatusBadRequest)
		return
	}
	if req.Password == "" {
		http.Error(w, "a password is required", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	_, exists := s.cfg.Users[email]
	s.mu.RUnlock()
	if exists {
		http.Error(w, "a user with that email already exists", http.StatusConflict)
		return
	}

	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusInternalServerError)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	now := time.Now().Unix()
	u := config.User{Hash: string(hash), Alias: strings.TrimSpace(req.Alias), Admin: req.Admin, CreatedAt: now}
	id, err := db.InsertUser(r.Context(), pool, dbUser(email, u))
	if err != nil {
		http.Error(w, "could not create user", http.StatusInternalServerError)
		return
	}
	u.ID = id

	s.mu.Lock()
	s.cfg.Users[email] = u
	err = config.Save(s.cfg)
	s.mu.Unlock()
	if err != nil {
		http.Error(w, "could not write config", http.StatusInternalServerError)
		return
	}
	writeJSON(w, dto(email, u))
}

// handleUpdateUser edits an account's alias, admin flag, blocked flag, and/or password.
// It refuses any change that would lock the installation out: an admin cannot block or
// de-admin their own account, and no change may leave zero active admins. Blocking an
// account also drops its active sessions.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad user id", http.StatusBadRequest)
		return
	}
	var req struct {
		Alias    *string `json:"alias"`
		Admin    *bool   `json:"admin"`
		Blocked  *bool   `json:"blocked"`
		Password *string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	caller := userFrom(r)

	s.mu.Lock()
	// Find the target by id.
	name := ""
	for n, u := range s.cfg.Users {
		if u.ID == id {
			name = n
			break
		}
	}
	if name == "" {
		s.mu.Unlock()
		http.Error(w, "no such user", http.StatusNotFound)
		return
	}
	u := s.cfg.Users[name]
	self := name == caller

	// Build the proposed change.
	updated := u
	if req.Alias != nil {
		updated.Alias = strings.TrimSpace(*req.Alias)
	}
	if req.Admin != nil {
		updated.Admin = *req.Admin
	}
	if req.Blocked != nil {
		updated.Blocked = *req.Blocked
	}

	// Guardrails (checked against the proposed change).
	if self && req.Blocked != nil && *req.Blocked {
		s.mu.Unlock()
		http.Error(w, "you cannot block your own account", http.StatusForbidden)
		return
	}
	if self && req.Admin != nil && !*req.Admin {
		s.mu.Unlock()
		http.Error(w, "you cannot remove your own admin rights", http.StatusForbidden)
		return
	}
	if !activeAdminRemains(s.cfg.Users, name, updated) {
		s.mu.Unlock()
		http.Error(w, "there must be at least one active admin", http.StatusForbidden)
		return
	}

	if req.Password != nil && *req.Password != "" {
		hash, herr := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if herr != nil {
			s.mu.Unlock()
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		updated.Hash = string(hash)
	}

	nowBlocked := updated.Blocked && !u.Blocked
	s.cfg.Users[name] = updated
	pool := s.db
	saveErr := config.Save(s.cfg)
	s.mu.Unlock()
	if saveErr != nil {
		http.Error(w, "could not write config", http.StatusInternalServerError)
		return
	}
	if pool != nil {
		_ = db.UpsertUser(r.Context(), pool, dbUser(name, updated))
	}
	if nowBlocked {
		s.sessions.deleteUser(name)
	}
	writeJSON(w, dto(name, updated))
}

// activeAdminRemains reports whether, after replacing changedName with changed, at least
// one account is still admin and not blocked.
func activeAdminRemains(users map[string]config.User, changedName string, changed config.User) bool {
	for name, u := range users {
		if name == changedName {
			u = changed
		}
		if u.Admin && !u.Blocked {
			return true
		}
	}
	return false
}

// stampLogin records a user's last-login time in the config (source of truth) and, when
// the cache is already open, mirrors it. Best-effort: a failed write never fails login.
func (s *Server) stampLogin(username string) {
	now := time.Now().Unix()
	s.mu.Lock()
	u, ok := s.cfg.Users[username]
	if ok {
		u.LastLoginAt = now
		s.cfg.Users[username] = u
		_ = config.Save(s.cfg)
	}
	pool := s.db
	s.mu.Unlock()
	if ok && pool != nil {
		_ = db.TouchUserLogin(context.Background(), pool, username, now)
	}
}

// reconcileUsers keeps the disposable users mirror in step with the config and back-fills
// any account that has no id yet (the install admin on first run) by minting one from the
// cache and saving it into the config. It is a no-op once the mirror matches and every
// account has an id.
func (s *Server) reconcileUsers(ctx context.Context, pool *sql.DB) error {
	existing, err := db.ListUsers(ctx, pool)
	if err != nil {
		return err
	}
	idByName := make(map[string]int64, len(existing))
	for _, u := range existing {
		idByName[u.Username] = u.ID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	inConfig := make(map[string]bool, len(s.cfg.Users))
	minted := false
	for name, u := range s.cfg.Users {
		inConfig[name] = true
		if u.ID == 0 {
			// A leftover mirror (e.g. after a reinstall) may already hold a row for this
			// username; adopt its id rather than inserting a colliding one. Otherwise mint
			// a fresh id.
			if id, ok := idByName[name]; ok {
				u.ID = id
			} else {
				id, ierr := db.InsertUser(ctx, pool, dbUser(name, u))
				if ierr != nil {
					return ierr
				}
				u.ID = id
			}
			s.cfg.Users[name] = u
			minted = true
		}
		if err := db.UpsertUser(ctx, pool, dbUser(name, u)); err != nil {
			return err
		}
	}
	// Drop mirror rows for accounts the config no longer has (stale after a reinstall),
	// keeping the disposable mirror a faithful copy of the source of truth.
	for _, u := range existing {
		if !inConfig[u.Username] {
			if err := db.DeleteUserByID(ctx, pool, u.ID); err != nil {
				return err
			}
		}
	}
	if minted {
		return config.Save(s.cfg)
	}
	return nil
}

// validEmail is a loose check: a single "@" with non-empty, space-free parts on each
// side. The username is an email but the app never sends mail, so this is shape-only.
func validEmail(s string) bool {
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') || at == len(s)-1 {
		return false
	}
	return strings.Contains(s[at+1:], ".")
}
