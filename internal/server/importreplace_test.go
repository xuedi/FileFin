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
	"filefin/internal/ffprobe"
	"filefin/internal/importer"
	"filefin/internal/state"
)

func TestSupersededFiles(t *testing.T) {
	dir := "/data/Movies/(1999) The Matrix"
	film := []db.MediaFile{{Path: dir + "/(1999) The Matrix.mkv", Season: 0, Episode: 0}}
	discs := []db.MediaFile{
		{Path: dir + "/(1999) The Matrix - part1.avi"},
		{Path: dir + "/(1999) The Matrix - part2.avi"},
	}
	show := []db.MediaFile{
		{Path: dir + "/(2010) Show S1E1.mkv", Season: 1, Episode: 1},
		{Path: dir + "/(2010) Show S1E2.mkv", Season: 1, Episode: 2},
	}
	cases := []struct {
		name            string
		files           []db.MediaFile
		season, episode int
		target          string
		want            []string
		wantAmbiguous   bool
	}{
		{
			name:  "a changed extension retires the old file",
			files: film, target: dir + "/(1999) The Matrix.mp4",
			want: []string{"(1999) The Matrix.mkv"},
		},
		{
			name:  "the same name retires nothing (it was overwritten in place)",
			files: film, target: dir + "/(1999) The Matrix.mkv",
		},
		{
			name:  "one disc of a split film leaves the other alone",
			files: discs, target: dir + "/(1999) The Matrix - part1.mkv",
			want: []string{"(1999) The Matrix - part1.avi"},
		},
		{
			name:  "an unrecognisable pair is left alone entirely",
			files: discs, target: dir + "/(1999) Matrix CD1.mkv",
			wantAmbiguous: true,
		},
		{
			name:  "a season pack retires only the episode it replaces",
			files: show, season: 1, episode: 2, target: dir + "/(2010) Show S1E2.mp4",
			want: []string{"(2010) Show S1E2.mkv"},
		},
		{
			name:  "an episode the library does not hold retires nothing",
			files: show, season: 1, episode: 7, target: dir + "/(2010) Show S1E7.mkv",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old, ambiguous := supersededFiles(tc.files, tc.season, tc.episode, tc.target)
			if ambiguous != tc.wantAmbiguous {
				t.Fatalf("ambiguous = %v, want %v", ambiguous, tc.wantAmbiguous)
			}
			var got []string
			for _, f := range old {
				got = append(got, filepath.Base(f.Path))
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("retired %v, want %v", got, tc.want)
			}
		})
	}
}

func TestKeepProbedFormats(t *testing.T) {
	dir := "/data/Shows/(2010) Show"
	target := dir + "/(2010) Show S1E2.mp4"
	known := []db.MediaFile{
		{Path: dir + "/(2010) Show S1E1.mkv", Container: "matroska", VideoCodec: "hevc", AudioCodec: "ac3"},
		{Path: dir + "/(2010) Show S1E2.mkv", Container: "matroska", VideoCodec: "hevc", AudioCodec: "ac3"},
	}
	fresh := []db.MediaFile{
		{Path: dir + "/(2010) Show S1E1.mkv"},
		{Path: target},
	}
	keepProbedFormats(fresh, known, target, ffprobe.Technical{Container: "mov,mp4", VideoCodec: "h264", AudioCodec: "aac"})

	// The episode that was not touched keeps what the cache knew about it.
	if fresh[0].Container != "matroska" || fresh[0].VideoCodec != "hevc" {
		t.Fatalf("untouched episode lost its probed format: %+v", fresh[0])
	}
	// The replacement takes the format the import just probed, not its predecessor's.
	if fresh[1].Container != "mov,mp4" || fresh[1].VideoCodec != "h264" || fresh[1].AudioCodec != "aac" {
		t.Fatalf("replacement did not take the fresh probe: %+v", fresh[1])
	}
}

// startReplaceOf presses Import with every row marked as replacing the library copy.
func startReplaceOf(t *testing.T, h http.Handler, admin *http.Cookie, catID int64, items []scanItem) *httptest.ResponseRecorder {
	t.Helper()
	type item struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Year       int    `json:"year"`
		CategoryID int64  `json:"categoryId"`
		Replace    bool   `json:"replace"`
	}
	body := struct {
		DeleteAfter bool   `json:"deleteAfter"`
		Items       []item `json:"items"`
	}{DeleteAfter: true}
	for _, it := range items {
		body.Items = append(body.Items, item{it.ID, it.Title, it.Year, catID, true})
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return do(t, h, "POST", "/api/admin/import/folder/start", string(b), admin)
}

