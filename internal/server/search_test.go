package server

import (
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
