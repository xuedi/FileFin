package mal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// newTestServer serves the two captured fixtures, paging from page 1 to page 2 via the
// offset query param, with page 1's paging.next rewritten to point back at this server.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	page1, err := os.ReadFile("testdata/page1.json")
	if err != nil {
		t.Fatal(err)
	}
	page2, err := os.ReadFile("testdata/page2.json")
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-MAL-CLIENT-ID") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("offset") == "1000" {
			w.Write(page2)
			return
		}
		next := srv.URL + "/v2/users/x/animelist?offset=1000"
		w.Write([]byte(strings.Replace(string(page1), "{{NEXT}}", next, 1)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetUserList(t *testing.T) {
	srv := newTestServer(t)
	c := New("client-id")
	c.baseURL = srv.URL

	entries, err := c.GetUserList(context.Background(), "someone")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries across both pages, got %d: %+v", len(entries), entries)
	}

	aot := entries[0]
	if aot.Title != "Shingeki no Kyojin" || aot.Year != 2013 || aot.Rating != 10 || !aot.Watched {
		t.Errorf("entry 0 = %+v", aot)
	}
	if len(aot.Aliases) != 1 || aot.Aliases[0] != "Attack on Titan" {
		t.Errorf("entry 0 should alias the English title: %+v", aot.Aliases)
	}

	kimi := entries[1]
	if kimi.Year != 2016 { // no start_season -> year parsed from start_date
		t.Errorf("entry 1 year should come from start_date: %+v", kimi)
	}
	if kimi.Rating != 0 || kimi.Watched || len(kimi.Aliases) != 0 {
		t.Errorf("entry 1 should be unrated, unwatched, alias-free: %+v", kimi)
	}

	if entries[2].Title != "Kingdom" || entries[2].Year != 2012 {
		t.Errorf("entry 2 (page 2) = %+v", entries[2])
	}
}

func TestGetUserListErrors(t *testing.T) {
	if _, err := New("").GetUserList(context.Background(), "x"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("empty client id should be ErrNotConfigured, got %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := New("id")
	c.baseURL = srv.URL
	if _, err := c.GetUserList(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 should be ErrNotFound, got %v", err)
	}
}
