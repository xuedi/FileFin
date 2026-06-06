package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"filefin/internal/cache"
	"filefin/internal/config"
	"filefin/internal/scanner"
	"filefin/internal/state"
	"filefin/internal/transcode"
)

// adminServer builds a server over a temp data dir with one non-direct-play (.avi) file,
// an admin account "root" and a normal account "alice", and the optimizer enabled.
func adminServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	folder := filepath.Join(dir, "Films", "(2020) Test")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(folder, "(2020) Test.avi")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	scan, err := scanner.Scan(dir)
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
	cfg.DataDir = dir
	cfg.OptimizeEnabled = true
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	cfg.Users["root"] = config.User{Hash: string(hash), Admin: true}
	cfg.Users["alice"] = config.User{Hash: string(hash)}
	return New(cfg, store, transcode.Encoder{}, nil), src
}

func get(t *testing.T, srv *Server, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestMeReportsAdmin(t *testing.T) {
	srv, _ := adminServer(t)
	for user, wantAdmin := range map[string]bool{"root": true, "alice": false} {
		cookie := login(t, srv, `{"username":"`+user+`","password":"secret"}`)
		rec := get(t, srv, "/api/me", cookie)
		var me struct {
			User  string `json:"user"`
			Admin bool   `json:"admin"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
			t.Fatal(err)
		}
		if me.User != user || me.Admin != wantAdmin {
			t.Fatalf("/api/me for %s = %+v", user, me)
		}
	}
}

func TestAdminGuard(t *testing.T) {
	srv, _ := adminServer(t)
	if rec := get(t, srv, "/api/admin/summary", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: want 401, got %d", rec.Code)
	}
	alice := login(t, srv, `{"username":"alice","password":"secret"}`)
	if rec := get(t, srv, "/api/admin/summary", alice); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: want 403, got %d", rec.Code)
	}
	root := login(t, srv, `{"username":"root","password":"secret"}`)
	if rec := get(t, srv, "/api/admin/summary", root); rec.Code != http.StatusOK {
		t.Fatalf("admin: want 200, got %d", rec.Code)
	}
}

func TestOptimizerQueue(t *testing.T) {
	srv, src := adminServer(t)
	root := login(t, srv, `{"username":"root","password":"secret"}`)

	rec := get(t, srv, "/api/admin/optimizer", root)
	var q struct {
		Enabled bool            `json:"enabled"`
		Items   []optimizerItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatal(err)
	}
	if !q.Enabled || len(q.Items) != 1 || q.Items[0].State != "pending" {
		t.Fatalf("expected one pending item, got %+v", q)
	}

	// Plant the in-progress lock; the item must flip to active.
	opt, _ := transcode.OptimizedSibling(src)
	if err := os.WriteFile(opt+".tmp", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rec = get(t, srv, "/api/admin/optimizer", root)
	q.Items = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatal(err)
	}
	if len(q.Items) != 1 || q.Items[0].State != "active" {
		t.Fatalf("expected the item to be active, got %+v", q.Items)
	}
}

func TestAdminUsers(t *testing.T) {
	srv, src := adminServer(t)
	// Mark the one media as watched + favorite for root by writing state directly.
	folder := filepath.Dir(src)
	if err := srv.stateMgr.Update(folder, "root", func(st state.UserState) state.UserState {
		st.Watched = true
		st.Favorite = true
		return st
	}); err != nil {
		t.Fatal(err)
	}

	root := login(t, srv, `{"username":"root","password":"secret"}`)
	rec := get(t, srv, "/api/admin/users", root)
	var users []adminUser
	if err := json.Unmarshal(rec.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	byName := map[string]adminUser{}
	for _, u := range users {
		byName[u.User] = u
	}
	if len(users) != 2 {
		t.Fatalf("want both configured users, got %v", users)
	}
	if r := byName["root"]; !r.Admin || r.Completed != 1 || r.Favorites != 1 {
		t.Fatalf("root row wrong: %+v", r)
	}
	if a := byName["alice"]; a.Admin || a.Completed != 0 || a.Favorites != 0 {
		t.Fatalf("alice row wrong: %+v", a)
	}

	// Guarded like the rest of the admin surface.
	alice := login(t, srv, `{"username":"alice","password":"secret"}`)
	if rec := get(t, srv, "/api/admin/users", alice); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin users page: want 403, got %d", rec.Code)
	}
}

func TestAdminSummary(t *testing.T) {
	srv, _ := adminServer(t)
	root := login(t, srv, `{"username":"root","password":"secret"}`)
	rec := get(t, srv, "/api/admin/summary", root)
	var sum struct {
		Library   cache.Stats    `json:"library"`
		Users     map[string]int `json:"users"`
		Optimizer map[string]any `json:"optimizer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Library.Media != 1 || sum.Library.Files != 1 {
		t.Fatalf("library stats: %+v", sum.Library)
	}
	if sum.Users["total"] != 2 || sum.Users["admins"] != 1 {
		t.Fatalf("user stats: %+v", sum.Users)
	}
	if sum.Optimizer["enabled"] != true || sum.Optimizer["pending"].(float64) != 1 {
		t.Fatalf("optimizer stats: %+v", sum.Optimizer)
	}
}
