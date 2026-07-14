package main

import (
	"os"
	"path/filepath"
	"testing"

	"filefin/internal/config"
	"filefin/internal/importer"
	"filefin/internal/state"
)

// writeMeta seeds a media folder with a meta.json carrying one user's playback state.
func writeMeta(t *testing.T, dir, user string, us state.UserState) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := importer.NewManager()
	if _, err := mgr.Update(dir, func(m importer.Meta) importer.Meta {
		m.Title = filepath.Base(dir)
		m.State = map[string]state.UserState{user: us}
		return m
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRenameUser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataDir := t.TempDir()

	// Two folders hold "xuedi" state (one nested under a category), one holds an unrelated
	// user, one holds no state at all.
	writeMeta(t, filepath.Join(dataDir, "Movies", "A (2001)"), "xuedi",
		state.UserState{Watched: true, Rating: 8, Updated: 1000})
	writeMeta(t, filepath.Join(dataDir, "Shows", "Deep", "B (2002)"), "xuedi",
		state.UserState{Progress: &state.Pointer{File: "1x3", Seconds: 42}, Updated: 2000})
	writeMeta(t, filepath.Join(dataDir, "Movies", "C (2003)"), "david",
		state.UserState{Favorite: true, Updated: 3000})
	if err := os.MkdirAll(filepath.Join(dataDir, "Movies", "D (2004)"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Bare folder, no meta.json, must be skipped without error.

	if err := config.Save(&config.Config{
		Port:    8080,
		DataDir: dataDir,
		Users: map[string]config.User{
			"xuedi": {Hash: "h1", Admin: true, ID: 1},
			"david": {Hash: "h2", ID: 2},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Dry run must not touch anything.
	if err := renameUser("xuedi", "xuedi@beijingcode.org", true); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got, _ := config.Load(); got.Users["xuedi"].Hash == "" {
		t.Fatal("dry run renamed the config key")
	}

	if err := renameUser("Xuedi", "xuedi@beijingcode.org", false); err != nil { // case-insensitive match
		t.Fatalf("rename: %v", err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Users["xuedi"]; ok {
		t.Fatal("old config key still present")
	}
	nu, ok := got.Users["xuedi@beijingcode.org"]
	if !ok {
		t.Fatal("new config key missing")
	}
	if nu.Hash != "h1" || !nu.Admin || nu.ID != 1 {
		t.Fatalf("account fields not preserved: %+v", nu)
	}

	// The two xuedi folders are renamed, values (and Updated) preserved; david untouched.
	stA, _ := importer.LoadState(filepath.Join(dataDir, "Movies", "A (2001)"))
	if _, ok := stA["xuedi"]; ok {
		t.Fatal("old state key still present in A")
	}
	if u := stA["xuedi@beijingcode.org"]; !u.Watched || u.Rating != 8 || u.Updated != 1000 {
		t.Fatalf("A state not preserved: %+v", u)
	}
	stB, _ := importer.LoadState(filepath.Join(dataDir, "Shows", "Deep", "B (2002)"))
	if u := stB["xuedi@beijingcode.org"]; u.Progress == nil || u.Progress.File != "1x3" || u.Updated != 2000 {
		t.Fatalf("B state not preserved: %+v", u)
	}
	stC, _ := importer.LoadState(filepath.Join(dataDir, "Movies", "C (2003)"))
	if _, ok := stC["david"]; !ok {
		t.Fatal("unrelated david state was disturbed")
	}
}

func TestRenameUserRejects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataDir := t.TempDir()
	if err := config.Save(&config.Config{
		Port:    8080,
		DataDir: dataDir,
		Users: map[string]config.User{
			"xuedi":             {Hash: "h1", Admin: true, ID: 1},
			"xuedi@mailbox.org": {Hash: "h3", ID: 3},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := renameUser("nobody", "x@y.com", false); err == nil {
		t.Fatal("expected error for unknown account")
	}
	if err := renameUser("xuedi", "Xuedi@Mailbox.org", false); err == nil {
		t.Fatal("expected collision error against existing account")
	}
	// Config must be unchanged after the rejected renames.
	got, _ := config.Load()
	if _, ok := got.Users["xuedi"]; !ok {
		t.Fatal("rejected rename mutated the config")
	}
}
