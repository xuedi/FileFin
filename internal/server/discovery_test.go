package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filefin/internal/db"
)

// TestDiscoverySweep drives the discovery tick end to end against a scratch data dir: a
// folder appearing on disk is discovered and recorded healthy, a corrupted meta.json is
// flagged, and a vanished folder is pruned from the cache.
func TestDiscoverySweep(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, _ := installedServer(t, dataDir)
	ctx := context.Background()

	// A category folder (writes config.json + a cache row).
	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Movies","alias":"Films"}`, admin); rr.Code != 200 {
		t.Fatalf("create category: %d %s", rr.Code, rr.Body.String())
	}
	pool, _ := s.ensureDB(ctx)

	// A media folder appears on disk, not yet in the cache.
	dir := filepath.Join(dataDir, "Movies", "(1966) Django")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "(1966) Django.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"title":"Django","year":1966}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// First sweep discovers it and records it healthy (and fully checked).
	s.discoveryTick(ctx)
	if n, _ := db.CountMedia(ctx, pool); n != 1 {
		t.Fatalf("media after discovery = %d, want 1", n)
	}
	if n, _ := db.CountUnhealthy(ctx, pool); n != 0 {
		t.Fatalf("unhealthy after discovery = %d, want 0", n)
	}
	if n, _ := db.CountUncheckedMedia(ctx, pool); n != 0 {
		t.Fatalf("unchecked after discovery = %d, want 0", n)
	}

	// Corrupting meta.json makes the next sweep flag the item meta_invalid.
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.discoveryTick(ctx)
	unhealthy, _ := db.ListUnhealthy(ctx, pool)
	if len(unhealthy) != 1 || !strings.Contains(unhealthy[0].Issues, healthMetaInvalid) {
		t.Fatalf("unhealthy after corrupt meta = %+v", unhealthy)
	}

	// The folder vanishes: the next sweep prunes the media and health rows.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	s.discoveryTick(ctx)
	if n, _ := db.CountMedia(ctx, pool); n != 0 {
		t.Fatalf("media after removal = %d, want 0", n)
	}
	if n, _ := db.CountUnhealthy(ctx, pool); n != 0 {
		t.Fatalf("unhealthy after removal = %d, want 0", n)
	}
}
