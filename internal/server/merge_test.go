package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/sources"
	"filefin/internal/tmdb"
)

// drainTMDb runs the TMDb agent's per-item body until its queue is empty.
func drainTMDb(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()
	pool, err := s.ensureDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for {
		task, ok, err := db.ClaimNextTMDb(ctx, pool, tmdbAgentName)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return
		}
		s.tmdbOne(ctx, pool, s.tmdbClient(), task)
	}
}

// writeMeta writes a meta.json built in Go into a new media folder with one video.
func writeMeta(t *testing.T, dataDir, category, folder, video string, m importer.Meta) string {
	t.Helper()
	dir := filepath.Join(dataDir, category, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, video), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := importer.WriteMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	return dir
}

// omdbMatrix is The Matrix as the OMDb enricher leaves it: the top level and the snapshot.
func omdbMatrix() importer.Meta {
	snap := &importer.Snapshot{
		ID: "tt0133093", Kind: "movie", ImdbID: "tt0133093", Fetched: 1, Title: "The Matrix", Year: 1999,
		Description: "A hacker learns the truth.",
		Metadata: map[string]string{
			"release": "31 Mar 1999", "runtime": "136", "language": "English", "origin": "United States",
			"directedBy": "Lana Wachowski, Lilly Wachowski", "writtenBy": "Lilly Wachowski, Lana Wachowski",
			"contentRating": "R", "imdbID": "tt0133093",
		},
		Genres: []string{"action", "sci-fi"},
		Actors: []string{"Keanu Reeves", "Laurence Fishburne"},
	}
	md := map[string]string{}
	for k, v := range snap.Metadata {
		md[k] = v
	}
	return importer.Meta{
		Title: "The Matrix", Year: 1999, Enriched: true, Description: snap.Description,
		Metadata: md, Genres: []string{"action", "sci-fi"}, Actors: []string{"Keanu Reeves", "Laurence Fishburne"},
		Sources: map[string]*importer.Snapshot{sources.OMDb: snap},
	}
}

// tmdbServer builds an installed server whose TMDb client talks to the fake TMDb.
func tmdbServer(t *testing.T, dataDir string) (*Server, func(method, path, body string) (int, string)) {
	t.Helper()
	s, h, admin, _ := installedServer(t, dataDir)
	api := fakeTMDb(t)
	s.cfg.TMDBKey = "k3y"
	s.newTMDb = func(key string) *tmdb.Client { return tmdb.NewAt(key, api.URL, api.URL+"/img") }
	call := func(method, path, body string) (int, string) {
		rr := do(t, h, method, path, body, admin)
		return rr.Code, rr.Body.String()
	}
	for _, c := range []string{"Movies", "Shows"} {
		if code, body := call("POST", "/api/admin/categories", `{"name":"`+c+`","alias":"`+c+`"}`); code != 200 {
			t.Fatalf("create category %s: %d %s", c, code, body)
		}
	}
	return s, call
}

// TestTMDbMatching: the agent records a TMDb snapshot by IMDb id for a matched film, and
// finds a series with no IMDb id by a title search, which is what matches it at all.
func TestTMDbMatching(t *testing.T) {
	dataDir := t.TempDir()
	s, _ := tmdbServer(t, dataDir)
	ctx := context.Background()

	matrix := writeMeta(t, dataDir, "Movies", "(1999) The Matrix", "The Matrix.mkv", omdbMatrix())
	cloy := writeMeta(t, dataDir, "Shows", "(2019) Crash Landing on You", "Crash Landing on You S01E01.mkv",
		importer.Meta{Title: "Crash Landing on You", Year: 2019})
	s.discoveryTick(ctx)
	drainTMDb(t, s)

	m, _ := importer.ReadMeta(matrix)
	if tm := m.Sources[sources.TMDb]; !tm.Usable() || tm.ID != "603" {
		t.Fatalf("matrix tmdb snapshot = %+v", tm)
	}
	if m.Metadata["tagline"] == "" || m.Metadata["tmdbID"] != "movie/603" || m.Ratings["tmdb"] == "" {
		t.Errorf("the merge must fill what only TMDb has: %v %v", m.Metadata, m.Ratings)
	}
	if m.Description != "A hacker learns the truth." || m.Metadata["release"] != "31 Mar 1999" {
		t.Errorf("OMDb's values stay where TMDb agrees or only differs in prose: %+v", m)
	}
	pool, _ := s.ensureDB(ctx)
	if items, _ := s.conflictItems(ctx, pool); len(items) != 0 {
		t.Errorf("two records of one film must not conflict: %+v", items)
	}

	c, _ := importer.ReadMeta(cloy)
	if tm := c.Sources[sources.TMDb]; !tm.Usable() || tm.ID != "94796" || tm.Kind != sources.KindSeries {
		t.Fatalf("series found by search = %+v", tm)
	}
	if !c.Enriched || c.Metadata["imdbID"] != "tt10850932" || c.Description == "" || len(c.Actors) == 0 {
		t.Errorf("a TMDb-only match fills the item and its IMDb id: %+v", c)
	}
	if n, _ := s.refillTMDb(ctx, pool); n != 0 {
		t.Errorf("a second refill queued %d, want 0", n)
	}
}

