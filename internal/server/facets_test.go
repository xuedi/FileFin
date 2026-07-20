package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"filefin/internal/db"
	"filefin/internal/importer"
)

// mediaFacetValues returns the media_facets values for an id and kind.
func mediaFacetValues(t *testing.T, s *Server, id, kind string) []string {
	t.Helper()
	s.mu.RLock()
	pool := s.db
	s.mu.RUnlock()
	rows, err := pool.QueryContext(context.Background(),
		`SELECT value FROM media_facets WHERE media_id = ? AND kind = ? ORDER BY value`, id, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestFacetDenormalizationOnDiscovery(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, _ := installedServer(t, dataDir)
	ctx := context.Background()
	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Movies","alias":"Films"}`, admin); rr.Code != 200 {
		t.Fatalf("create category: %d %s", rr.Code, rr.Body.String())
	}
	pool, _ := s.ensureDB(ctx)

	dir := filepath.Join(dataDir, "Movies", "(1999) The Matrix")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "(1999) The Matrix.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := importer.WriteMeta(dir, importer.Meta{
		Title: "The Matrix", Year: 1999,
		Metadata: map[string]string{"language": "English", "origin": "USA", "directedBy": "The Wachowskis", "writtenBy": "The Wachowskis"},
		Actors:   []string{"Keanu Reeves", "Carrie-Anne Moss"},
		Genres:   []string{"action", "sci-fi"},
		Tags:     []string{"rewatch"},
	}); err != nil {
		t.Fatal(err)
	}

	// Discovery picks up the new folder and denormalizes its facets.
	s.discoveryTick(ctx)
	id := mediaID("Movies", "(1999) The Matrix")
	m, err := db.GetMedia(ctx, pool, id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Language != "English" || m.Country != "USA" || m.Director != "The Wachowskis" || m.Writer != "The Wachowskis" {
		t.Fatalf("scalar facets not denormalized: %+v", m)
	}
	if got := mediaFacetValues(t, s, id, "actor"); len(got) != 2 {
		t.Fatalf("actor facets: %v", got)
	}
	if got := mediaFacetValues(t, s, id, "genre"); len(got) != 2 || got[0] != "action" {
		t.Fatalf("genre facets: %v", got)
	}
	if got := mediaFacetValues(t, s, id, "tag"); len(got) != 1 || got[0] != "rewatch" {
		t.Fatalf("tag facets: %v", got)
	}

	// Changing meta.json (a new fingerprint) makes the next sweep refresh the facets.
	if err := importer.WriteMeta(dir, importer.Meta{
		Title: "The Matrix", Year: 1999,
		Metadata: map[string]string{"language": "French", "directedBy": "Someone Else"},
		Actors:   []string{"Neo"},
		Genres:   []string{"thriller"},
	}); err != nil {
		t.Fatal(err)
	}
	s.discoveryTick(ctx)
	m, _ = db.GetMedia(ctx, pool, id)
	if m.Language != "French" || m.Director != "Someone Else" {
		t.Fatalf("scalar facets not refreshed: %+v", m)
	}
	if got := mediaFacetValues(t, s, id, "actor"); len(got) != 1 || got[0] != "Neo" {
		t.Fatalf("actor facets not refreshed: %v", got)
	}
	if got := mediaFacetValues(t, s, id, "genre"); len(got) != 1 || got[0] != "thriller" {
		t.Fatalf("genre facets not refreshed: %v", got)
	}
	if got := mediaFacetValues(t, s, id, "tag"); len(got) != 0 {
		t.Fatalf("tag facets not refreshed: %v", got)
	}
}

func TestFacetBackfill(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, _ := installedServer(t, dataDir)
	ctx := context.Background()
	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Movies","alias":"Films"}`, admin); rr.Code != 200 {
		t.Fatalf("create category: %d %s", rr.Code, rr.Body.String())
	}
	pool, _ := s.ensureDB(ctx)

	// A media folder on disk with facet-rich meta.json.
	dir := filepath.Join(dataDir, "Movies", "(1994) Leon")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "(1994) Leon.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := importer.WriteMeta(dir, importer.Meta{
		Title: "Leon", Year: 1994, Metadata: map[string]string{"language": "French"},
		Actors: []string{"Jean Reno"}, Genres: []string{"thriller"},
	}); err != nil {
		t.Fatal(err)
	}
	// Insert the cache row WITHOUT facets, simulating a pre-upgrade cache, and reset the
	// data version so the backfill is due.
	id := mediaID("Movies", "(1994) Leon")
	if err := db.InsertMedia(ctx, pool, db.Media{ID: id, CategoryID: categoryRowID(t, s, "Movies"), Path: dir, Year: 1994, Title: "Leon"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSchemaVersion(ctx, pool, 0); err != nil {
		t.Fatal(err)
	}

	// Re-entering the cache runs the one-time backfill from meta.json.
	s.backfillCache(ctx, pool, dataDir)
	m, _ := db.GetMedia(ctx, pool, id)
	if m.Language != "French" {
		t.Fatalf("scalar facet not backfilled: %+v", m)
	}
	if got := mediaFacetValues(t, s, id, "actor"); len(got) != 1 || got[0] != "Jean Reno" {
		t.Fatalf("actor facet not backfilled: %v", got)
	}
	// The version is now stamped, so a second pass is a no-op (no duplication).
	s.backfillCache(ctx, pool, dataDir)
	if got := mediaFacetValues(t, s, id, "actor"); len(got) != 1 {
		t.Fatalf("backfill ran twice, duplicated facets: %v", got)
	}
}
