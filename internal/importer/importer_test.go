package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"filefin/internal/ffprobe"
	"filefin/internal/omdb"
)

func TestMetaFromOMDb(t *testing.T) {
	mv := &omdb.Movie{
		Title: "Blade Runner", Released: "25 Jun 1982", Runtime: "117 min",
		Language: "English", Country: "United States, United Kingdom", Director: "Ridley Scott",
		Rated: "R", BoxOffice: "N/A", ImdbID: "tt0083658", ImdbRating: "8.1", ImdbVotes: "835,123",
		Genre: "Sci-Fi, Thriller", Actors: "Harrison Ford, Rutger Hauer",
		Ratings: []omdb.Rating{{Source: "Rotten Tomatoes", Value: "89%"}, {Source: "Metacritic", Value: "84/100"}},
		Plot:    "A blade runner hunts replicants.",
	}
	// The caller's title/year win over OMDb.
	m := MetaFromOMDb(mv, "Blade Runner", 1982)
	if m.Title != "Blade Runner" || m.Year != 1982 {
		t.Fatalf("title/year: %+v", m)
	}
	if m.Metadata["directedBy"] != "Ridley Scott" || m.Metadata["contentRating"] != "R" {
		t.Fatalf("metadata: %+v", m.Metadata)
	}
	if _, ok := m.Metadata["boxOffice"]; ok {
		t.Fatalf("N/A boxOffice should be dropped: %+v", m.Metadata)
	}
	if m.Ratings["imdb"] != "8.1 (835,123 votes)" || m.Ratings["rottenTomatoes"] != "89%" || m.Ratings["metacritic"] != "84/100" {
		t.Fatalf("ratings: %+v", m.Ratings)
	}
	if len(m.Genres) != 2 || m.Genres[0] != "sci-fi" {
		t.Fatalf("genres: %+v", m.Genres)
	}
	if len(m.Actors) != 2 {
		t.Fatalf("actors: %+v", m.Actors)
	}
}

func TestWriteMeta(t *testing.T) {
	dir := t.TempDir()
	m := StubMeta("The Matrix", 1999)
	m.Technical = &ffprobe.Technical{Duration: 8160, Container: "matroska", VideoCodec: "h264"}
	if err := WriteMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Meta
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "The Matrix" || got.Year != 1999 || got.Metadata["release"] != "1999" {
		t.Fatalf("meta: %+v", got)
	}
	if got.Technical == nil || got.Technical.VideoCodec != "h264" {
		t.Fatalf("technical: %+v", got.Technical)
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	want := []byte("hello world, this is a media file")
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out", "dst.bin")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	var lastCopied, lastTotal int64
	if err := CopyFile(src, dst, func(copied, total int64) { lastCopied, lastTotal = copied, total }); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != string(want) {
		t.Fatalf("copied content mismatch: %q %v", got, err)
	}
	if lastCopied != int64(len(want)) || lastTotal != int64(len(want)) {
		t.Fatalf("progress: copied %d total %d, want %d", lastCopied, lastTotal, len(want))
	}
	// No .part left behind.
	if _, err := os.Stat(dst + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part file should be gone")
	}
}

func TestMergeMetaIsAdditive(t *testing.T) {
	base := Meta{
		Title: "Alpha", Year: 2001, Description: "plex summary",
		Metadata: map[string]string{"release": "2001", "directedBy": "Jane"},
		Ratings:  map[string]string{"plex": "8.0"},
		Actors:   []string{"Jane", "Joe"},
	}
	add := Meta{
		Title: "Alpha", Year: 2001, Description: "omdb plot",
		Metadata: map[string]string{"release": "2001-06-25", "language": "English"},
		Ratings:  map[string]string{"imdb": "8.1", "plex": "9.9"},
		Actors:   []string{"Someone Else"},
		Genres:   []string{"drama"},
	}
	got := MergeMeta(base, add)

	// Existing values win; only gaps are filled.
	if got.Description != "plex summary" {
		t.Fatalf("description overwritten: %q", got.Description)
	}
	if got.Metadata["release"] != "2001" {
		t.Fatalf("release overwritten: %q", got.Metadata["release"])
	}
	if got.Metadata["directedBy"] != "Jane" || got.Metadata["language"] != "English" {
		t.Fatalf("metadata merge wrong: %+v", got.Metadata)
	}
	if got.Ratings["plex"] != "8.0" || got.Ratings["imdb"] != "8.1" {
		t.Fatalf("ratings merge wrong: %+v", got.Ratings)
	}
	if len(got.Actors) != 2 || got.Actors[0] != "Jane" {
		t.Fatalf("actors should be kept, not replaced: %+v", got.Actors)
	}
	if len(got.Genres) != 1 || got.Genres[0] != "drama" {
		t.Fatalf("missing genres should be filled: %+v", got.Genres)
	}
}
