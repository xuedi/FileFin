package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/people"
	"filefin/internal/tmdb"
)

// fakeTMDb serves the recorded TMDb fixtures: The Matrix by its IMDb id, its credits, one
// person's details, and any photo. Every other person is unknown to it, like a person TMDb
// has since removed.
func fakeTMDb(t *testing.T) *httptest.Server {
	t.Helper()
	routes := map[string]string{
		"/find/tt0133093":    "find_movie.json",
		"/movie/603/credits": "movie_credits.json",
		"/person/6384":       "person.json",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/img/") {
			w.Write([]byte("jpeg"))
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
		data, err := os.ReadFile(filepath.Join("..", "tmdb", "testdata", name))
		if err != nil {
			t.Error(err)
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// writeMediaFolder creates a media folder with one video and the given meta.json.
func writeMediaFolder(t *testing.T, dataDir, category, folder, meta string) string {
	t.Helper()
	dir := filepath.Join(dataDir, category, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, folder+".mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// drainPeople runs the people agent's per-item body until its queue is empty.
func drainPeople(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()
	pool, err := s.ensureDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for {
		task, ok, err := db.ClaimNextPeople(ctx, pool, peopleAgentName)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return
		}
		s.peopleOne(ctx, pool, s.tmdbClient(), task)
	}
}

// TestPeopleAgent drives the cast flow end to end against a fake TMDb: discovery queues the
// items with an IMDb id, the agent writes the cast block and fills the shared people store,
// the detail page shows portraits with the OMDb-only actor after them, a TMDb-only name is
// searchable, and the people store is invisible to the category scan and the rebuild.
func TestPeopleAgent(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, bob := installedServer(t, dataDir)
	api := fakeTMDb(t)
	s.cfg.TMDBKey = "k3y"
	s.newTMDb = func(key string) *tmdb.Client { return tmdb.NewAt(key, api.URL, api.URL+"/img") }
	ctx := context.Background()

	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Movies","alias":"Films"}`, admin); rr.Code != 200 {
		t.Fatalf("create category: %d %s", rr.Code, rr.Body.String())
	}
	matrix := writeMediaFolder(t, dataDir, "Movies", "(1999) The Matrix",
		`{"title":"The Matrix","year":1999,"enriched":true,"metadata":{"imdbID":"tt0133093"},"actors":["Keanu Reeves","Hugo Weaving"]}`)
	unknown := writeMediaFolder(t, dataDir, "Movies", "(2001) Obscure",
		`{"title":"Obscure","year":2001,"enriched":true,"metadata":{"imdbID":"tt0000000"}}`)
	writeMediaFolder(t, dataDir, "Movies", "(2002) Home Video", `{"title":"Home Video","year":2002}`)

	s.discoveryTick(ctx)
	pool, _ := s.ensureDB(ctx)
	if n, _ := db.CountPendingPeople(ctx, pool); n != 2 {
		t.Fatalf("pending people tasks = %d, want the 2 items with an IMDb id", n)
	}
	drainPeople(t, s)

	m, err := importer.ReadMeta(matrix)
	if err != nil {
		t.Fatal(err)
	}
	if m.Cast == nil || m.Cast.ImdbID != "tt0133093" || m.Cast.TMDbID != 603 || len(m.Cast.Members) != 4 {
		t.Fatalf("matrix cast = %+v", m.Cast)
	}
	if m.Title != "The Matrix" || len(m.Actors) != 2 {
		t.Fatalf("the cast write must keep the rest of meta.json, got %+v", m)
	}
	if u, _ := importer.ReadMeta(unknown); u.Cast == nil || u.Cast.Error != "not on TMDb" || len(u.Cast.Members) != 0 {
		t.Fatalf("an unknown title records why it has no cast, got %+v", u.Cast)
	}

	store := people.New(dataDir)
	if store.Count() != 4 || store.PhotoPath(6384) == "" {
		t.Fatalf("people store: %d people, photo %q", store.Count(), store.PhotoPath(6384))
	}
	if store.PhotoPath(1090466) != "" {
		t.Error("a person without a TMDb photo gets none")
	}

	// Nothing is left to do, so a second pass queues nothing.
	if n, _ := s.refillPeople(ctx, pool); n != 0 {
		t.Fatalf("second refill queued %d, want 0", n)
	}

	// The detail page: TMDb cast in billing order, then the actor only OMDb knows.
	id := mediaID("Movies", "(1999) The Matrix")
	rr := do(t, h, "GET", "/api/media/"+id, "", bob)
	if rr.Code != 200 {
		t.Fatalf("detail: %d %s", rr.Code, rr.Body.String())
	}
	var d mediaDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if !d.CastFromTMDb || len(d.People) != 5 {
		t.Fatalf("people = %+v (fromTMDb %v), want 4 cast + 1 actor", d.People, d.CastFromTMDb)
	}
	if first := d.People[0]; first.Name != "Keanu Reeves" || first.Character != "Neo" || first.Photo != "/api/people/6384/photo" {
		t.Errorf("first card = %+v", first)
	}
	if last := d.People[4]; last.Name != "Hugo Weaving" || last.ID != 0 || last.Photo != "" {
		t.Errorf("the OMDb-only actor comes last without a photo, got %+v", last)
	}

	rr = do(t, h, "GET", "/api/people/6384/photo", "", bob)
	if rr.Code != 200 || !strings.Contains(rr.Header().Get("Cache-Control"), "max-age") {
		t.Fatalf("photo: %d, cache %q", rr.Code, rr.Header().Get("Cache-Control"))
	}
	for _, bad := range []string{"/api/people/1090466/photo", "/api/people/x/photo", "/api/people/-1/photo"} {
		if rr := do(t, h, "GET", bad, "", bob); rr.Code != 404 {
			t.Errorf("%s: %d, want 404", bad, rr.Code)
		}
	}
	if rr := do(t, h, "GET", "/api/people/6384/photo", "", nil); rr.Code == 200 {
		t.Error("a photo needs a session")
	}

	if got := searchTitles(t, h, admin, "field=cast&q=carrie-anne"); len(got) != 1 || got[0] != "The Matrix" {
		t.Errorf("a TMDb-only cast name must be searchable, got %v", got)
	}

	// The people store sits at the data dir root beside the categories and is never taken
	// for one, by the rebuild or by the category list.
	if rr := do(t, h, "POST", "/api/admin/rebuild", "", admin); rr.Code != 200 {
		t.Fatalf("rebuild: %d %s", rr.Code, rr.Body.String())
	}
	waitForRebuild(t, s)
	if st := s.rebuildJob.snapshot(); st.Categories != 1 || st.Media != 3 || st.Error != "" {
		t.Fatalf("rebuild = %+v, want 1 category and 3 media", st)
	}
	if got := searchTitles(t, h, admin, "field=cast&q=carrie-anne"); len(got) != 1 {
		t.Errorf("the rebuild must restore the cast facets from meta.json, got %v", got)
	}
	rr = do(t, h, "GET", "/api/admin/categories", "", admin)
	if strings.Contains(rr.Body.String(), people.DirName) {
		t.Errorf("the people store must not be listed as a category: %s", rr.Body.String())
	}
}

// TestPeopleRematch: a cast describes the IMDb id it was resolved from. Once the item is
// matched to another id, the old cast is hidden and the item is queued again.
func TestPeopleRematch(t *testing.T) {
	dir := t.TempDir()
	store := people.New(dir)
	meta := importer.Meta{
		Metadata: map[string]string{"imdbID": "tt2"},
		Actors:   []string{"Xiwei Tian"},
		Cast: &importer.Cast{ImdbID: "tt1", Members: []importer.CastMember{
			{ID: 1, Name: "Tian Xiwei", Character: "Lead"},
		}},
	}
	if !needsPeople(meta, store) {
		t.Error("a cast for another IMDb id needs refreshing")
	}
	cards, fromTMDb := castCards(meta, store)
	if fromTMDb || len(cards) != 1 || cards[0].Name != "Xiwei Tian" {
		t.Errorf("a stale cast is not shown, got %+v (fromTMDb %v)", cards, fromTMDb)
	}
	if got := meta.FacetActors(); len(got) != 1 {
		t.Errorf("a stale cast is not searchable, got %v", got)
	}

	// Current again: the two spellings of one name show once, as the TMDb card.
	meta.Metadata["imdbID"] = "tt1"
	cards, fromTMDb = castCards(meta, store)
	if !fromTMDb || len(cards) != 1 || cards[0].Name != "Tian Xiwei" || cards[0].Character != "Lead" {
		t.Errorf("cards = %+v, want the TMDb card only", cards)
	}
	if !needsPeople(meta, store) {
		t.Error("a cast member missing from the people store still needs work")
	}
	if err := store.Write(people.Record{ID: 1, Name: "Tian Xiwei"}); err != nil {
		t.Fatal(err)
	}
	if needsPeople(meta, store) {
		t.Error("a complete cast needs nothing")
	}
	if needsPeople(importer.Meta{}, store) {
		t.Error("an item without an IMDb id has nothing to resolve")
	}
}
