package omdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient points a client at a test server that dispatches on the query params OMDb
// uses: t= (lookup by title), i= (lookup by id), s= (search).
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("key")
	c.baseURL = srv.URL
	return c
}

func TestLookup(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") != "Goblin" {
			t.Errorf("expected t=Goblin, got %q", r.URL.Query().Get("t"))
		}
		w.Write([]byte(`{"Title":"Goblin","Year":"2016","imdbID":"tt5679540","Response":"True"}`))
	})
	m, err := c.Lookup(context.Background(), "Goblin", 2016)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Goblin" || m.ImdbID != "tt5679540" {
		t.Errorf("lookup = %+v", m)
	}
}

func TestLookupByID(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("i") != "tt5679540" {
			t.Errorf("expected i=tt5679540, got %q", r.URL.Query().Get("i"))
		}
		w.Write([]byte(`{"Title":"Goblin","Year":"2016","imdbID":"tt5679540","Response":"True"}`))
	})
	m, err := c.LookupByID(context.Background(), "tt5679540")
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Goblin" {
		t.Errorf("lookup by id = %+v", m)
	}
}

func TestSearch(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("s") != "King" {
			t.Errorf("expected s=King, got %q", r.URL.Query().Get("s"))
		}
		w.Write([]byte(`{"Search":[
			{"Title":"King","Year":"2011","imdbID":"tt1","Type":"series","Poster":"http://x/p.jpg"},
			{"Title":"King 2","Year":"2013","imdbID":"tt2","Type":"movie","Poster":"N/A"}
		],"totalResults":"2","Response":"True"}`))
	})
	res, err := c.Search(context.Background(), "King", 2011, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].ImdbID != "tt1" || res[0].Year != "2011" || res[1].Title != "King 2" {
		t.Errorf("search = %+v", res)
	}
}

func TestSearchNoResults(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Response":"False","Error":"Movie not found!"}`))
	})
	res, err := c.Search(context.Background(), "zzxx", 0, "")
	if err != nil {
		t.Fatalf("a not-found search must not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected no candidates, got %+v", res)
	}
}
