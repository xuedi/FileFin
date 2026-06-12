package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"filefin/internal/config"
	"filefin/internal/db"
	"filefin/internal/library"
)

func TestInstallFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := New() // cfg nil -> install mode
	h := s.handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/state", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"needsSetup":true`) {
		t.Fatalf("state before install: %d %s", rr.Code, rr.Body.String())
	}

	dataDir := t.TempDir()
	body := `{"username":"admin","password":"pw","port":9999,"dataDir":"` + dataDir + `"}`
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/install", strings.NewReader(body)))
	if rr.Code != 200 {
		t.Fatalf("install: %d %s", rr.Code, rr.Body.String())
	}
	if !config.Exists() {
		t.Fatal("config not written")
	}
	select {
	case <-s.reload:
	default:
		t.Fatal("reload not fired after install")
	}
	got, _ := config.Load()
	if got.Port != 9999 || got.Users["admin"].Hash == "" || !got.Users["admin"].Admin {
		t.Fatalf("config after install: %+v", got)
	}
	if got.DataDir != dataDir {
		t.Fatalf("dataDir not persisted: %q want %q", got.DataDir, dataDir)
	}
}

// TestInstallRemovesOldCache checks that a fresh install drops a leftover disposable
// cache, so stale rows from a previous install never carry over.
func TestInstallRemovesOldCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cachePath, err := db.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New()
	h := s.handler()
	dataDir := t.TempDir()
	body := `{"username":"admin","password":"pw","port":9999,"dataDir":"` + dataDir + `"}`
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/install", strings.NewReader(body)))
	if rr.Code != 200 {
		t.Fatalf("install: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("old cache should be removed on install, stat err = %v", err)
	}
}

func TestInstallRejectsBadDataDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := New()
	h := s.handler()
	for _, body := range []string{
		`{"username":"admin","password":"pw","port":9999}`,                        // missing dataDir
		`{"username":"admin","password":"pw","port":9999,"dataDir":"/no/such/x"}`, // nonexistent
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/install", strings.NewReader(body)))
		if rr.Code != 400 {
			t.Fatalf("install %q: %d, want 400", body, rr.Code)
		}
	}
	if config.Exists() {
		t.Fatal("config should not be written on bad dataDir")
	}
}

func TestBrowse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := New()
	h := s.handler()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "movies"), 0o755); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/install/browse?path="+root, nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"name":"movies"`) {
		t.Fatalf("browse: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"parent":"`+filepath.Dir(root)+`"`) {
		t.Fatalf("browse parent missing: %s", rr.Body.String())
	}

	// With no path, browse defaults to the app's current working directory.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/install/browse", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"path":"`+wd+`"`) {
		t.Fatalf("browse default: %d %s, want path %s", rr.Code, rr.Body.String(), wd)
	}
}

func TestLoginMeLogout(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.DefaultCost)
	s := New()
	s.cfg = &config.Config{Port: 8080, Users: map[string]config.User{"admin": {Hash: string(hash), Admin: true}}}
	h := s.handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"admin","password":"pw"}`)))
	if rr.Code != 200 {
		t.Fatalf("login: %d %s", rr.Code, rr.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie set")
	}

	me := httptest.NewRequest("GET", "/api/me", nil)
	me.AddCookie(cookie)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, me)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"admin":true`) {
		t.Fatalf("me: %d %s", rr.Code, rr.Body.String())
	}

	lo := httptest.NewRequest("POST", "/api/logout", nil)
	lo.AddCookie(cookie)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, lo)
	if rr.Code != 204 {
		t.Fatalf("logout: %d", rr.Code)
	}

	me2 := httptest.NewRequest("GET", "/api/me", nil)
	me2.AddCookie(cookie)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, me2)
	if rr.Code != 401 {
		t.Fatalf("me after logout: %d, want 401", rr.Code)
	}
}

