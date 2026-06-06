package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"filefin/internal/cache"
	"filefin/internal/config"
	"filefin/internal/scanner"
	"filefin/internal/state"
	"filefin/internal/transcode"
)

// tempDataServer builds a server over a fresh, writable data dir holding one single-file
// movie, so progress writes land in a temp folder rather than the repo's testdata.
func tempDataServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	folder := filepath.Join(dir, "Films", "(2020) Test")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "(2020) Test.mp4"), []byte("x"), 0o644); err != nil {
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
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	cfg.Users["alice"] = config.User{Hash: string(hash)}
	return New(cfg, store, transcode.Encoder{}, nil), folder
}

func TestProgressRecordsAndReflects(t *testing.T) {
	srv, folder := tempDataServer(t)
	cookie := login(t, srv, `{"username":"alice","password":"secret"}`)
	id := scanner.MediaID("Films", "(2020) Test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/media/"+id+"/progress",
		strings.NewReader(`{"file":0,"position":950,"duration":1000,"event":"ended"}`))
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("progress post: %d %s", rec.Code, rec.Body.String())
	}

	// state.md must have been written into the media folder.
	all, err := state.Load(folder)
	if err != nil {
		t.Fatal(err)
	}
	if !all["alice"].Watched {
		t.Fatalf("alice should be watched: %#v", all["alice"])
	}

	// The detail view must reflect the watched state live.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/media/"+id, nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	var d cache.MediaDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if !d.Watched || !d.Files[0].Watched {
		t.Fatalf("detail should report watched: %#v", d)
	}
}

func TestContinueListsInProgress(t *testing.T) {
	srv, _ := tempDataServer(t)
	cookie := login(t, srv, `{"username":"alice","password":"secret"}`)
	id := scanner.MediaID("Films", "(2020) Test")

	// Nothing watched yet: continue list is empty.
	rec := get(t, srv, "/api/continue", cookie)
	if rec.Body.String() != "[]\n" {
		t.Fatalf("expected empty continue list, got %q", rec.Body.String())
	}

	// Start (but do not finish) the film: it must appear in continue.
	post := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/media/"+id+"/progress",
		strings.NewReader(`{"file":0,"position":100,"duration":1000,"event":"pause"}`))
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(post, req)

	rec = get(t, srv, "/api/continue", cookie)
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["id"] != id {
		t.Fatalf("expected the started film in continue, got %v", list)
	}

	// Finish it (>=90%): a watched folder drops out of continue.
	post = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/media/"+id+"/progress",
		strings.NewReader(`{"file":0,"position":950,"duration":1000,"event":"ended"}`))
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(post, req)

	rec = get(t, srv, "/api/continue", cookie)
	if rec.Body.String() != "[]\n" {
		t.Fatalf("watched film should leave continue, got %q", rec.Body.String())
	}
}

func TestFavoriteToggleAndList(t *testing.T) {
	srv, folder := tempDataServer(t)
	cookie := login(t, srv, `{"username":"alice","password":"secret"}`)
	id := scanner.MediaID("Films", "(2020) Test")

	send := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		var rdr *strings.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		} else {
			rdr = strings.NewReader("")
		}
		req := httptest.NewRequest(method, path, rdr)
		req.AddCookie(cookie)
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	// Favorite it.
	if rec := send("POST", "/api/media/"+id+"/favorite", `{"favorite":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("favorite post: %d %s", rec.Code, rec.Body.String())
	}
	all, err := state.Load(folder)
	if err != nil {
		t.Fatal(err)
	}
	if !all["alice"].Favorite {
		t.Fatalf("alice should be favorite: %#v", all["alice"])
	}

	// It must appear in /api/favorites and in the detail view.
	rec := get(t, srv, "/api/favorites", cookie)
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["id"] != id {
		t.Fatalf("favorites list: %v", list)
	}
	rec = get(t, srv, "/api/media/"+id, cookie)
	var d cache.MediaDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if !d.Favorite {
		t.Fatalf("detail should report favorite: %#v", d)
	}

	// Unfavorite it: the list empties.
	if rec := send("POST", "/api/media/"+id+"/favorite", `{"favorite":false}`); rec.Code != http.StatusNoContent {
		t.Fatalf("unfavorite post: %d", rec.Code)
	}
	if rec := get(t, srv, "/api/favorites", cookie); rec.Body.String() != "[]\n" {
		t.Fatalf("favorites should be empty, got %q", rec.Body.String())
	}
}

func TestClearProgressRemovesFromContinue(t *testing.T) {
	srv, _ := tempDataServer(t)
	cookie := login(t, srv, `{"username":"alice","password":"secret"}`)
	id := scanner.MediaID("Films", "(2020) Test")

	// Start watching so it lands in continue.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/media/"+id+"/progress",
		strings.NewReader(`{"file":0,"position":100,"duration":1000,"event":"pause"}`))
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec := get(t, srv, "/api/continue", cookie); rec.Body.String() == "[]\n" {
		t.Fatal("expected the item in continue before clearing")
	}

	// Clear progress: it leaves continue.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("DELETE", "/api/media/"+id+"/progress", nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete progress: %d", rec.Code)
	}
	if rec := get(t, srv, "/api/continue", cookie); rec.Body.String() != "[]\n" {
		t.Fatalf("continue should be empty after clear, got %q", rec.Body.String())
	}
}

func TestProgressRequiresAuth(t *testing.T) {
	srv, _ := tempDataServer(t)
	id := scanner.MediaID("Films", "(2020) Test")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/media/"+id+"/progress",
		strings.NewReader(`{"file":0,"position":1,"duration":10}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}
