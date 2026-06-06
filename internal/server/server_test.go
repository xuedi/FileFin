package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"filefin/internal/cache"
	"filefin/internal/config"
	"filefin/internal/scanner"
	"filefin/internal/transcode"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	scan, err := scanner.Scan("../scanner/testdata")
	if err != nil {
		t.Fatal(err)
	}
	store, err := cache.Open(t.TempDir() + "/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Rebuild(scan); err != nil {
		t.Fatal(err)
	}
	cfg := config.New()
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	cfg.Users["alice"] = string(hash)
	return New(cfg, store, transcode.Encoder{})
}

func TestAuthRequired(t *testing.T) {
	srv := testServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/categories", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func login(t *testing.T, srv *Server, body string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
	srv.Handler().ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestLoginAndBrowse(t *testing.T) {
	srv := testServer(t)

	if c := login(t, srv, `{"username":"alice","password":"wrong"}`); c != nil {
		t.Fatal("login should fail with wrong password")
	}
	cookie := login(t, srv, `{"username":"alice","password":"secret"}`)
	if cookie == nil {
		t.Fatal("expected session cookie on successful login")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/categories", nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Films - English") {
		t.Fatalf("categories: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRangeRequest(t *testing.T) {
	srv := testServer(t)
	cookie := login(t, srv, `{"username":"alice","password":"secret"}`)

	// List media in the show category (space in the name must be URL-encoded).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/categories/"+url.PathEscape("Shows - China")+"/media", nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("media list: %d", rec.Code)
	}
	id := scanner.MediaID("Shows - China", "(2002) Firefly")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/media/"+id+"/file/0", nil)
	req.Header.Set("Range", "bytes=0-")
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	// Empty test file: a range over a 0-byte file yields 416; a served file yields 200/206.
	if rec.Code != http.StatusPartialContent && rec.Code != http.StatusOK && rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("unexpected stream code: %d", rec.Code)
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("expected Accept-Ranges: bytes header, got %q", rec.Header().Get("Accept-Ranges"))
	}
}
