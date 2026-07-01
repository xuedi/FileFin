package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"filefin/internal/config"
)

// loginTestServer builds an app-mode server with one admin whose password is "password1".
func loginTestServer(t *testing.T) http.Handler {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("password1"), bcrypt.DefaultCost)
	s := New()
	s.cfg = &config.Config{Port: 8080, Users: map[string]config.User{"admin": {Hash: string(hash), Admin: true}}}
	return s.handler()
}

func TestSecurityHeaders(t *testing.T) {
	h := loginTestServer(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/state", nil))
	for k, want := range map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rr.Header().Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
	if csp := rr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP missing frame-ancestors 'none': %q", csp)
	}
}

func TestLoginCookieSecureFlag(t *testing.T) {
	h := loginTestServer(t)
	login := func(setup func(*http.Request)) *http.Cookie {
		t.Helper()
		req := httptest.NewRequest("POST", "/api/login",
			strings.NewReader(`{"username":"admin","password":"password1"}`))
		setup(req)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("login: %d %s", rr.Code, rr.Body.String())
		}
		for _, c := range rr.Result().Cookies() {
			if c.Name == sessionCookie && c.Value != "" {
				return c
			}
		}
		t.Fatal("no session cookie set")
		return nil
	}
	// Plain HTTP from a direct client: not Secure, so a plain-HTTP LAN box still works.
	if c := login(func(r *http.Request) { r.RemoteAddr = "192.0.2.4:5555" }); c.Secure {
		t.Error("cookie should not carry Secure on a plain direct request")
	}
	// TLS terminated by the co-located (loopback) reverse proxy: Secure.
	if c := login(func(r *http.Request) {
		r.RemoteAddr = "127.0.0.1:5555"
		r.Header.Set("X-Forwarded-Proto", "https")
	}); !c.Secure {
		t.Error("cookie should carry Secure behind a loopback TLS proxy")
	}
}

func TestLoginThrottle(t *testing.T) {
	h := loginTestServer(t)
	bad := func() int {
		req := httptest.NewRequest("POST", "/api/login",
			strings.NewReader(`{"username":"admin","password":"wrongpassword"}`))
		req.RemoteAddr = "192.0.2.9:1111"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	// The first loginAccountFails wrong attempts are rejected 401; the account then locks.
	for i := 0; i < loginAccountFails; i++ {
		if code := bad(); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d, want 401", i+1, code)
		}
	}
	if code := bad(); code != http.StatusTooManyRequests {
		t.Fatalf("after lockout: %d, want 429", code)
	}
}

func TestPasswordPolicyOnInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := New()
	h := s.handler()
	body := `{"username":"admin","password":"short","port":9999,"dataDir":"` + t.TempDir() + `"}`
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/install", strings.NewReader(body)))
	if rr.Code != 400 {
		t.Fatalf("short install password: %d, want 400", rr.Code)
	}
	if config.Exists() {
		t.Fatal("config should not be written for a policy-violating password")
	}
}

func TestPasswordPolicyOnCreate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, h, admin, _ := installedServer(t, t.TempDir())
	rr := do(t, h, "POST", "/api/admin/users", `{"email":"x@example.com","password":"short"}`, admin)
	if rr.Code != 400 {
		t.Fatalf("short create password: %d %s, want 400", rr.Code, rr.Body.String())
	}
}