// importOneRow drains the single queued import row through the worker (the poller would
// otherwise wait for its next tick) and returns the row it ran.
func importOneRow(t *testing.T, s *Server) db.Import {
	t.Helper()
	ctx := context.Background()
	pool, err := s.ensureDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListImports(ctx, pool, db.StatusImport)
	if err != nil || len(rows) != 1 {
		t.Fatalf("queued rows = %d %v, want 1", len(rows), err)
	}
	s.importOne(ctx, pool, rows[0])
	return rows[0]
}

// seedLibraryItem imports one film from the import folder and returns its media folder and
// media id: the library item a later replace acts on.
func seedLibraryItem(t *testing.T, s *Server, h http.Handler, admin *http.Cookie, catID int64) (string, string) {
	t.Helper()
	importFolder(t, h, admin, catID, true)
	importOneRow(t, s)
	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	var id string
	if err := pool.QueryRowContext(ctx, `SELECT id FROM media`).Scan(&id); err != nil {
		t.Fatalf("no media row after the first import: %v", err)
	}
	return filepath.Join(s.cfg.DataDir, "Movies", "(1999) The Matrix"), id
}

func TestReplaceSwapsThePayloadAndKeepsTheIdentity(t *testing.T) {
	imp := t.TempDir()
	if err := os.WriteFile(filepath.Join(imp, "(1999) The Matrix.mkv"), []byte(strings.Repeat("old", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	s, h, admin, catID := importServer(t, imp)
	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	dir, idBefore := seedLibraryItem(t, s, h, admin, catID)

	// Curate the item the way a user would, and give it a pre-transcode of the old payload.
	meta, err := importer.ReadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta.Tags = []string{"rewatch"}
	meta.State = map[string]state.UserState{"someone": {Watched: true, Rating: 9}}
	if err := importer.WriteMeta(dir, meta); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "(1999) The Matrix.optimized.mp4")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A better rip of the same film shows up in the import folder.
	newBody := strings.Repeat("new", 400)
	if err := os.WriteFile(filepath.Join(imp, "The.Matrix.1999.2160p.mp4"), []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}
	items := scanImport(t, h, admin)
	if len(items) != 1 {
		t.Fatalf("scanned %d items, want 1", len(items))
	}
	if items[0].DuplicateID != idBefore {
		t.Fatalf("duplicate id = %q, want %q", items[0].DuplicateID, idBefore)
	}
	if rr := startReplaceOf(t, h, admin, catID, items); rr.Code != 200 {
		t.Fatalf("start replace: %d %s", rr.Code, rr.Body.String())
	}
	row := importOneRow(t, s)
	if row.ReplaceMediaID != idBefore {
		t.Fatalf("staged replace id = %q, want %q", row.ReplaceMediaID, idBefore)
	}

	// The new payload sits in the existing folder, under that folder's naming.
	target := filepath.Join(dir, "(1999) The Matrix.mp4")
	if got, err := os.ReadFile(target); err != nil || string(got) != newBody {
		t.Fatalf("replacement not placed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "(1999) The Matrix.mkv")); !os.IsNotExist(err) {
		t.Fatal("the superseded file is still there")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("the pre-transcode of the old payload is still there")
	}

	// The item kept its identity: same id, same folder, curated fields and state intact.
	var n int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("media rows = %d %v, want 1", n, err)
	}
	var idAfter string
	if err := pool.QueryRowContext(ctx, `SELECT id FROM media`).Scan(&idAfter); err != nil || idAfter != idBefore {
		t.Fatalf("media id = %q, want %q (%v)", idAfter, idBefore, err)
	}
	after, err := importer.ReadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Tags) != 1 || after.Tags[0] != "rewatch" {
		t.Fatalf("curated tags lost: %v", after.Tags)
	}
	if us := after.State["someone"]; !us.Watched || us.Rating != 9 {
		t.Fatalf("playback state lost: %+v", us)
	}

	// The cache follows the disk: one file row, pointing at the new payload.
	files, err := db.MediaFiles(ctx, pool, idAfter)
	if err != nil || len(files) != 1 {
		t.Fatalf("media files = %d %v, want 1", len(files), err)
	}
	if files[0].Path != target {
		t.Fatalf("cached file = %q, want %q", files[0].Path, target)
	}
}

func TestReplaceOfAVanishedItemImportsNormally(t *testing.T) {
	imp := t.TempDir()
	if err := os.WriteFile(filepath.Join(imp, "(1999) The Matrix.mkv"), []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, h, admin, catID := importServer(t, imp)
	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	importFolder(t, h, admin, catID, false)

	// The library item named by the row is gone by the time the worker reaches it.
	rows, _ := db.ListImports(ctx, pool, db.StatusImport)
	if len(rows) != 1 {
		t.Fatalf("queued rows = %d, want 1", len(rows))
	}
	if err := db.UpdateImportReplace(ctx, pool, rows[0].ID, "0123456789ab"); err != nil {
		t.Fatal(err)
	}
	row := importOneRow(t, s)
	if row.ReplaceMediaID != "0123456789ab" {
		t.Fatalf("replace id = %q", row.ReplaceMediaID)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.DataDir, "Movies", "(1999) The Matrix", "(1999) The Matrix.mkv")); err != nil {
		t.Fatalf("the file was not imported as new: %v", err)
	}
	var n int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("media rows = %d %v, want 1", n, err)
	}
}

