package db

import (
	"os"
	"testing"
)

// TestMain isolates the cache path for every test in this package. db.Path derives from the
// OS cache dir (XDG_CACHE_HOME, falling back to HOME/.cache), so without this a test that
// opens the cache would write to the real ~/.cache/filefin/cache.db. Throwaway dirs for the
// whole package prevent that even when a test forgets to set XDG_CACHE_HOME itself.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "filefin-db-test-home-")
	if err != nil {
		panic(err)
	}
	cache, err := os.MkdirTemp("", "filefin-db-test-cache-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("XDG_CACHE_HOME", cache)
	code := m.Run()
	os.RemoveAll(home)
	os.RemoveAll(cache)
	os.Exit(code)
}
