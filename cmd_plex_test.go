package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filefin/internal/plex"
)

func TestApplyRemap(t *testing.T) {
	remaps := parseRemaps([]string{"/source/plex=/tmp/media", "bad", "=skip"})
	if len(remaps) != 1 {
		t.Fatalf("parseRemaps: %+v", remaps)
	}
	got := applyRemap("/source/plex/films/x.avi", remaps)
	if got != "/tmp/media/films/x.avi" {
		t.Fatalf("remap: %q", got)
	}
	if applyRemap("/other/path.avi", remaps) != "/other/path.avi" {
		t.Fatal("non-matching path should be unchanged")
	}
}

func TestMetaFromPlex(t *testing.T) {
	mc := metaFromPlex(plex.Item{
		Title: "Blade Runner", Year: 1982, Summary: "A blade runner.",
		Release: "1982-06-25", Runtime: 117, Rating: "8.1", ContentRating: "R",
		Directors: []string{"Ridley Scott"}, Genres: []string{"Sci-Fi", "Thriller"},
		Actors: []string{"Harrison Ford"},
	})
	if mc.Title != "Blade Runner" || mc.Description != "A blade runner." {
		t.Fatalf("title/desc: %+v", mc)
	}
	meta := map[string]string{}
	for _, kv := range mc.Metadata {
		meta[kv.Key] = kv.Value
	}
	if meta["release"] != "1982-06-25" || meta["runtime"] != "117" || meta["directedBy"] != "Ridley Scott" || meta["rating"] != "8.1" {
		t.Fatalf("metadata: %+v", mc.Metadata)
	}
	if len(mc.Tags) != 2 || mc.Tags[0] != "sci-fi" {
		t.Fatalf("tags: %+v", mc.Tags)
	}
}

func TestApplyPlexItem(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "blade.avi")
	os.WriteFile(src, []byte("MOVIE"), 0o644)
	poster := filepath.Join(dir, "poster_src")
	os.WriteFile(poster, []byte("JPEGDATA"), 0o644)
	data := filepath.Join(dir, "data")

	it := plex.Item{
		Section: "x_Cyberpunk", Title: "Blade Runner", Year: 1982,
		Summary: "A blade runner.", Genres: []string{"Sci-Fi"},
		PosterPath: poster,
		Files:      []plex.SourceFile{{Path: src}},
	}
	if err := applyPlexItem(it, data, false, true); err != nil {
		t.Fatal(err)
	}

	folder := filepath.Join(data, "x_Cyberpunk", "(1982) Blade Runner")
	if b, err := os.ReadFile(filepath.Join(folder, "(1982) Blade Runner.avi")); err != nil || string(b) != "MOVIE" {
		t.Fatalf("media: %q err=%v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(folder, "poster.jpg")); err != nil || string(b) != "JPEGDATA" {
		t.Fatalf("poster: %q err=%v", b, err)
	}
	meta, err := os.ReadFile(filepath.Join(folder, "meta.md"))
	if err != nil || !strings.Contains(string(meta), "A blade runner.") || !strings.Contains(string(meta), "sci-fi") {
		t.Fatalf("meta: %q err=%v", meta, err)
	}
}

func TestApplyPlexItemEpisodes(t *testing.T) {
	dir := t.TempDir()
	mk := func(n string) string {
		p := filepath.Join(dir, n)
		os.WriteFile(p, []byte("x"), 0o644)
		return p
	}
	data := filepath.Join(dir, "data")
	it := plex.Item{
		Section: "Shows", IsShow: true, Title: "Firefly", Year: 2002,
		Files: []plex.SourceFile{
			{Path: mk("e1.avi"), Season: 1, Episode: 1},
			{Path: mk("e2.avi"), Season: 1, Episode: 2},
		},
	}
	if err := applyPlexItem(it, data, false, false); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(data, "Shows", "(2002) Firefly")
	for _, name := range []string{"(2002) Firefly - 1x1.avi", "(2002) Firefly - 1x2.avi", "meta.md"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
}
