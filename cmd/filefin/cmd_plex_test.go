package main

import (
	"errors"
	"testing"

	"filefin/internal/importer"
	"filefin/internal/model"
	"filefin/internal/omdb"
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

func TestEnrichWithOMDb(t *testing.T) {
	plexMeta := func(title string) importer.MetaContent {
		return importer.MetaContent{
			Title:       title,
			Description: "plex summary for " + title,
			Metadata:    []model.KV{{Key: "release", Value: "1999"}, {Key: "rating", Value: "7.0"}},
		}
	}
	items := []importer.Media{
		{Title: "Found", Year: 1999, Meta: plexMeta("Found")},
		{Title: "Missing", Year: 1980, Meta: plexMeta("Missing")},
	}

	// OMDb knows "Found" but not "Missing".
	calls := 0
	lookup := func(title string, year int) (*omdb.Movie, error) {
		calls++
		if title == "Found" {
			return &omdb.Movie{Title: title, Plot: "omdb plot", ImdbRating: "8.5", Type: "movie"}, nil
		}
		return nil, errors.New("not found")
	}

	enriched, fellBack := enrichWithOMDb(items, lookup)
	if enriched != 1 || fellBack != 1 {
		t.Fatalf("counts: enriched=%d fellBack=%d", enriched, fellBack)
	}

	// The found item now leads with OMDb's description and rating, with Plex filling
	// the gaps (its release date survives because OMDb omitted one).
	found := items[0].Meta
	if found.Description != "omdb plot" {
		t.Fatalf("found description: %q", found.Description)
	}
	rt := kvMap(found.Ratings)
	if rt["imdb"] != "8.5" {
		t.Fatalf("found imdb rating: %+v", found.Ratings)
	}
	md := kvMap(found.Metadata)
	if md["release"] != "1999" { // OMDb omitted a release; Plex's fills the gap
		t.Fatalf("found release fallback: %+v", found.Metadata)
	}
	if md["rating"] != "7.0" { // a Plex-only key is carried over
		t.Fatalf("found carried plex rating: %+v", found.Metadata)
	}

	// The missing item is untouched: full Plex meta, run never aborted.
	missing := items[1].Meta
	if missing.Description != "plex summary for Missing" || len(missing.Ratings) != 0 {
		t.Fatalf("missing should keep plex meta: %+v", missing)
	}
}

func TestEnrichWithOMDbDedupes(t *testing.T) {
	items := []importer.Media{
		{Title: "Dup", Year: 2000, Meta: importer.MetaContent{Title: "Dup"}},
		{Title: "dup", Year: 2000, Meta: importer.MetaContent{Title: "dup"}}, // same key, different case
	}
	calls := 0
	lookup := func(title string, year int) (*omdb.Movie, error) {
		calls++
		return &omdb.Movie{Title: title, Type: "movie"}, nil
	}
	if enriched, _ := enrichWithOMDb(items, lookup); enriched != 2 {
		t.Fatalf("enriched: %d", enriched)
	}
	if calls != 1 {
		t.Fatalf("expected a single deduped lookup, got %d", calls)
	}
}
