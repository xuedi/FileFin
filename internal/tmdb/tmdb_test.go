package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestClient points a client at a test server that serves the recorded fixture named by
// each request path.
func newTestClient(t *testing.T, credential string, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewAt(credential, srv.URL, srv.URL+"/img")
	c.pace = 0
	return c
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fixtureServer answers each API path with its recorded response, checking the v3 key.
func fixtureServer(t *testing.T) http.HandlerFunc {
	routes := map[string]string{
		"/find/tt0133093":             "find_movie.json",
		"/find/tt4574334":             "find_tv.json",
		"/movie/603/credits":          "movie_credits.json",
		"/tv/66732/aggregate_credits": "tv_aggregate_credits.json",
		"/person/6384":                "person.json",
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "k3y" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/find/tt0000000" {
			w.Write([]byte(`{"movie_results":[],"tv_results":[]}`))
			return
		}
		name, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(fixture(t, name))
	}
}

func TestFindByIMDb(t *testing.T) {
	c := newTestClient(t, "k3y", fixtureServer(t))
	ctx := context.Background()

	movie, err := c.FindByIMDb(ctx, "tt0133093")
	if err != nil || movie != (Title{Kind: KindMovie, ID: 603, Name: "The Matrix"}) {
		t.Fatalf("movie = %+v, %v", movie, err)
	}
	tv, err := c.FindByIMDb(ctx, "tt4574334")
	if err != nil || tv != (Title{Kind: KindTV, ID: 66732, Name: "Stranger Things"}) {
		t.Fatalf("tv = %+v, %v", tv, err)
	}
	if _, err := c.FindByIMDb(ctx, "tt0000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrNotFound", err)
	}
}

func TestMovieCredits(t *testing.T) {
	c := newTestClient(t, "k3y", fixtureServer(t))
	cast, err := c.Credits(context.Background(), Title{Kind: KindMovie, ID: 603})
	if err != nil {
		t.Fatal(err)
	}
	if len(cast) != 4 {
		t.Fatalf("got %d credits, want 4", len(cast))
	}
	neo := cast[0]
	if neo.ID != 6384 || neo.Name != "Keanu Reeves" || neo.Character != "Neo" || neo.ProfilePath == "" {
		t.Errorf("first credit = %+v", neo)
	}
	if last := cast[3]; last.Name != "Deni Gordon" || last.ProfilePath != "" {
		t.Errorf("credit without a photo = %+v", last)
	}
}

func TestTVAggregateCredits(t *testing.T) {
	c := newTestClient(t, "k3y", fixtureServer(t))
	cast, err := c.Credits(context.Background(), Title{Kind: KindTV, ID: 66732})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(cast); i++ {
		if cast[i].Order < cast[i-1].Order {
			t.Fatalf("credits not in billing order: %+v", cast)
		}
	}
	var eleven Credit
	for _, cr := range cast {
		if cr.Name == "Millie Bobby Brown" {
			eleven = cr
		}
	}
	if eleven.Character != "Eleven / Jane Hopper" {
		t.Errorf("series character = %q, want the role with the most episodes", eleven.Character)
	}
}

func TestPerson(t *testing.T) {
	c := newTestClient(t, "k3y", fixtureServer(t))
	p, err := c.Person(context.Background(), 6384)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Keanu Reeves" || p.Birthday != "1964-09-02" || p.Deathday != "" || p.Department != "Acting" {
		t.Errorf("person = %+v", p)
	}
	if _, err := c.Person(context.Background(), 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown person: err = %v, want ErrNotFound", err)
	}
}

func TestReadTokenUsesBearer(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiJ4In0.c2ln"
	c := newTestClient(t, token, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "" {
			t.Error("a read token must not be sent as api_key")
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write(fixture(t, "find_movie.json"))
	})
	if _, err := c.FindByIMDb(context.Background(), "tt0133093"); err != nil {
		t.Fatal(err)
	}
}

func TestBadKey(t *testing.T) {
	c := newTestClient(t, "wrong", fixtureServer(t))
	_, err := c.FindByIMDb(context.Background(), "tt0133093")
	if err == nil || !strings.Contains(err.Error(), "invalid API key") {
		t.Fatalf("err = %v, want invalid API key", err)
	}
}

func TestRetryOnceOn429(t *testing.T) {
	calls := 0
	c := newTestClient(t, "k3y", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write(fixture(t, "find_movie.json"))
	})
	c.pace = 0
	if _, err := c.FindByIMDb(context.Background(), "tt0133093"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestProfileImage(t *testing.T) {
	c := newTestClient(t, "k3y", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/img/abc.jpg" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("api_key") != "" {
			t.Error("the image host must not receive the API key")
		}
		w.Write([]byte("jpeg"))
	})
	data, err := c.ProfileImage(context.Background(), "/abc.jpg")
	if err != nil || string(data) != "jpeg" {
		t.Fatalf("image = %q, %v", data, err)
	}
	if _, err := c.ProfileImage(context.Background(), "../etc/passwd"); err == nil {
		t.Error("a path outside the image host must be refused")
	}
}
