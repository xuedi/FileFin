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
		if r.URL.Path != "/img/w185/abc.jpg" {
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

// detailsServer answers the details and search paths with their recorded responses.
func detailsServer(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie/603":
			if !strings.Contains(r.URL.Query().Get("append_to_response"), "credits") {
				t.Error("movie details must append the credits")
			}
			w.Write(fixture(t, "movie_details.json"))
		case "/tv/94796":
			w.Write(fixture(t, "tv_details.json"))
		case "/search/movie":
			if r.URL.Query().Get("query") == "The Matrix" && r.URL.Query().Get("primary_release_year") != "1999" {
				t.Errorf("movie search year = %q", r.URL.Query().Get("primary_release_year"))
			}
			w.Write(fixture(t, "search_movie.json"))
		case "/search/tv":
			w.Write(fixture(t, "search_tv.json"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestMovieDetails(t *testing.T) {
	c := newTestClient(t, "k3y", detailsServer(t))
	d, err := c.Details(context.Background(), KindMovie, 603)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "The Matrix" || d.Year != 1999 || d.Release != "1999-03-31" || d.Runtime != 136 || d.ImdbID != "tt0133093" {
		t.Errorf("movie = %+v", d)
	}
	if d.ContentRating != "R" || d.Tagline == "" || len(d.AltTitles) == 0 || d.PosterPath == "" {
		t.Errorf("rating %q tagline %q alt %v poster %q", d.ContentRating, d.Tagline, d.AltTitles, d.PosterPath)
	}
	if strings.Join(d.Directors, ",") != "Lana Wachowski,Lilly Wachowski" {
		t.Errorf("directors = %v", d.Directors)
	}
	if len(d.Writers) != 2 || len(d.Cast) == 0 || d.Cast[0] != "Keanu Reeves" {
		t.Errorf("writers %v cast %v", d.Writers, d.Cast)
	}
	if strings.Join(d.Genres, ",") != "Action,Science Fiction" || d.Languages[0] != "English" || d.Countries[0].Code != "US" {
		t.Errorf("genres %v languages %v countries %v", d.Genres, d.Languages, d.Countries)
	}
}

func TestSeriesDetails(t *testing.T) {
	c := newTestClient(t, "k3y", detailsServer(t))
	d, err := c.Details(context.Background(), KindTV, 94796)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Crash Landing on You" || d.OriginalTitle != "사랑의 불시착" || d.Year != 2019 || d.ImdbID != "tt10850932" {
		t.Errorf("series = %+v", d)
	}
	if d.Runtime != 0 {
		t.Errorf("a series without an episode run time has none (the last episode is no guide), got %d", d.Runtime)
	}
	if d.ContentRating != "TV-MA" || strings.Join(d.Writers, ",") != "Park Ji-eun,Lee Jung-hyo" || d.Languages[0] != "Korean" {
		t.Errorf("rating %q writers %v languages %v", d.ContentRating, d.Writers, d.Languages)
	}
}

func TestSearch(t *testing.T) {
	c := newTestClient(t, "k3y", detailsServer(t))
	res, err := c.Search(context.Background(), "The Matrix", 1999, KindMovie)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || res[0].ID != 603 || res[0].Year != 1999 || res[0].Kind != KindMovie {
		t.Fatalf("movie search = %+v", res)
	}
	both, err := c.Search(context.Background(), "Crash Landing on You", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	var tv SearchResult
	for _, r := range both {
		if r.Kind == KindTV {
			tv = r
		}
	}
	if tv.ID != 94796 || tv.OriginalTitle != "사랑의 불시착" {
		t.Errorf("a search of both kinds must include the series, got %+v", both)
	}
}

func TestImagePathIsStrict(t *testing.T) {
	c := newTestClient(t, "k3y", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("img")) })
	ctx := context.Background()
	if _, err := c.Poster(ctx, "w154", "/abc_1-2.jpg"); err != nil {
		t.Fatalf("a plain poster path must pass: %v", err)
	}
	for _, bad := range [][2]string{{"w154", "/a/b.jpg"}, {"w154", "/a?x=1"}, {"x154", "/a.jpg"}, {"w154", "a.jpg"}, {"../w", "/a.jpg"}} {
		if _, err := c.Poster(ctx, bad[0], bad[1]); err == nil {
			t.Errorf("Poster(%q, %q) must be refused", bad[0], bad[1])
		}
	}
}
