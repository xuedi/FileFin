package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filefin/internal/importer"
	"filefin/internal/library"
)

func TestNaturalLess(t *testing.T) {
	// Each pair must order left-before-right; the reverse must not hold.
	pairs := [][2]string{
		{"E2", "E10"},
		{"E9", "E10"},
		{"E4", "E37"},
		{"Part 2", "Part 10"},
		{"S01E04", "S01E37"},
		{"E01", "E2"}, // leading zeros ignored: 1 < 2
		{"abc", "abd"},
		{"file", "file2"}, // shorter prefix sorts first
	}
	for _, p := range pairs {
		if !naturalLess(p[0], p[1]) {
			t.Errorf("naturalLess(%q, %q) = false, want true", p[0], p[1])
		}
		if naturalLess(p[1], p[0]) {
			t.Errorf("naturalLess(%q, %q) = true, want false", p[1], p[0])
		}
	}
}

// TestReadMediaFolderEpisodeOrder is the regression guard for the resume engine: Idx must
// follow (season, episode), not the lexical filename order, so "watch all previous" marks
// E4-E9 when E37 is reached instead of skipping them.
func TestReadMediaFolderEpisodeOrder(t *testing.T) {
	dataDir := t.TempDir()
	cat := library.Category{ID: 1, Name: "Shows"}
	folder := "My Show (2020)"
	dir := filepath.Join(dataDir, cat.Name, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Written in a deliberately lexical-unfriendly order.
	for _, ep := range []string{"S01E10", "S01E2", "S01E37", "S01E4", "S01E1"} {
		f := filepath.Join(dir, "My Show "+ep+".mkv")
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sm, ok := readMediaFolder(dataDir, cat, folder)
	if !ok {
		t.Fatal("readMediaFolder returned ok=false")
	}
	wantEp := []int{1, 2, 4, 10, 37}
	if len(sm.files) != len(wantEp) {
		t.Fatalf("got %d files, want %d", len(sm.files), len(wantEp))
	}
	for i, f := range sm.files {
		if f.Idx != i {
			t.Errorf("file %d: Idx = %d, want %d", i, f.Idx, i)
		}
		if f.Episode != wantEp[i] {
			t.Errorf("position %d: Episode = %d, want %d", i, f.Episode, wantEp[i])
		}
	}
}

// TestReadMediaFolderAddedDate covers where the "entered the library" date comes from: the
// meta.json field when it is there, and the folder's own mtime for a folder that predates the
// field, so an existing library sorts by something sensible before anything is rewritten.
func TestReadMediaFolderAddedDate(t *testing.T) {
	dataDir := t.TempDir()
	cat := library.Category{ID: 1, Name: "Movies"}

	seed := func(folder string, meta importer.Meta, writeMeta bool) string {
		dir := filepath.Join(dataDir, cat.Name, folder)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, folder+".mkv"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if writeMeta {
			if err := importer.WriteMeta(dir, meta); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	seed("(1999) The Matrix", importer.Meta{Title: "The Matrix", Year: 1999, Added: 1234567890}, true)
	sm, ok := readMediaFolder(dataDir, cat, "(1999) The Matrix")
	if !ok || sm.media.Added != 1234567890 {
		t.Fatalf("a recorded added date must win: %d", sm.media.Added)
	}

	// No meta.json at all: the oldest media file's mtime stands in - that is when the
	// importer copied it - while the folder's own mtime is deliberately newer, because later
	// poster and subtitle writes reset it long after the item arrived.
	dir := seed("(1994) Leon", importer.Meta{}, false)
	want := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, "(1994) Leon.mkv"), want, want); err != nil {
		t.Fatal(err)
	}
	touched := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(dir, touched, touched); err != nil {
		t.Fatal(err)
	}
	sm, ok = readMediaFolder(dataDir, cat, "(1994) Leon")
	if !ok || sm.media.Added != want.Unix() {
		t.Fatalf("added = %d, want the oldest media file mtime %d", sm.media.Added, want.Unix())
	}

	// The oldest of several files wins: a show whose later episodes arrived afterwards still
	// entered the library when its first one did.
	dir = seed("(2001) A Show", importer.Meta{}, false)
	for i, when := range []time.Time{
		time.Date(2019, 5, 5, 0, 0, 0, 0, time.UTC),
		time.Date(2021, 6, 6, 0, 0, 0, 0, time.UTC),
	} {
		f := filepath.Join(dir, fmt.Sprintf("(2001) A Show S01E%02d.mkv", i+1))
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(f, when, when); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(dir, "(2001) A Show.mkv")); err != nil {
		t.Fatal(err)
	}
	sm, ok = readMediaFolder(dataDir, cat, "(2001) A Show")
	if !ok || sm.media.Added != time.Date(2019, 5, 5, 0, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("added = %d, want the oldest of the two episodes", sm.media.Added)
	}
}
