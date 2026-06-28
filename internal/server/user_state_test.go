package server

import (
	"context"
	"strings"
	"testing"

	"filefin/internal/db"
	"filefin/internal/importer"
)

// userStateRowCount returns how many user_state rows exist for a media id.
func userStateRowCount(t *testing.T, s *Server, id string) int {
	t.Helper()
	s.mu.RLock()
	pool := s.db
	s.mu.RUnlock()
	var n int
	if err := pool.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM user_state WHERE media_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestUserStateMirrorAndRebuild proves home is served from the mirror and that a full rebuild
// re-derives the mirror from meta.json (the rebuildable invariant).
func TestUserStateMirrorAndRebuild(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	id, dir := seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mp4",
		importer.Meta{Title: "The Matrix", Year: 1999})

	// Favorite + watch it through the handlers (the live writeState chokepoint).
	if rr := do(t, h, "POST", "/api/media/"+id+"/favorite", `{"favorite":true}`, admin); rr.Code != 204 {
		t.Fatalf("favorite: %d", rr.Code)
	}
	if rr := do(t, h, "POST", "/api/media/"+id+"/progress", `{"file":0,"position":950,"duration":1000}`, admin); rr.Code != 204 {
		t.Fatalf("progress: %d", rr.Code)
	}

	// The mirror row exists and home (cache-served) reflects both buckets.
	if n := userStateRowCount(t, s, id); n != 1 {
		t.Fatalf("user_state rows after writes = %d, want 1", n)
	}
	assertHome := func(stage string) {
		rr := do(t, h, "GET", "/api/home", "", admin)
		body := rr.Body.String()
		if rr.Code != 200 {
			t.Fatalf("%s home: %d %s", stage, rr.Code, body)
		}
		if strings.Count(body, `"id":"`+id+`"`) < 2 {
			t.Fatalf("%s: expected item in favorites and completed:\n%s", stage, body)
		}
	}
	assertHome("after writes")

	// Wipe the mirror entirely, then rebuild from disk: the state must come back, proving it
	// is re-derived from the authoritative meta.json and nothing lives only in the cache.
	s.mu.RLock()
	pool := s.db
	s.mu.RUnlock()
	if err := db.ClearUserState(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	if n := userStateRowCount(t, s, id); n != 0 {
		t.Fatalf("user_state not cleared: %d", n)
	}
	if rr := do(t, h, "POST", "/api/admin/rebuild", "", admin); rr.Code != 200 {
		t.Fatalf("rebuild: %d %s", rr.Code, rr.Body.String())
	}
	if n := userStateRowCount(t, s, id); n != 1 {
		t.Fatalf("user_state not re-derived by rebuild = %d, want 1", n)
	}
	assertHome("after rebuild")

	// Sanity: the state really is on disk in meta.json.
	if m, err := importer.ReadMeta(dir); err != nil || !m.State["admin"].Favorite || !m.State["admin"].Watched {
		t.Fatalf("meta.json state: %+v err=%v", m.State, err)
	}
}

// TestSearchWatchedOverlay checks the search results carry the per-user watched flag from the
// user_state join, not just from a freshly-seeded item.
func TestSearchWatchedOverlay(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	id, _ := seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mp4",
		importer.Meta{Title: "The Matrix", Year: 1999, Tags: []string{"action"}})

	// Before watching, a genre search returns it unwatched.
	rr := do(t, h, "GET", "/api/search?field=genre&q=action", "", admin)
	if !strings.Contains(rr.Body.String(), `"title":"The Matrix"`) || strings.Contains(rr.Body.String(), `"watched":true`) {
		t.Fatalf("pre-watch search: %s", rr.Body.String())
	}

	// Mark watched, then the same search reflects it via the overlay.
	if rr := do(t, h, "POST", "/api/media/"+id+"/progress", `{"file":0,"position":950,"duration":1000}`, admin); rr.Code != 204 {
		t.Fatalf("progress: %d", rr.Code)
	}
	rr = do(t, h, "GET", "/api/search?field=genre&q=action", "", admin)
	if !strings.Contains(rr.Body.String(), `"watched":true`) {
		t.Fatalf("post-watch search overlay missing watched: %s", rr.Body.String())
	}
}
