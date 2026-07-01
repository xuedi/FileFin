package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"filefin/internal/logging"
)

const sessionCookie = "filefin_session"

// dummyHash is a valid bcrypt hash the login handler compares against when the account is
// unknown or blocked, so every rejection path spends the same time in bcrypt and cannot be
// distinguished by timing (which would otherwise reveal whether an account exists).
var dummyHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("filefin/constant-time-login-placeholder"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return h
}()

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
	ip := clientIP(r)
	now := time.Now()
	if ok, retry := s.logins.allowed(req.Username, ip, now); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		http.Error(w, "too many attempts, try again later", http.StatusTooManyRequests)
		return
	}

	s.mu.RLock()
	u, ok := s.cfg.Users[req.Username]
	malConfigured := s.cfg.MALClientID != ""
	s.mu.RUnlock()
	// Always run exactly one bcrypt compare. For an unknown or blocked account, compare
	// against a fixed dummy hash so the rejection takes the same time as a wrong password
	// (constant-time), and a block still leaks nothing about which accounts exist.
	hash := dummyHash
	if ok && !u.Blocked && u.Hash != "" {
		hash = []byte(u.Hash)
	}
	valid := bcrypt.CompareHashAndPassword(hash, []byte(req.Password)) == nil && ok && !u.Blocked
	if !valid {
		s.logins.fail(req.Username, ip, now)
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	s.logins.success(req.Username)
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
		Secure:   cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(7 * 24 * time.Hour),
	})
	writeJSON(w, authResultOf(req.Username, u, malConfigured))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: cookieSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
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
