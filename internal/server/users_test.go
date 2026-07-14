package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"filefin/internal/config"
	"filefin/internal/db"
)

// userByName finds a user row in a /api/admin/users listing.
func userByName(t *testing.T, rr *httptest.ResponseRecorder, name string) userDTO {
	t.Helper()
	var list []userDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode users: %v (%s)", err, rr.Body.String())
	}
	for _, u := range list {
		if u.Username == name {
			return u
		}
	}
	t.Fatalf("user %q not found in %s", name, rr.Body.String())
	return userDTO{}
}

func TestUserManagement(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // config.Save writes ~/.filefin.json
	_, h, admin, bob := installedServer(t, t.TempDir())

	// Non-admin is forbidden on every user route.
	if rr := do(t, h, "GET", "/api/admin/users", "", bob); rr.Code != 403 {
		t.Fatalf("non-admin list: %d, want 403", rr.Code)
	}
	if rr := do(t, h, "POST", "/api/admin/users", `{"email":"x@y.com","password":"p"}`, bob); rr.Code != 403 {
		t.Fatalf("non-admin create: %d, want 403", rr.Code)
	}
	if rr := do(t, h, "PUT", "/api/admin/users/1", `{"blocked":true}`, bob); rr.Code != 403 {
		t.Fatalf("non-admin update: %d, want 403", rr.Code)
	}

	// Entering an admin route reconciled the seeded accounts and minted their ids.
	list := do(t, h, "GET", "/api/admin/users", "", admin)
	if list.Code != 200 {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	adminRow := userByName(t, list, "admin")
	if adminRow.ID == 0 || !adminRow.Admin {
		t.Fatalf("admin row = %+v (want minted id + admin)", adminRow)
	}

	// Create a user: it gets a minted id and can then log in.
	rr := do(t, h, "POST", "/api/admin/users",
		`{"email":"Carol@Example.com","alias":"Carol","password":"hunter22","admin":false}`, admin)
	if rr.Code != 200 {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var carol userDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &carol); err != nil {
		t.Fatal(err)
	}
	if carol.ID == 0 || carol.Username != "carol@example.com" || carol.Alias != "Carol" || carol.Admin {
		t.Fatalf("created user = %+v", carol)
	}

	// Duplicate email is rejected; bad email is rejected.
	if rr := do(t, h, "POST", "/api/admin/users", `{"email":"carol@example.com","password":"password1"}`, admin); rr.Code != 409 {
		t.Fatalf("duplicate: %d, want 409", rr.Code)
	}
	if rr := do(t, h, "POST", "/api/admin/users", `{"email":"nope","password":"p"}`, admin); rr.Code != 400 {
		t.Fatalf("bad email: %d, want 400", rr.Code)
	}

	// Carol can log in and gets a session.
	login := do(t, h, "POST", "/api/login", `{"username":"carol@example.com","password":"hunter22"}`, nil)
	if login.Code != 200 {
		t.Fatalf("carol login: %d %s", login.Code, login.Body.String())
	}
	var carolCookie *http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			carolCookie = c
		}
	}
	if carolCookie == nil {
		t.Fatal("carol got no session cookie")
	}
	if rr := do(t, h, "GET", "/api/me", "", carolCookie); rr.Code != 200 {
		t.Fatalf("carol me: %d", rr.Code)
	}

	// The email is matched case-insensitively (and space-trimmed): Carol logs in with the
	// casing she typed at creation, not only the lower-cased stored key, and the resulting
	// session still resolves on /api/me.
	for _, name := range []string{"Carol@Example.com", "  carol@example.com  ", "CAROL@EXAMPLE.COM"} {
		body := `{"username":` + strconv.Quote(name) + `,"password":"hunter22"}`
		lr := do(t, h, "POST", "/api/login", body, nil)
		if lr.Code != 200 {
			t.Fatalf("login as %q: %d %s", name, lr.Code, lr.Body.String())
		}
		var ck *http.Cookie
		for _, c := range lr.Result().Cookies() {
			if c.Name == sessionCookie && c.Value != "" {
				ck = c
			}
		}
		if ck == nil {
			t.Fatalf("login as %q: no session cookie", name)
		}
		if mr := do(t, h, "GET", "/api/me", "", ck); mr.Code != 200 {
			t.Fatalf("me after login as %q: %d", name, mr.Code)
		}
	}

	// Block Carol: her session is dropped immediately and she can no longer log in.
	cid := strconv.FormatInt(carol.ID, 10)
	if rr := do(t, h, "PUT", "/api/admin/users/"+cid, `{"blocked":true}`, admin); rr.Code != 200 {
		t.Fatalf("block: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do(t, h, "GET", "/api/me", "", carolCookie); rr.Code != 401 {
		t.Fatalf("blocked session still valid: %d, want 401", rr.Code)
	}
	if rr := do(t, h, "POST", "/api/login", `{"username":"carol@example.com","password":"hunter22"}`, nil); rr.Code != 401 {
		t.Fatalf("blocked login: %d, want 401", rr.Code)
	}

	// Unblock and edit alias; the change persists in the listing.
	if rr := do(t, h, "PUT", "/api/admin/users/"+cid, `{"blocked":false,"alias":"Caroline"}`, admin); rr.Code != 200 {
		t.Fatalf("unblock+alias: %d %s", rr.Code, rr.Body.String())
	}
	if got := userByName(t, do(t, h, "GET", "/api/admin/users", "", admin), "carol@example.com"); got.Blocked || got.Alias != "Caroline" {
		t.Fatalf("after edit = %+v", got)
	}
}

