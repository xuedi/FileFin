package jellyfin

import (
	"os"
	"path/filepath"
	"sort"
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

// TestScan builds a small library with a foldered movie (movie.nfo + poster), a show
// with a Season 01/ of episode NFOs, and a loose movie file, then checks the scanner
// recognises each shape with the right title/year, season/episode, poster, and metadata.
func TestScan(t *testing.T) {
	root := t.TempDir()

	// Foldered movie with a movie.nfo and a poster.
	write(t, filepath.Join(root, "The Matrix (1999)", "The Matrix.mkv"), "x")
	write(t, filepath.Join(root, "The Matrix (1999)", "poster.jpg"), "img")
	write(t, filepath.Join(root, "The Matrix (1999)", "movie.nfo"), `<movie>
		<title>The Matrix</title>
		<year>1999</year>
		<plot>A hacker learns the truth.</plot>
		<runtime>136</runtime>
		<director>Lana Wachowski</director>
		<director>Lilly Wachowski</director>
		<genre>Action</genre>
		<genre>Sci-Fi</genre>
		<mpaa>R</mpaa>
		<studio>Warner Bros.</studio>
		<uniqueid type="imdb">tt0133093</uniqueid>
		<actor><name>Keanu Reeves</name><role>Neo</role></actor>
		<ratings><rating default="true"><value>8.7</value></rating></ratings>
	</movie>`)

	// Show with a tvshow.nfo and a Season 01 of two episode NFOs.
	write(t, filepath.Join(root, "Firefly (2002)", "tvshow.nfo"), `<tvshow>
		<title>Firefly</title>
		<year>2002</year>
		<plot>A crew on the edge.</plot>
	</tvshow>`)
	write(t, filepath.Join(root, "Firefly (2002)", "Season 01", "Firefly S01E01.mkv"), "x")
	write(t, filepath.Join(root, "Firefly (2002)", "Season 01", "Firefly S01E01.nfo"), `<episodedetails>
		<title>Serenity</title><season>1</season><episode>1</episode>
	</episodedetails>`)
	write(t, filepath.Join(root, "Firefly (2002)", "Season 01", "Firefly S01E02.mkv"), "x")

	// Loose movie file directly under the root, no NFO.
	write(t, filepath.Join(root, "(1980) The Gods Must Be Crazy.mp4"), "x")

	items, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]Item{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3: %+v", len(items), items)
	}

	mtx, ok := byTitle["The Matrix"]
	if !ok || mtx.Year != 1999 || mtx.IsShow || len(mtx.Files) != 1 {
		t.Fatalf("matrix item = %+v", mtx)
	}
	if filepath.Base(mtx.PosterPath) != "poster.jpg" {
		t.Fatalf("matrix poster = %q", mtx.PosterPath)
	}
	if mtx.Details.Description == "" || mtx.Details.Runtime != 136 || mtx.Details.Rating != "8.7" {
		t.Fatalf("matrix details = %+v", mtx.Details)
	}
	if mtx.Details.Studio != "Warner Bros." || len(mtx.Details.Directors) != 2 {
		t.Fatalf("matrix details = %+v", mtx.Details)
	}

	ff, ok := byTitle["Firefly"]
	if !ok || ff.Year != 2002 || !ff.IsShow || len(ff.Files) != 2 {
		t.Fatalf("firefly item = %+v", ff)
	}
	// Both episodes recognised: S01E01 from the NFO, S01E02 from the filename.
	var eps []int
	season := map[int]bool{}
	for _, f := range ff.Files {
		eps = append(eps, f.Episode)
		season[f.Season] = true
	}
	sort.Ints(eps)
	if len(eps) != 2 || eps[0] != 1 || eps[1] != 2 || !season[1] {
		t.Fatalf("firefly files = %+v", ff.Files)
	}

	gods, ok := byTitle["The Gods Must Be Crazy"]
	if !ok || gods.Year != 1980 || len(gods.Files) != 1 {
		t.Fatalf("loose movie item = %+v", gods)
	}
}

func TestScanEmpty(t *testing.T) {
	items, err := Scan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("empty library should yield no items, got %d", len(items))
	}
}
