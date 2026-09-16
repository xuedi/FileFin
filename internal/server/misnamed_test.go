package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/mediafmt"
	"filefin/internal/recognize"
	"filefin/internal/state"
)

// seedShow creates a media folder holding several episode files (plus whatever extra files
// the caller names) and the matching cache rows, returning the media id. The cache is seeded
// with the names actually on disk, which is what makes the drift visible.
func seedShow(t *testing.T, s *Server, dataDir, category string, catID int64, folder string, meta importer.Meta, videos []string, extras ...string) string {
	t.Helper()
	dir := filepath.Join(dataDir, category, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range append(append([]string{}, videos...), extras...) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "poster.jpg"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := importer.WriteMeta(dir, meta); err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	pool := s.db
	s.mu.RUnlock()
	ctx := context.Background()
	id := mediaID(category, folder)
	if err := db.InsertMedia(ctx, pool, db.Media{
		ID: id, CategoryID: catID, Path: dir, Year: meta.Year, Title: meta.Title,
		Poster: "poster.jpg", Enriched: meta.Enriched,
	}); err != nil {
		t.Fatal(err)
	}
	for i, n := range videos {
		p := recognize.ParseName(n, true)
		if err := db.InsertMediaFile(ctx, pool, db.MediaFile{
			MediaID: id, Idx: i, Path: filepath.Join(dir, n), Name: n,
			Season: p.Season, Episode: p.Episode, Ext: strings.ToLower(filepath.Ext(n)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// misnamedList reads the misnamed-media report.
func misnamedList(t *testing.T, h http.Handler, admin *http.Cookie) []misnamedMedia {
	t.Helper()
	rr := do(t, h, "GET", "/api/admin/misnamed", "", admin)
	if rr.Code != 200 {
		t.Fatalf("misnamed: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Items []misnamedMedia `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Items
}

// changeMap flattens a plan's file changes for comparison.
func changeMap(cs []nameChange) map[string]string {
	out := map[string]string{}
	for _, c := range cs {
		out[c.From] = c.To
	}
	return out
}

func TestMisnamedReportsFolderFilesAndSidecars(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	s.cfg.MediaFormat = mediafmt.FileFin

	// The year was missing at import and the enricher filled it in afterwards, so meta.json
	// is right and every name still carries the "(0)".
	meta := importer.Meta{Title: "Genius Girlfriend", Year: 2026, Enriched: true}
	seedShow(t, s, dataDir, "Movies", catID, "(0) Genius Girlfriend", meta,
		[]string{"(0) Genius Girlfriend - 1x1.mkv", "(0) Genius Girlfriend - 1x10.mkv"},
		"(0) Genius Girlfriend - 1x1.en.srt",
		"(0) Genius Girlfriend - 1x10.optimized.mp4")

	items := misnamedList(t, h, admin)
	if len(items) != 1 {
		t.Fatalf("want one misnamed item, got %+v", items)
	}
	got := items[0]
	if got.Plan.Folder.From != "(0) Genius Girlfriend" || got.Plan.Folder.To != "(2026) Genius Girlfriend" {
		t.Fatalf("folder change = %+v", got.Plan.Folder)
	}
	want := map[string]string{
		"(0) Genius Girlfriend - 1x1.mkv":            "(2026) Genius Girlfriend - 1x1.mkv",
		"(0) Genius Girlfriend - 1x10.mkv":           "(2026) Genius Girlfriend - 1x10.mkv",
		"(0) Genius Girlfriend - 1x1.en.srt":         "(2026) Genius Girlfriend - 1x1.en.srt",
		"(0) Genius Girlfriend - 1x10.optimized.mp4": "(2026) Genius Girlfriend - 1x10.optimized.mp4",
	}
	if diff := changeMap(got.Plan.Files); len(diff) != len(want) {
		t.Fatalf("file changes = %+v, want %+v", diff, want)
	}
	for from, to := range want {
		if changeMap(got.Plan.Files)[from] != to {
			t.Errorf("%q -> %q, want %q", from, changeMap(got.Plan.Files)[from], to)
		}
	}
}

func TestMisnamedReportsFilesUnderACorrectFolder(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	s.cfg.MediaFormat = mediafmt.FileFin

	// The folder is right but the files never carried the format's naming at all.
	meta := importer.Meta{Title: "Trepalium", Year: 2016, Enriched: true}
	seedShow(t, s, dataDir, "Movies", catID, "(2016) Trepalium", meta,
		[]string{"Trepalium - 1x1.avi"}, "Trepalium - 1x1.optimized.mp4")

	items := misnamedList(t, h, admin)
	if len(items) != 1 {
		t.Fatalf("want one misnamed item, got %+v", items)
	}
	if f := items[0].Plan.Folder; f.From != f.To {
		t.Fatalf("folder should not change, got %+v", f)
	}
	want := map[string]string{
		"Trepalium - 1x1.avi":           "(2016) Trepalium - 1x1.avi",
		"Trepalium - 1x1.optimized.mp4": "(2016) Trepalium - 1x1.optimized.mp4",
	}
	for from, to := range want {
		if got := changeMap(items[0].Plan.Files)[from]; got != to {
			t.Errorf("%q -> %q, want %q", from, got, to)
		}
	}
}

func TestMisnamedSkipsCorrectAndYearlessItems(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	s.cfg.MediaFormat = mediafmt.FileFin

	seedShow(t, s, dataDir, "Movies", catID, "(1999) The Matrix",
		importer.Meta{Title: "The Matrix", Year: 1999, Enriched: true},
		[]string{"(1999) The Matrix.mkv"})
	// No year yet: renaming it would only produce "(0) Unknown Show" again, so it belongs on
	// the no-metadata list rather than this one.
	seedShow(t, s, dataDir, "Movies", catID, "(0) Unknown Show",
		importer.Meta{Title: "Unknown Show"},
		[]string{"(0) Unknown Show - 1x1.mkv"})

	if items := misnamedList(t, h, admin); len(items) != 0 {
		t.Fatalf("want nothing reported, got %+v", items)
	}
}

func TestMisnamedRefusesWhenATargetNameIsTaken(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	s.cfg.MediaFormat = mediafmt.FileFin

	// A stray file already sits on the name the rename would need, so the whole plan is
	// withheld rather than applied half way.
	seedShow(t, s, dataDir, "Movies", catID, "(0) Genius Girlfriend",
		importer.Meta{Title: "Genius Girlfriend", Year: 2026, Enriched: true},
		[]string{"(0) Genius Girlfriend - 1x1.mkv"},
		"(2026) Genius Girlfriend - 1x1.mkv")

	if items := misnamedList(t, h, admin); len(items) != 0 {
		t.Fatalf("want the unsafe plan withheld, got %+v", items)
	}
}

func TestRenameMovesEverythingAndRekeysTheCache(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	s.cfg.MediaFormat = mediafmt.FileFin
	ctx := context.Background()

	meta := importer.Meta{
		Title: "Genius Girlfriend", Year: 2026, Enriched: true,
		State: map[string]state.UserState{"admin": {Watched: true, Rating: 9, Updated: 1700000000}},
	}
	oldID := seedShow(t, s, dataDir, "Movies", catID, "(0) Genius Girlfriend", meta,
		[]string{"(0) Genius Girlfriend - 1x1.mkv", "(0) Genius Girlfriend - 1x10.mkv"},
		"(0) Genius Girlfriend - 1x1.en.srt",
		"(0) Genius Girlfriend - 1x10.optimized.mp4")

	rr := do(t, h, "POST", "/api/admin/media/"+oldID+"/rename", "", admin)
	if rr.Code != 200 {
		t.Fatalf("rename: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID == oldID || got.ID != mediaID("Movies", "(2026) Genius Girlfriend") {
		t.Fatalf("new id = %q", got.ID)
	}

	// On disk: one renamed folder holding renamed videos, sidecars and optimized copies,
	// with meta.json and the poster untouched.
	dir := filepath.Join(dataDir, "Movies", "(2026) Genius Girlfriend")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	want := []string{
		"(2026) Genius Girlfriend - 1x1.en.srt",
		"(2026) Genius Girlfriend - 1x1.mkv",
		"(2026) Genius Girlfriend - 1x10.mkv",
		"(2026) Genius Girlfriend - 1x10.optimized.mp4",
		"meta.json",
		"poster.jpg",
	}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("folder contents = %v, want %v", names, want)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "Movies", "(0) Genius Girlfriend")); err == nil {
		t.Fatal("the old folder is still there")
	}

	// In the cache: the old id is gone and the new one carries the state meta.json kept.
	pool, err := s.ensureDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetMedia(ctx, pool, oldID); err == nil {
		t.Fatal("the old cache row survived the rename")
	}
	m, err := db.GetMedia(ctx, pool, got.ID)
	if err != nil {
		t.Fatalf("new cache row: %v", err)
	}
	if m.Title != "Genius Girlfriend" || m.Year != 2026 || m.Poster != "poster.jpg" {
		t.Fatalf("new row = %+v", m)
	}
	files, err := db.MediaFiles(ctx, pool, got.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("new files = %+v (%v)", files, err)
	}
	var watched, rating int
	if err := pool.QueryRowContext(ctx,
		`SELECT watched, rating FROM user_state WHERE user = 'admin' AND media_id = ?`, got.ID).
		Scan(&watched, &rating); err != nil {
		t.Fatalf("user state did not follow the rename: %v", err)
	}
	if watched != 1 || rating != 9 {
		t.Fatalf("user state = watched %d rating %d, want 1 and 9", watched, rating)
	}

	// The item is no longer reported, and a second rename has nothing to do.
	if items := misnamedList(t, h, admin); len(items) != 0 {
		t.Fatalf("still reported after the rename: %+v", items)
	}
	if rr := do(t, h, "POST", "/api/admin/media/"+got.ID+"/rename", "", admin); rr.Code != http.StatusConflict {
		t.Fatalf("second rename: %d %s, want 409", rr.Code, rr.Body.String())
	}
}

func TestRenameRefusedWhileTheItemIsBeingOptimized(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	s.cfg.MediaFormat = mediafmt.FileFin
	ctx := context.Background()

	id := seedShow(t, s, dataDir, "Movies", catID, "(0) Genius Girlfriend",
		importer.Meta{Title: "Genius Girlfriend", Year: 2026, Enriched: true},
		[]string{"(0) Genius Girlfriend - 1x1.mkv"})

	pool, err := s.ensureDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dataDir, "Movies", "(0) Genius Girlfriend", "(0) Genius Girlfriend - 1x1.mkv")
	if err := db.UpsertPendingTask(ctx, pool, id, 0, src, src+".optimized.mp4"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimNextTask(ctx, pool, "worker"); err != nil {
		t.Fatal(err)
	}
	if rr := do(t, h, "POST", "/api/admin/media/"+id+"/rename", "", admin); rr.Code != http.StatusConflict {
		t.Fatalf("rename during an encode: %d %s, want 409", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "Movies", "(0) Genius Girlfriend")); err != nil {
		t.Fatal("the folder should not have moved")
	}
}
