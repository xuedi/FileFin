package mal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// newTestServer serves the two captured fixtures, paging from page 1 to page 2 by the offset
// query param. Page 1 is a full page (equal to the client's page size), so the client requests
// the next; page 2 is short, so it stops.
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("offset") == "3" {
			w.Write(page2)
			return
		}
		w.Write(page1)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetUserList(t *testing.T) {
	srv := newTestServer(t)
	c := New()
	c.baseURL = srv.URL
	c.pageSize = 3 // page 1 holds exactly 3 rows, so the client pages on to page 2

	entries, err := c.GetUserList(context.Background(), "someone")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("want 4 entries across both pages, got %d: %+v", len(entries), entries)
	}

	aot := entries[0]
	if aot.Title != "Shingeki no Kyojin" || aot.Year != 2013 || aot.Rating != 10 || !aot.Watched {
		t.Errorf("entry 0 = %+v", aot)
	}
	if len(aot.Aliases) != 1 || aot.Aliases[0] != "Attack on Titan" {
		t.Errorf("entry 0 should alias the distinct English title: %+v", aot.Aliases)
	}

	// One Piece: watching (not watched), unrated, English title equals romaji so no alias,
	// and a "99" year resolves to 1999.
	op := entries[1]
	if op.Watched || op.Rating != 0 || len(op.Aliases) != 0 || op.Year != 1999 {
		t.Errorf("entry 1 = %+v", op)
	}

	// Aa! Megami-sama!: dropped (status 4) is not watched but keeps its rating, and its
	// distinct English title becomes an alias; "93" resolves to 1993.
	goddess := entries[2]
	if goddess.Watched || goddess.Rating != 5 || goddess.Year != 1993 {
		t.Errorf("entry 2 = %+v", goddess)
	}
	if len(goddess.Aliases) != 1 || goddess.Aliases[0] != "Oh! My Goddess" {
		t.Errorf("entry 2 aliases = %+v", goddess.Aliases)
	}

	if entries[3].Title != "Kingdom" || entries[3].Year != 2012 || !entries[3].Watched {
		t.Errorf("entry 3 (page 2) = %+v", entries[3])
	}
}

func TestGetUserListErrors(t *testing.T) {
	// A 404 (no such user) surfaces as ErrNotFound.
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFound.Close()
	c := New()
	c.baseURL = notFound.URL
	if _, err := c.GetUserList(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 should be ErrNotFound, got %v", err)
	}

	// An empty list (private or genuinely empty) surfaces as ErrEmpty.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer empty.Close()
	c.baseURL = empty.URL
	if _, err := c.GetUserList(context.Background(), "nobody"); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty list should be ErrEmpty, got %v", err)
	}
}

func TestYearOf(t *testing.T) {
	cases := map[string]int{
		"04-07-13": 2013,
		"10-20-99": 1999,
		"02-25-98": 1998,
		"01-01-30": 2030,
		"01-01-31": 1931,
		"":         0,
		"2013":     0,
		"bad":      0,
	}
	for in, want := range cases {
		if got := yearOf(in); got != want {
			t.Errorf("yearOf(%q) = %d, want %d", in, got, want)
		}
	}
}