func TestBadLogin(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.DefaultCost)
	s := New()
	s.cfg = &config.Config{Port: 8080, Users: map[string]config.User{"admin": {Hash: string(hash), Admin: true}}}
	h := s.handler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"admin","password":"wrong"}`)))
	if rr.Code != 401 {
		t.Fatalf("bad login: %d, want 401", rr.Code)
	}
}

// installedServer builds an app-mode server with an admin and a plain user, plus a
// session for each, so admin-route tests can authenticate.
func installedServer(t *testing.T, dataDir string) (*Server, http.Handler, *http.Cookie, *http.Cookie) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // isolate the SQLite cache per test
	s := New()
	s.cfg = &config.Config{
		Port:    8080,
		DataDir: dataDir,
		Users: map[string]config.User{
			"admin": {Hash: "x", Admin: true},
			"bob":   {Hash: "x", Admin: false},
		},
	}
	adminID, _ := s.sessions.create("admin")
	bobID, _ := s.sessions.create("bob")
	mk := func(id string) *http.Cookie { return &http.Cookie{Name: sessionCookie, Value: id} }
	return s, s.handler(), mk(adminID), mk(bobID)
}

// categoryRowID reads a category's id from the cache, or -1 if absent.
func categoryRowID(t *testing.T, s *Server, name string) int64 {
	t.Helper()
	s.mu.RLock()
	pool := s.db
	s.mu.RUnlock()
	if pool == nil {
		t.Fatal("cache pool not opened")
	}
	var id int64
	if err := pool.QueryRowContext(context.Background(),
		"SELECT id FROM categories WHERE name = ?", name).Scan(&id); err != nil {
		return -1
	}
	return id
}

func TestCategoryAPI(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, bob := installedServer(t, dataDir)

	do := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// Non-admin is forbidden.
	if rr := do("GET", "/api/admin/categories", "", bob); rr.Code != 403 {
		t.Fatalf("non-admin list: %d, want 403", rr.Code)
	}

	// Create: writes config.json with an id and mirrors a row into the cache.
	if rr := do("POST", "/api/admin/categories", `{"name":"Movies","alias":"Films"}`, admin); rr.Code != 200 ||
		!strings.Contains(rr.Body.String(), `"id":1`) {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "Movies", "config.json")); err != nil {
		t.Fatalf("config.json not written: %v", err)
	}

	// Invalid name is rejected.
	if rr := do("POST", "/api/admin/categories", `{"name":"a/b"}`, admin); rr.Code != 400 {
		t.Fatalf("invalid name: %d, want 400", rr.Code)
	}

	// List shows the created category.
	if rr := do("GET", "/api/admin/categories", "", admin); rr.Code != 200 ||
		!strings.Contains(rr.Body.String(), `"name":"Movies"`) ||
		!strings.Contains(rr.Body.String(), `"alias":"Films"`) {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}

	// Edit the alias: updates both config.json and the cache row.
	if rr := do("PUT", "/api/admin/categories/Movies", `{"alias":"Cinema"}`, admin); rr.Code != 204 {
		t.Fatalf("set alias: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do("GET", "/api/admin/categories", "", admin); !strings.Contains(rr.Body.String(), `"alias":"Cinema"`) {
		t.Fatalf("alias not updated: %s", rr.Body.String())
	}
	if id := categoryRowID(t, s, "Movies"); id != 1 {
		t.Fatalf("cache row id = %d, want 1", id)
	}

	// A non-empty category cannot be deleted.
	if err := os.WriteFile(filepath.Join(dataDir, "Movies", "film.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rr := do("DELETE", "/api/admin/categories/Movies", "", admin); rr.Code != 400 {
		t.Fatalf("delete non-empty: %d, want 400", rr.Code)
	}

	// An empty category deletes cleanly, from both the filesystem and the cache.
	if rr := do("POST", "/api/admin/categories", `{"name":"Empty"}`, admin); rr.Code != 200 {
		t.Fatalf("create empty: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do("DELETE", "/api/admin/categories/Empty", "", admin); rr.Code != 204 {
		t.Fatalf("delete empty: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "Empty")); !os.IsNotExist(err) {
		t.Fatal("empty category should be gone after delete")
	}
	if id := categoryRowID(t, s, "Empty"); id != -1 {
		t.Fatalf("cache row should be gone after delete, got id %d", id)
	}
}

// A category already on disk (with its id in config.json) must be reconciled into a
// freshly built cache when an admin page is entered.
func TestReconcileOnAdminEntry(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := library.Create(dataDir, "", "Movies", "Films", 42, 0); err != nil {
		t.Fatal(err)
	}
	s, h, admin, _ := installedServer(t, dataDir)

	req := httptest.NewRequest("GET", "/api/admin/categories", nil)
	req.AddCookie(admin)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req) // entering an admin page builds + reconciles the cache
	if rr.Code != 200 {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	if id := categoryRowID(t, s, "Movies"); id != 42 {
		t.Fatalf("reconciled id = %d, want 42 (from config.json)", id)
	}
}

func TestAdminBrowse(t *testing.T) {
	dataDir := t.TempDir()
	_, h, admin, bob := installedServer(t, dataDir)
	if err := os.Mkdir(filepath.Join(dataDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "movie.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	get := func(q string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/admin/browse"+q, nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// Non-admin forbidden.
	if rr := get("?path="+dataDir, bob); rr.Code != 403 {
		t.Fatalf("non-admin browse: %d, want 403", rr.Code)
	}

	// Dirs only by default: the file is hidden.
	rr := get("?path="+dataDir, admin)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"name":"sub"`) || strings.Contains(rr.Body.String(), "movie.mkv") {
		t.Fatalf("browse dirs-only: %d %s", rr.Code, rr.Body.String())
	}

	// With files=true the file shows up.
	rr = get("?path="+dataDir+"&files=true", admin)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "movie.mkv") {
		t.Fatalf("browse with files: %d %s", rr.Code, rr.Body.String())
	}
}

