package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"filefin/internal/logging"
)

const sessionCookie = "filefin_session"

type userKey struct{}

// sessionStore holds active sessions in memory; they are cleared on restart.
type sessionStore struct {
	mu sync.Mutex
	m  map[string]string // session id -> username
}

func newSessionStore() *sessionStore { return &sessionStore{m: map[string]string{}} }

func (s *sessionStore) create(user string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	s.mu.Lock()
	s.m[id] = user
	s.mu.Unlock()
	return id, nil
}

func (s *sessionStore) user(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.m[id]
	return u, ok
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}

// deleteUser drops every active session for a username, so blocking an account logs it
// out immediately rather than only on its next request.
func (s *sessionStore) deleteUser(user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, u := range s.m {
		if u == user {
			delete(s.m, id)
		}
	}
}

// auth guards a handler, requiring a valid session cookie and stashing the username.
func (s *Server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		user, ok := s.sessions.user(c.Value)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)))
	})
}

// admin guards a handler, requiring a valid session whose user is an admin. Entering
// an admin page also builds the cache on the fly when missing - best-effort, since
// the app reads the filesystem and must keep working if the cache is unavailable.
func (s *Server) admin(next http.HandlerFunc) http.Handler {
	return s.auth(func(w http.ResponseWriter, r *http.Request) {
		user, _ := r.Context().Value(userKey{}).(string)
		s.mu.RLock()
		u, ok := s.cfg.Users[user]
		s.mu.RUnlock()
		if !ok || !u.Admin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if _, err := s.ensureDB(r.Context()); err != nil {
			s.logger().For(logging.Backend).Error("cache unavailable", logging.Fields{"error": err.Error()})
		}
		next(w, r)
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	u, ok := s.cfg.Users[req.Username]
	malConfigured := s.cfg.MALClientID != ""
	s.mu.RUnlock()
	// A blocked account is rejected exactly like a bad password, so a block leaks
	// nothing about which accounts exist.
	if !ok || u.Blocked || bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(req.Password)) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	id, err := s.sessions.create(req.Username)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.stampLogin(req.Username)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(7 * 24 * time.Hour),
	})
	writeJSON(w, authResultOf(req.Username, u, malConfigured))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userKey{}).(string)
	s.mu.RLock()
	u, ok := s.cfg.Users[user]
	malConfigured := s.cfg.MALClientID != ""
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, authResultOf(user, u, malConfigured))
}