// TestUserReconcileAfterReinstall reproduces the "cache unavailable" bug: a leftover
// (disposable) cache holds user rows from before a reinstall, while the fresh config has
// an account of the same name with no id yet. Reconcile must adopt the stale row's id
// instead of inserting a colliding username, and prune rows the config no longer has.
func TestUserReconcileAfterReinstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, h, admin, _ := installedServer(t, t.TempDir())

	// First admin entry mints ids for the seeded accounts (admin, bob) into the mirror.
	if rr := do(t, h, "GET", "/api/admin/users", "", admin); rr.Code != 200 {
		t.Fatalf("initial list: %d %s", rr.Code, rr.Body.String())
	}

	// Simulate a reinstall: the config is replaced by a fresh single admin that has lost
	// its id (ID 0), while the cache mirror still holds the old 'admin' and 'bob' rows.
	s.mu.Lock()
	s.cfg.Users = map[string]config.User{"admin": {Hash: "x", Admin: true}}
	s.mu.Unlock()

	// Entering an admin route must reconcile cleanly (no UNIQUE collision -> no 5xx).
	rr := do(t, h, "GET", "/api/admin/users", "", admin)
	if rr.Code != 200 {
		t.Fatalf("reconcile after reinstall: %d %s", rr.Code, rr.Body.String())
	}
	if row := userByName(t, rr, "admin"); row.ID == 0 {
		t.Fatalf("admin should have adopted the stale mirror id, got %+v", row)
	}

	// The stale 'bob' row is pruned from the mirror.
	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	us, _ := db.ListUsers(ctx, pool)
	for _, u := range us {
		if u.Username == "bob" {
			t.Fatalf("stale 'bob' mirror row should be gone, got %+v", us)
		}
	}
}

func TestUserManagementGuardrails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, h, admin, _ := installedServer(t, t.TempDir())

	adminRow := userByName(t, do(t, h, "GET", "/api/admin/users", "", admin), "admin")
	aid := strconv.FormatInt(adminRow.ID, 10)

	// The sole admin cannot block or de-admin their own account.
	if rr := do(t, h, "PUT", "/api/admin/users/"+aid, `{"blocked":true}`, admin); rr.Code != 403 {
		t.Fatalf("self-block: %d, want 403", rr.Code)
	}
	if rr := do(t, h, "PUT", "/api/admin/users/"+aid, `{"admin":false}`, admin); rr.Code != 403 {
		t.Fatalf("self-de-admin: %d, want 403", rr.Code)
	}

	// Add a second admin, then de-admining the first (non-self) is allowed because an
	// active admin still remains.
	rr := do(t, h, "POST", "/api/admin/users", `{"email":"dee@example.com","password":"password1","admin":true}`, admin)
	if rr.Code != 200 {
		t.Fatalf("create admin: %d %s", rr.Code, rr.Body.String())
	}
	var dee userDTO
	_ = json.Unmarshal(rr.Body.Bytes(), &dee)
	did := strconv.FormatInt(dee.ID, 10)
	if rr := do(t, h, "PUT", "/api/admin/users/"+did, `{"blocked":true}`, admin); rr.Code != 200 {
		t.Fatalf("block second admin: %d %s", rr.Code, rr.Body.String())
	}

	// Unknown id is a 404.
	if rr := do(t, h, "PUT", "/api/admin/users/9999", `{"alias":"x"}`, admin); rr.Code != 404 {
		t.Fatalf("unknown id: %d, want 404", rr.Code)
	}
}
