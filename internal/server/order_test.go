package server

import (
	"os"
	"path/filepath"
	"testing"

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
