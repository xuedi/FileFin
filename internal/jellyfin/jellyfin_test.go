package jellyfin

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFolderName(t *testing.T) {
	cases := []struct {
		in    string
		title string
		year  int
	}{
		{"The Matrix (1999)", "The Matrix", 1999},
		{"(1999) The Matrix", "The Matrix", 1999},
		{"Some Movie", "Some Movie", 0},
	}
	for _, c := range cases {
		title, year := parseFolderName(c.in)
		if title != c.title || year != c.year {
			t.Errorf("parseFolderName(%q)=%q,%d want %q,%d", c.in, title, year, c.title, c.year)
		}
	}
}

func TestScanMovie(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "The Matrix (1999)")
	write(t, filepath.Join(dir, "The Matrix (1999).mkv"), "VIDEO")
	write(t, filepath.Join(dir, "poster.jpg"), "IMG")
	write(t, filepath.Join(dir, "fanart.jpg"), "IMG")
	write(t, filepath.Join(dir, "movie.nfo"), `<?xml version="1.0"?>
<movie>
  <title>The Matrix</title>
  <year>1999</year>
  <premiered>1999-03-31</premiered>
  <plot>A hacker learns the truth.</plot>
  <runtime>136</runtime>
  <mpaa>R</mpaa>
  <genre>Action</genre>
  <genre>Science Fiction</genre>
  <director>Lana Wachowski</director>
  <director>Lilly Wachowski</director>
  <ratings><rating name="imdb" default="true"><value>8.7</value></rating></ratings>
  <uniqueid type="imdb" default="true">tt0133093</uniqueid>
  <actor><name>Keanu Reeves</name><role>Neo</role></actor>
</movie>`)

	items, err := Scan(root, "Films")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	m := items[0]
	if m.IsShow || m.Title != "The Matrix" || m.Year != 1999 || m.Category != "Films" {
		t.Fatalf("movie fields: %+v", m)
	}
	if len(m.Files) != 1 || m.PosterPath == "" || m.BannerPath == "" {
		t.Fatalf("files/art: files=%d poster=%q banner=%q", len(m.Files), m.PosterPath, m.BannerPath)
	}
	meta := map[string]string{}
	for _, kv := range m.Meta.Metadata {
		meta[kv.Key] = kv.Value
	}
	if meta["release"] != "1999-03-31" || meta["runtime"] != "136" || meta["rating"] != "8.7" || meta["imdbId"] != "tt0133093" {
		t.Fatalf("meta: %+v", m.Meta.Metadata)
	}
	if meta["directedBy"] != "Lana Wachowski, Lilly Wachowski" {
		t.Fatalf("directors: %q", meta["directedBy"])
	}
	if len(m.Meta.Actors) != 1 || m.Meta.Actors[0] != "Keanu Reeves (Neo)" {
		t.Fatalf("actors: %+v", m.Meta.Actors)
	}
	if len(m.Meta.Tags) != 2 || m.Meta.Tags[1] != "science fiction" {
		t.Fatalf("tags: %+v", m.Meta.Tags)
	}
}

func TestScanShow(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Firefly")
	write(t, filepath.Join(dir, "tvshow.nfo"), `<?xml version="1.0"?>
<tvshow><title>Firefly</title><year>2002</year><plot>Space western.</plot><genre>Sci-Fi</genre></tvshow>`)
	write(t, filepath.Join(dir, "poster.jpg"), "IMG")
	// Episode 1 with an NFO carrying the numbers.
	write(t, filepath.Join(dir, "Season 01", "Firefly S01E01.mkv"), "V")
	write(t, filepath.Join(dir, "Season 01", "Firefly S01E01.nfo"),
		`<episodedetails><title>Serenity</title><season>1</season><episode>1</episode></episodedetails>`)
	// Episode 2 with no NFO -> numbers parsed from the filename.
	write(t, filepath.Join(dir, "Season 01", "Firefly S01E02.mkv"), "V")

	items, err := Scan(root, "Shows")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 show, got %d", len(items))
	}
	s := items[0]
	if !s.IsShow || s.Title != "Firefly" || s.Year != 2002 || len(s.Files) != 2 {
		t.Fatalf("show: %+v", s)
	}
	got := map[string][2]int{}
	for _, f := range s.Files {
		got[filepath.Base(f.Path)] = [2]int{f.Season, f.Episode}
	}
	if got["Firefly S01E01.mkv"] != [2]int{1, 1} || got["Firefly S01E02.mkv"] != [2]int{1, 2} {
		t.Fatalf("episode numbers: %+v", got)
	}
}
