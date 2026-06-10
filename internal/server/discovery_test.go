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

// TestProbeRefill checks the probe agent's self-heal candidacy: a media item whose cache
// format columns are empty (the rebuild/reconcile shape) is queued for probing, and the
// queue clears only once both the format columns and the meta.json technical block are
// complete.
func TestProbeRefill(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, _ := installedServer(t, dataDir)
	ctx := context.Background()

	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Movies","alias":"Films"}`, admin); rr.Code != 200 {
		t.Fatalf("create category: %d %s", rr.Code, rr.Body.String())
	}
	pool, _ := s.ensureDB(ctx)

	dir := filepath.Join(dataDir, "Movies", "(1966) Django")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "(1966) Django.avi"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	// meta.json without a technical block, like a folder imported before probing landed.
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"title":"Django","year":1966}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// The sweep discovers it (empty format columns) and queues a probe task.
	s.discoveryTick(ctx)
	if n, _ := db.MediaMissingFormat(ctx, pool); len(n) != 1 {
		t.Fatalf("missing-format ids = %v, want 1", n)
	}
	if n, _ := db.CountPendingProbe(ctx, pool); n != 1 {
		t.Fatalf("pending probe after sweep = %d, want 1", n)
	}

	// Filling only the format columns is not enough: the meta.json still lacks technical,
	// so the item stays a candidate.
	id := mediaID("Movies", "(1966) Django")
	if err := db.SetMediaFileFormat(ctx, pool, id, 0, "mov,mp4,m4a,3gp,3g2,mj2", "h264", "aac"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.refillProbe(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountPendingProbe(ctx, pool); n != 1 {
		t.Fatalf("pending probe with incomplete meta = %d, want 1", n)
	}

	// Writing a complete technical block clears the candidacy and prunes the task.
	complete := `{"title":"Django","year":1966,"technical":{"container":"mov,mp4,m4a,3gp,3g2,mj2","videoCodec":"h264","audioCodec":"aac"}}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(complete), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.refillProbe(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountPendingProbe(ctx, pool); n != 0 {
		t.Fatalf("pending probe after complete = %d, want 0", n)
	}
}
