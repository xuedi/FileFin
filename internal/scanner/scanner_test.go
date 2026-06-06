package scanner

import "testing"

func TestScan(t *testing.T) {
	scan, err := Scan("testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Issues) != 0 {
		t.Fatalf("unexpected issues: %v", scan.Issues)
	}
	if len(scan.Categories) != 2 {
		t.Fatalf("want 2 categories, got %d", len(scan.Categories))
	}

	// Categories are sorted: "Films - English" before "Shows - China".
	films := scan.Categories[0]
	if films.Name != "Films - English" || len(films.Media) != 1 {
		t.Fatalf("films: %+v", films)
	}
	movie := films.Media[0]
	if movie.Year != 1980 || movie.Title != "The Gods Must Be Crazy" {
		t.Fatalf("movie parse: year=%d title=%q", movie.Year, movie.Title)
	}
	if len(movie.Files) != 1 || movie.Files[0].Season != 0 {
		t.Fatalf("movie files: %+v", movie.Files)
	}
	if movie.Poster == "" {
		t.Fatalf("poster not detected")
	}
	if movie.Meta == nil || len(movie.Meta.Tags) != 1 || movie.Meta.Tags[0] != "comedy" {
		t.Fatalf("meta: %+v", movie.Meta)
	}

	show := scan.Categories[1].Media[0]
	if show.Title != "Firefly" || len(show.Files) != 2 {
		t.Fatalf("show: %+v", show)
	}
	if show.Files[0].Season != 1 || show.Files[0].Episode != 1 || show.Files[1].Episode != 2 {
		t.Fatalf("episode parse: %+v", show.Files)
	}
}

func TestMediaIDStable(t *testing.T) {
	a := MediaID("Films - English", "(1980) The Gods Must Be Crazy")
	b := MediaID("Films - English", "(1980) The Gods Must Be Crazy")
	if a != b || a == "" {
		t.Fatalf("id not stable: %q %q", a, b)
	}
}
