package main

import (
	"os"
	"path/filepath"
	"testing"

	"filefin/internal/cache"
	"filefin/internal/config"
)

// TestReloadLibrary covers the reload code path serve runs on SIGHUP: a second call on the
// same open store re-parses the data dir and atomically repopulates the cache, so new media
// appears without reopening the store (or restarting the process).
func TestReloadLibrary(t *testing.T) {
	dataDir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.sqlite")
	cfg := &config.Config{DataDir: dataDir, CachePath: cachePath}

	store, err := cache.Open(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if n, err := reloadLibrary(cfg, store); err != nil || n != 0 {
		t.Fatalf("reloadLibrary on empty library = %d (err=%v), want 0", n, err)
	}
	if st, _ := store.Stats(); st.Media != 0 {
		t.Fatalf("cache media before add = %d, want 0", st.Media)
	}

	folder := filepath.Join(dataDir, "Films", "(1966) Django")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "(1966) Django.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if n, err := reloadLibrary(cfg, store); err != nil || n != 1 {
		t.Fatalf("reloadLibrary after add = %d (err=%v), want 1", n, err)
	}
	if st, _ := store.Stats(); st.Media != 1 {
		t.Fatalf("cache media after reload = %d, want 1", st.Media)
	}
}