// TestTMDbSearchIsYearStrict: a search hit a year off by more than one is not taken; the item
// records the miss and is not asked again right away.
func TestTMDbSearchIsYearStrict(t *testing.T) {
	dataDir := t.TempDir()
	s, _ := tmdbServer(t, dataDir)
	ctx := context.Background()
	dir := writeMeta(t, dataDir, "Shows", "(2015) Crash Landing on You", "Crash Landing on You S01E01.mkv",
		importer.Meta{Title: "Crash Landing on You", Year: 2015})
	s.discoveryTick(ctx)
	drainTMDb(t, s)
	m, _ := importer.ReadMeta(dir)
	if tm := m.Sources[sources.TMDb]; tm == nil || tm.Usable() {
		t.Fatalf("want a failed snapshot, got %+v", tm)
	}
	if m.Enriched || m.Metadata["imdbID"] != "" {
		t.Errorf("a miss changes nothing else: %+v", m)
	}
	pool, _ := s.ensureDB(ctx)
	if n, _ := s.refillTMDb(ctx, pool); n != 0 {
		t.Errorf("a recent miss is not retried, queued %d", n)
	}
}

// conflicted writes an item whose OMDb and TMDb snapshots disagree on the runtime.
func conflicted(t *testing.T, dataDir, folder string) string {
	m := omdbMatrix()
	tm := &importer.Snapshot{
		ID: "603", Kind: "movie", ImdbID: "tt0133093", Fetched: time.Now().Unix(), Title: "The Matrix", Year: 1999,
		Metadata: map[string]string{"runtime": "170", "tagline": "Believe the unbelievable."},
	}
	m.Sources[sources.TMDb] = tm
	return writeMeta(t, dataDir, "Movies", folder, folder+".mkv", m)
}

