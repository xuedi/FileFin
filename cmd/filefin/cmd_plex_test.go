package main

import (
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
