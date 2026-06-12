package server

import (
	"os"
	"testing"
)

// TestMain isolates the whole package from the developer's real environment. Both the
// config (~/.filefin.json, via HOME) and the SQLite cache (via the OS cache dir) derive
// their paths from the environment, so a test that logs in or touches an admin route would
// otherwise overwrite the real config or cache. Pointing HOME and XDG_CACHE_HOME at
// throwaway dirs for every test makes that impossible even when an individual test forgets
// to isolate itself; per-test t.Setenv overrides still work on top of this.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "filefin-test-home-")
	if err != nil {
		panic(err)
	}
	cache, err := os.MkdirTemp("", "filefin-test-cache-")
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