func TestDuplicateComparison(t *testing.T) {
	imp := t.TempDir()
	oldBody := strings.Repeat("old", 100)
	if err := os.WriteFile(filepath.Join(imp, "(1999) The Matrix.mkv"), []byte(oldBody), 0o644); err != nil {
		t.Fatal(err)
	}
	s, h, admin, catID := importServer(t, imp)
	_, mediaID := seedLibraryItem(t, s, h, admin, catID)

	newBody := strings.Repeat("new", 400)
	if err := os.WriteFile(filepath.Join(imp, "The.Matrix.1999.2160p.mp4"), []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}
	items := scanImport(t, h, admin)
	if len(items) != 1 || items[0].DuplicateID == "" {
		t.Fatalf("expected one duplicate item, got %+v", items)
	}
	rr := do(t, h, "GET", "/api/admin/import/folder/"+items[0].ID+"/duplicate", "", admin)
	if rr.Code != 200 {
		t.Fatalf("compare: %d %s", rr.Code, rr.Body.String())
	}
	var got dupCompare
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.MediaID != mediaID {
		t.Fatalf("compared against %q, want %q", got.MediaID, mediaID)
	}
	if got.Incoming.FileBytes != int64(len(newBody)) || got.Library.FileBytes != int64(len(oldBody)) {
		t.Fatalf("sizes = incoming %d / library %d", got.Incoming.FileBytes, got.Library.FileBytes)
	}
	if got.Incoming.Path != "The.Matrix.1999.2160p.mp4" {
		t.Fatalf("incoming path = %q", got.Incoming.Path)
	}
	if got.Library.Path != "Movies/(1999) The Matrix/(1999) The Matrix.mkv" {
		t.Fatalf("library path = %q", got.Library.Path)
	}
	if got.Library.Category != "Movies" {
		t.Fatalf("library category = %q", got.Library.Category)
	}

	// An item the library does not hold has nothing to compare against.
	if err := os.WriteFile(filepath.Join(imp, "(2011) Drive.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, it := range scanImport(t, h, admin) {
		if it.Title != "Drive" {
			continue
		}
		if rr := do(t, h, "GET", "/api/admin/import/folder/"+it.ID+"/duplicate", "", admin); rr.Code != 404 {
			t.Fatalf("compare of a new media: %d, want 404", rr.Code)
		}
	}
}

func TestMediaDetailNamesTheFileAndItsSize(t *testing.T) {
	imp := t.TempDir()
	body := strings.Repeat("movie", 200)
	if err := os.WriteFile(filepath.Join(imp, "(1999) The Matrix.mkv"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, h, admin, catID := importServer(t, imp)
	_, mediaID := seedLibraryItem(t, s, h, admin, catID)

	rr := do(t, h, "GET", "/api/media/"+mediaID, "", admin)
	if rr.Code != 200 {
		t.Fatalf("detail: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Files []struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(got.Files))
	}
	if got.Files[0].Path != "Movies/(1999) The Matrix/(1999) The Matrix.mkv" {
		t.Fatalf("path = %q", got.Files[0].Path)
	}
	if got.Files[0].Size != int64(len(body)) {
		t.Fatalf("size = %d, want %d", got.Files[0].Size, len(body))
	}
}