func TestSettingsAndFormat(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // config.Save writes to ~/.filefin.json
	_, h, admin, bob := installedServer(t, t.TempDir())

	get := func(cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/admin/settings", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	setFormat := func(body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/admin/settings/format", strings.NewReader(body))
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// Non-admin forbidden on both.
	if rr := get(bob); rr.Code != 403 {
		t.Fatalf("non-admin get settings: %d, want 403", rr.Code)
	}
	if rr := setFormat(`{"format":"filefin"}`, bob); rr.Code != 403 {
		t.Fatalf("non-admin set format: %d, want 403", rr.Code)
	}

	// Before choosing, mediaFormat is empty.
	if rr := get(admin); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"mediaFormat":""`) {
		t.Fatalf("settings before choice: %d %s", rr.Code, rr.Body.String())
	}

	// Invalid value rejected.
	if rr := setFormat(`{"format":"vlc"}`, admin); rr.Code != 400 {
		t.Fatalf("bad format: %d, want 400", rr.Code)
	}

	// Set persists and shows up on the next get.
	if rr := setFormat(`{"format":"filefin"}`, admin); rr.Code != 200 ||
		!strings.Contains(rr.Body.String(), `"mediaFormat":"filefin"`) {
		t.Fatalf("set format: %d %s", rr.Code, rr.Body.String())
	}
	if rr := get(admin); !strings.Contains(rr.Body.String(), `"mediaFormat":"filefin"`) {
		t.Fatalf("settings after choice: %s", rr.Body.String())
	}

	// Permanent: a second set is rejected.
	if rr := setFormat(`{"format":"plex"}`, admin); rr.Code != 409 {
		t.Fatalf("second set: %d, want 409", rr.Code)
	}
}

func TestSetImportFolder(t *testing.T) {
	_, h, admin, bob := installedServer(t, t.TempDir())
	folder := t.TempDir()

	set := func(body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/admin/settings/import-folder", strings.NewReader(body))
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	if rr := set(`{"path":"`+folder+`"}`, bob); rr.Code != 403 {
		t.Fatalf("non-admin set import folder: %d, want 403", rr.Code)
	}
	// A non-existent / relative path is rejected.
	if rr := set(`{"path":"/no/such/dir/x"}`, admin); rr.Code != 400 {
		t.Fatalf("missing dir: %d, want 400", rr.Code)
	}
	if rr := set(`{"path":"relative"}`, admin); rr.Code != 400 {
		t.Fatalf("relative path: %d, want 400", rr.Code)
	}
	// A valid directory persists and is freely changeable.
	if rr := set(`{"path":"`+folder+`"}`, admin); rr.Code != 200 ||
		!strings.Contains(rr.Body.String(), `"importFolder":"`+folder+`"`) {
		t.Fatalf("set import folder: %d %s", rr.Code, rr.Body.String())
	}
	other := t.TempDir()
	if rr := set(`{"path":"`+other+`"}`, admin); rr.Code != 200 ||
		!strings.Contains(rr.Body.String(), `"importFolder":"`+other+`"`) {
		t.Fatalf("change import folder: %d %s", rr.Code, rr.Body.String())
	}
}
