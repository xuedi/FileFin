package importer

import (
	"testing"

	"filefin/internal/jellyfin"
)

// TestMetaFromJellyfin checks the NFO details map onto the Meta fields and that the
// folder is left unenriched so the OMDb enricher fills gaps later.
func TestMetaFromJellyfin(t *testing.T) {
	item := jellyfin.Item{
		Title: "The Matrix",
		Year:  1999,
		Details: jellyfin.Details{
			Description:   "A hacker learns the truth.",
			Release:       "1999-03-31",
			Runtime:       136,
			Directors:     []string{"Lana Wachowski", "Lilly Wachowski"},
			Writers:       []string{"The Wachowskis"},
			Rating:        "8.7",
			ContentRating: "R",
			Studio:        "Warner Bros.",
			UniqueIDs:     []jellyfin.UniqueID{{Type: "imdb", Value: "tt0133093"}},
			Actors:        []string{"Keanu Reeves (Neo)"},
			Genres:        []string{"Action", "Sci-Fi"},
		},
	}
	m := MetaFromJellyfin(item)

	if m.Title != "The Matrix" || m.Year != 1999 {
		t.Fatalf("title/year = %q/%d", m.Title, m.Year)
	}
	if m.Enriched {
		t.Fatal("a Jellyfin import must be left unenriched")
	}
	if m.Description != "A hacker learns the truth." {
		t.Fatalf("description = %q", m.Description)
	}
	want := map[string]string{
		"release":       "1999-03-31",
		"runtime":       "136",
		"directedBy":    "Lana Wachowski, Lilly Wachowski",
		"writtenBy":     "The Wachowskis",
		"contentRating": "R",
		"studio":        "Warner Bros.",
		"imdbId":        "tt0133093",
	}
	for k, v := range want {
		if m.Metadata[k] != v {
			t.Errorf("metadata[%q] = %q, want %q", k, m.Metadata[k], v)
		}
	}
	if m.Ratings["nfo"] != "8.7" {
		t.Errorf("ratings[nfo] = %q, want 8.7", m.Ratings["nfo"])
	}
	if len(m.Actors) != 1 || m.Actors[0] != "Keanu Reeves (Neo)" {
		t.Errorf("actors = %v", m.Actors)
	}
	if len(m.Genres) != 2 || m.Genres[0] != "action" || m.Genres[1] != "sci-fi" {
		t.Errorf("genres = %v (want lowercased)", m.Genres)
	}
}

// TestMetaFromJellyfinFallsBackToYear checks an empty premiered date falls back to the
// year for the release key, and a sparse item produces a minimal Meta.
func TestMetaFromJellyfinFallsBackToYear(t *testing.T) {
	m := MetaFromJellyfin(jellyfin.Item{Title: "Loose", Year: 1980})
	if m.Metadata["release"] != "1980" {
		t.Fatalf("release fallback = %q, want 1980", m.Metadata["release"])
	}
	if m.Enriched {
		t.Fatal("must be unenriched")
	}
}
