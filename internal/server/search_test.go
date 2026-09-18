package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"filefin/internal/importer"
)

func TestSearchMedia(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)

	seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mp4",
		importer.Meta{
			Title: "The Matrix", Year: 1999, Description: "A hacker learns the truth.",
			Metadata: map[string]string{"language": "English", "directedBy": "The Wachowskis"},
			Actors:   []string{"Keanu Reeves", "Carrie-Anne Moss"},
			Genres:   []string{"action", "sci-fi"},
			Tags:     []string{"rewatch"},
		})
	seedMedia(t, s, dataDir, "Movies", catID, "(1994) Leon", "(1994) Leon.mp4",
		importer.Meta{
			Title: "Leon", Year: 1994, Description: "A hitman and a girl.",
			Metadata: map[string]string{"language": "French", "directedBy": "Luc Besson"},
			Actors:   []string{"Jean Reno", "Natalie Portman"},
			Genres:   []string{"action", "thriller"},
			Tags:     []string{"rewatch", "subtitled"},
		})
	seedMedia(t, s, dataDir, "Movies", catID, "(2003) Oldboy", "(2003) Oldboy.mp4",
		importer.Meta{
			Title: "Oldboy", Year: 2003, Description: "A man seeks revenge.",
			Metadata: map[string]string{"language": "Korean", "directedBy": "Park Chan-wook"},
			Actors:   []string{"Choi Min-sik"},
			Genres:   []string{"thriller", "mystery"},
		})

	cases := []struct {
		name        string
		query       string
		wantTitles  []string
		notExpected []string
	}{
		{"all matches across fields", "q=revenge", []string{"Oldboy"}, []string{"The Matrix", "Leon"}},
		{"all is case-insensitive over actors", "q=reeves", []string{"The Matrix"}, []string{"Leon", "Oldboy"}},
		{"cast scope", "field=cast&q=reno", []string{"Leon"}, []string{"The Matrix", "Oldboy"}},
		{"genre scope", "field=genre&q=thriller", []string{"Leon", "Oldboy"}, []string{"The Matrix"}},
		{"tag scope", "field=tag&q=rewatch", []string{"The Matrix", "Leon"}, []string{"Oldboy"}},
		{"tag scope does not match a genre", "field=tag&q=thriller", nil, []string{"Leon", "Oldboy", "The Matrix"}},
		{"genre scope does not match a tag", "field=genre&q=rewatch", nil, []string{"The Matrix", "Leon", "Oldboy"}},
		{"all finds a curated tag", "q=subtitled", []string{"Leon"}, []string{"The Matrix", "Oldboy"}},
		{"language scope", "field=language&q=korean", []string{"Oldboy"}, []string{"The Matrix", "Leon"}},
		{"director scope", "field=director&q=besson", []string{"Leon"}, []string{"The Matrix", "Oldboy"}},
		{"year scope is exact", "field=year&q=1999", []string{"The Matrix"}, []string{"Leon", "Oldboy"}},
		{"decade scope is a range", "field=decade&q=1990s", []string{"Leon", "The Matrix"}, []string{"Oldboy"}},
		{"decade scope plain number", "field=decade&q=2000", []string{"Oldboy"}, []string{"The Matrix", "Leon"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := do(t, h, "GET", "/api/search?"+c.query, "", admin)
			if rr.Code != 200 {
				t.Fatalf("search %q: %d %s", c.query, rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			for _, want := range c.wantTitles {
				if !strings.Contains(body, `"title":"`+want+`"`) {
					t.Fatalf("search %q: expected %q in:\n%s", c.query, want, body)
				}
			}
			for _, no := range c.notExpected {
				if strings.Contains(body, `"title":"`+no+`"`) {
					t.Fatalf("search %q: did not expect %q in:\n%s", c.query, no, body)
				}
			}
		})
	}

	// An empty query returns an empty result set, never the whole library.
	rr := do(t, h, "GET", "/api/search?q=", "", admin)
	if rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatalf("empty query: %d %q", rr.Code, rr.Body.String())
	}
}

// searchTitles runs a search and returns the result titles in the order they came back, so a
// test can assert on ordering as well as membership.
func searchTitles(t *testing.T, h http.Handler, admin *http.Cookie, query string) []string {
	t.Helper()
	rr := do(t, h, "GET", "/api/search?"+query, "", admin)
	if rr.Code != 200 {
		t.Fatalf("search %q: %d %s", query, rr.Code, rr.Body.String())
	}
	var rows []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode search %q: %v (%s)", query, err, rr.Body.String())
	}
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

// TestSearchFiltersAndSorting covers the controls that let a home row's "more" tile reproduce
// its list: the playback-status scope, the favorites toggle and the four orderings. They work
// with no text at all, which is what makes "all media, newest first" a search.
func TestSearchFiltersAndSorting(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)

	seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mp4",
		importer.Meta{Title: "The Matrix", Year: 1999, Added: 300})
	leon, _ := seedMedia(t, s, dataDir, "Movies", catID, "(1994) Leon", "(1994) Leon.mp4",
		importer.Meta{Title: "Leon", Year: 1994, Added: 100})
	oldboy, _ := seedMedia(t, s, dataDir, "Movies", catID, "(2003) Oldboy", "(2003) Oldboy.mp4",
		importer.Meta{Title: "Oldboy", Year: 2003, Added: 200})

	// Leon is half-watched and a favorite; Oldboy is finished. The Matrix is untouched.
	if rr := do(t, h, "POST", "/api/media/"+leon+"/progress", `{"file":0,"position":300,"duration":1000}`, admin); rr.Code != 204 {
		t.Fatalf("progress: %d", rr.Code)
	}
	if rr := do(t, h, "POST", "/api/media/"+leon+"/favorite", `{"favorite":true}`, admin); rr.Code != 204 {
		t.Fatalf("favorite: %d", rr.Code)
	}
	if rr := do(t, h, "POST", "/api/media/"+oldboy+"/watched", `{"watched":true}`, admin); rr.Code != 204 {
		t.Fatalf("watched: %d", rr.Code)
	}

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"unwatched is never started and never finished", "status=unwatched", []string{"The Matrix"}},
		{"in progress is the continue shelf", "status=progress", []string{"Leon"}},
		{"watched is the completed shelf", "status=watched", []string{"Oldboy"}},
		{"favorites only", "fav=1", []string{"Leon"}},
		{"a filter narrows a text query too", "q=leon&status=watched", nil},
		{"sort by date added, newest first", "sort=added&dir=desc", []string{"The Matrix", "Oldboy", "Leon"}},
		{"sort by date added, oldest first", "sort=added", []string{"Leon", "Oldboy", "The Matrix"}},
		{"sort by title", "sort=title", []string{"Leon", "Oldboy", "The Matrix"}},
		{"sort by year, newest first", "sort=year&dir=desc", []string{"Oldboy", "The Matrix", "Leon"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := searchTitles(t, h, admin, c.query)
			if len(got) != len(c.want) {
				t.Fatalf("search %q = %v, want %v", c.query, got, c.want)
			}
			for i, w := range c.want {
				if got[i] != w {
					t.Fatalf("search %q = %v, want %v", c.query, got, c.want)
				}
			}
		})
	}

	// A search that asks for nothing at all stays empty, so a bare Enter cannot swap the home
	// rows for the whole library - but naming an order is asking for something.
	if got := searchTitles(t, h, admin, ""); len(got) != 0 {
		t.Fatalf("a search with no text, filter or order must be empty, got %v", got)
	}
	if got := searchTitles(t, h, admin, "sort=title"); len(got) != 3 {
		t.Fatalf("naming an order must list the library, got %v", got)
	}
}