// TestMergeFlow: a conflict is listed, the merge view shows it, a pick settles it for the item
// and "remember" makes it a library rule that settles the same conflict on another item;
// removing the rule brings that one back.
func TestMergeFlow(t *testing.T) {
	dataDir := t.TempDir()
	s, call := tmdbServer(t, dataDir)
	ctx := context.Background()
	first := conflicted(t, dataDir, "(1999) The Matrix")
	second := conflicted(t, dataDir, "(1999) The Matrix Again")
	third := conflicted(t, dataDir, "(1999) The Matrix Once More")
	s.discoveryTick(ctx)

	code, body := call("GET", "/api/admin/conflicts", "")
	var list struct{ Items []conflictItem }
	if err := json.Unmarshal([]byte(body), &list); code != 200 || err != nil || len(list.Items) != 3 {
		t.Fatalf("conflicts: %d %s", code, body)
	}
	if list.Items[0].Fields[0] != "runtime" || list.Items[0].Identity {
		t.Errorf("conflict row = %+v", list.Items[0])
	}

	id := mediaID("Movies", "(1999) The Matrix")
	code, body = call("GET", "/api/admin/media/"+id+"/merge", "")
	var v mergeView
	if err := json.Unmarshal([]byte(body), &v); code != 200 || err != nil {
		t.Fatalf("merge view: %d %s", code, body)
	}
	if len(v.Sources) != 2 || v.Sources[1].Poster != "" {
		t.Errorf("merge sources = %+v", v.Sources)
	}
	var runtime sources.Row
	for _, r := range v.Rows {
		if r.Field == "runtime" {
			runtime = r
		}
	}
	if runtime.Status != sources.StatusConflict || runtime.Values["tmdb"][0] != "170" {
		t.Fatalf("runtime row = %+v", runtime)
	}

	if code, _ := call("POST", "/api/admin/media/"+id+"/merge", `{"picks":{"imdbID":"tmdb"}}`); code != 400 {
		t.Errorf("an id is fixed by a re-match, not a pick: %d", code)
	}
	if code, _ := call("POST", "/api/admin/media/"+id+"/merge", `{"picks":{"runtime":"union"}}`); code != 400 {
		t.Errorf("a scalar has no union: %d", code)
	}

	// A pick for this item alone: the answer names the next item still in conflict.
	code, body = call("POST", "/api/admin/media/"+id+"/merge", `{"picks":{"runtime":"tmdb"}}`)
	if code != 200 || !strings.Contains(body, `"next":"`) || strings.Contains(body, `"next":""`) || strings.Contains(body, id) {
		t.Fatalf("apply merge: %d %s (want another conflict as next)", code, body)
	}
	m, _ := importer.ReadMeta(first)
	if m.Metadata["runtime"] != "170" || m.Choices["runtime"] != sources.TMDb {
		t.Errorf("the pick is written and pinned: %v %v", m.Metadata["runtime"], m.Choices)
	}
	if len(s.cfg.MetadataRules) != 0 {
		t.Fatalf("a pick without remember makes no rule: %+v", s.cfg.MetadataRules)
	}

	// Remembered on the second item, it becomes a rule that settles the third one too.
	againID := mediaID("Movies", "(1999) The Matrix Again")
	code, body = call("POST", "/api/admin/media/"+againID+"/merge", `{"picks":{"runtime":"tmdb"},"remember":["runtime"]}`)
	if code != 200 || !strings.Contains(body, `"next":""`) {
		t.Fatalf("apply with remember: %d %s (want nothing left)", code, body)
	}
	if r := s.cfg.MetadataRules["runtime"]; len(r.Order) == 0 || r.Order[0] != sources.TMDb {
		t.Fatalf("remember makes a rule: %+v", s.cfg.MetadataRules)
	}
	waitReapply(t, s)
	if other, _ := importer.ReadMeta(third); other.Metadata["runtime"] != "170" || other.Choices["runtime"] != "" {
		t.Errorf("the rule settles the other item without pinning it: %v %v", other.Metadata["runtime"], other.Choices)
	}
	if code, body = call("GET", "/api/admin/conflicts", ""); strings.Contains(body, `"id"`) {
		t.Errorf("no conflicts left under the rule: %s", body)
	}

	code, body = call("DELETE", "/api/admin/settings/metadata-rules/runtime", "")
	if code != 200 || strings.Contains(body, `"field":"runtime"`) {
		t.Fatalf("delete rule: %d %s", code, body)
	}
	waitReapply(t, s)
	_, body = call("GET", "/api/admin/conflicts", "")
	if err := json.Unmarshal([]byte(body), &list); err != nil || len(list.Items) != 1 || list.Items[0].ID != mediaID("Movies", "(1999) The Matrix Once More") {
		t.Errorf("without the rule only the unpinned item conflicts again: %s", body)
	}
	if again, _ := importer.ReadMeta(second); again.Choices["runtime"] != sources.TMDb {
		t.Errorf("the item the rule was made on keeps its own pin: %v", again.Choices)
	}
}

// TestEditPinsManual: a field changed in the metadata editor is pinned, so the merge leaves
// it alone even where a source says otherwise.
func TestEditPinsManual(t *testing.T) {
	dataDir := t.TempDir()
	s, call := tmdbServer(t, dataDir)
	dir := conflicted(t, dataDir, "(1999) The Matrix")
	s.discoveryTick(context.Background())
	id := mediaID("Movies", "(1999) The Matrix")

	_, body := call("GET", "/api/admin/media/"+id+"/meta", "")
	var form map[string]any
	if err := json.Unmarshal([]byte(body), &form); err != nil {
		t.Fatal(err)
	}
	form["runtime"] = "140"
	payload, _ := json.Marshal(form)
	if code, body := call("POST", "/api/admin/media/"+id+"/meta", string(payload)); code != 204 {
		t.Fatalf("save: %d %s", code, body)
	}
	m, _ := importer.ReadMeta(dir)
	if m.Choices["runtime"] != sources.ChoiceManual || m.Choices["title"] != "" {
		t.Errorf("only the edited field is pinned: %v", m.Choices)
	}
	if _, rows := s.resolveMeta(m); len(sources.Conflicts(rows)) != 0 {
		t.Errorf("a hand-set field is not a conflict any more: %+v", sources.Conflicts(rows))
	}
}

func waitReapply(t *testing.T, s *Server) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if !s.reapplying.Load() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the rules were not re-applied in time")
}
