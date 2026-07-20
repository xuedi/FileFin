package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"filefin/internal/db"
	"filefin/internal/library"
	"filefin/internal/mediafmt"
)

// importServer builds an installed server with a category, an import folder, and a
// chosen media format, returning the server, handler, admin cookie, and category id.
func importServer(t *testing.T, importFolder string) (*Server, http.Handler, *http.Cookie, int64) {
	t.Helper()
	dataDir := t.TempDir()
	s, h, admin, _ := installedServer(t, dataDir)
	s.cfg.ImportFolder = importFolder
	s.cfg.MediaFormat = mediafmt.FileFin
	// Create a category so it has a stable id in config.json + cache.
	pool, err := s.ensureDB(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.InsertCategory(context.Background(), pool, "Movies", "Films", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.Create(dataDir, "", "Movies", "Films", id, 0); err != nil {
		t.Fatal(err)
	}
	return s, h, admin, id
}

func do(t *testing.T, h http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestAssessEditDelete(t *testing.T) {
	imp := t.TempDir()
	// Two video files (one nested) plus a non-video that must be ignored.
	if err := os.WriteFile(filepath.Join(imp, "The.Matrix.1999.1080p.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(imp, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imp, "sub", "(1982) Blade Runner.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imp, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, h, admin, catID := importServer(t, imp)
	_ = s

	// Assess (OMDb disabled: no key configured) yields one preCheck row per video.
	rr := do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	if rr.Code != 200 {
		t.Fatalf("assess: %d %s", rr.Code, rr.Body.String())
	}
	var rows []db.Import
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("assess rows = %d, want 2: %s", len(rows), rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"title":"The Matrix"`) || !strings.Contains(body, `"title":"Blade Runner"`) {
		t.Fatalf("recognized titles missing: %s", body)
	}

	// Edit a row's title/year.
	var id int64
	for _, r := range rows {
		if r.Title == "The Matrix" {
			id = r.ID
		}
	}
	rr = do(t, h, "PUT", "/api/admin/imports/"+strconv.FormatInt(id, 10), `{"title":"Matrix","year":2000}`, admin)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"title":"Matrix"`) || !strings.Contains(rr.Body.String(), `"year":2000`) {
		t.Fatalf("edit: %d %s", rr.Code, rr.Body.String())
	}

	// Delete the other row.
	var other int64
	for _, r := range rows {
		if r.ID != id {
			other = r.ID
		}
	}
	if rr := do(t, h, "DELETE", "/api/admin/imports/"+strconv.FormatInt(other, 10), "", admin); rr.Code != 204 {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	rr = do(t, h, "GET", "/api/admin/imports?status=preCheck", "", admin)
	if c := strings.Count(rr.Body.String(), `"id":`); c != 1 {
		t.Fatalf("after delete want 1 row, body: %s", rr.Body.String())
	}

	// Re-assess clears prior preCheck rows (still 2, not 3).
	rr = do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("re-assess rows = %d, want 2", len(rows))
	}
}

func TestStartImportCopiesAndWritesMedia(t *testing.T) {
	imp := t.TempDir()
	content := strings.Repeat("movie-bytes", 100)
	if err := os.WriteFile(filepath.Join(imp, "(1999) The Matrix.mkv"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// A folder-level poster sitting beside the source should ride along into the media folder.
	if err := os.WriteFile(filepath.Join(imp, "poster.jpg"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, h, admin, catID := importServer(t, imp)

	rr := do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	if rr.Code != 200 {
		t.Fatalf("assess: %d %s", rr.Code, rr.Body.String())
	}

	// Start flips preCheck -> import.
	if rr := do(t, h, "POST", "/api/admin/import/start", "", admin); rr.Code != 200 ||
		!strings.Contains(rr.Body.String(), `"started":1`) {
		t.Fatalf("start: %d %s", rr.Code, rr.Body.String())
	}

	// Drive the worker directly (the poller would otherwise wait ~5s).
	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	rows, _ := db.ListImports(ctx, pool, db.StatusImport)
	if len(rows) != 1 {
		t.Fatalf("import rows = %d, want 1", len(rows))
	}
	s.importOne(ctx, pool, rows[0])

	// The media file was copied into the canonical layout.
	target := filepath.Join(s.cfg.DataDir, "Movies", "(1999) The Matrix", "(1999) The Matrix.mkv")
	got, err := os.ReadFile(target)
	if err != nil || string(got) != content {
		t.Fatalf("copied media missing/mismatch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), "meta.json")); err != nil {
		t.Fatalf("meta.json not written: %v", err)
	}
	// The sidecar poster was placed as poster.jpg and recorded on the media row.
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), "poster.jpg")); err != nil {
		t.Fatalf("poster not placed: %v", err)
	}

	// A media row was inserted and the import row marked done.
	var n int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("media rows = %d %v", n, err)
	}
	var posterCol string
	if err := pool.QueryRowContext(ctx, `SELECT poster FROM media`).Scan(&posterCol); err != nil || posterCol != "poster.jpg" {
		t.Fatalf("media poster = %q %v, want poster.jpg", posterCol, err)
	}
	done, _ := db.ListImports(ctx, pool, db.StatusDone)
	if len(done) != 1 {
		t.Fatalf("done rows = %d, want 1", len(done))
	}
}

func TestActiveImportsEndpoint(t *testing.T) {
	imp := t.TempDir()
	s, h, admin, _ := importServer(t, imp)

	// Seed an in-flight row directly and overlay live progress.
	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	id, _ := db.InsertImport(ctx, pool, db.Import{Title: "X", Status: db.StatusImporting})
	s.setProgress(id, 42, 100)

	rr := do(t, h, "GET", "/api/admin/imports/active", "", admin)
	if rr.Code != 200 {
		t.Fatalf("active: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"copied":42`) || !strings.Contains(rr.Body.String(), `"total":100`) {
		t.Fatalf("live progress overlay missing: %s", rr.Body.String())
	}
}

func TestSetOMDBKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // config.Save writes to ~/.filefin.json
	_, h, admin, _ := installedServer(t, t.TempDir())

	rr := do(t, h, "POST", "/api/admin/settings/omdb-key", `{"key":"abc123"}`, admin)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"omdbKey":"abc123"`) {
		t.Fatalf("set omdb key: %d %s", rr.Code, rr.Body.String())
	}
	// Empty key is allowed (disables enrichment).
	rr = do(t, h, "POST", "/api/admin/settings/omdb-key", `{"key":""}`, admin)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"omdbKey":""`) {
		t.Fatalf("clear omdb key: %d %s", rr.Code, rr.Body.String())
	}
}

func TestSetLogging(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // config.Save writes to ~/.filefin.json
	s, h, admin, bob := installedServer(t, t.TempDir())

	// Non-admin forbidden.
	if rr := do(t, h, "POST", "/api/admin/settings/logging", `{"level":"info","output":"STDOUT"}`, bob); rr.Code != 403 {
		t.Fatalf("non-admin: %d, want 403", rr.Code)
	}
	// Bad level rejected.
	if rr := do(t, h, "POST", "/api/admin/settings/logging", `{"level":"loud","output":"STDOUT"}`, admin); rr.Code != 400 {
		t.Fatalf("bad level: %d, want 400", rr.Code)
	}
	// Relative file path rejected.
	if rr := do(t, h, "POST", "/api/admin/settings/logging", `{"level":"info","output":"relative.log"}`, admin); rr.Code != 400 {
		t.Fatalf("relative output: %d, want 400", rr.Code)
	}
	// Valid level + file output persists and reconfigures the live logger.
	logFile := filepath.Join(t.TempDir(), "app.log")
	rr := do(t, h, "POST", "/api/admin/settings/logging", `{"level":"debug","output":"`+logFile+`"}`, admin)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"logLevel":"debug"`) ||
		!strings.Contains(rr.Body.String(), logFile) {
		t.Fatalf("set logging: %d %s", rr.Code, rr.Body.String())
	}
	// The live logger now writes debug JSON to the file.
	s.logger().For("backend").Info("hello")
	data, err := os.ReadFile(logFile)
	if err != nil || !strings.Contains(string(data), `"msg":"hello"`) {
		t.Fatalf("log file not written by live logger: %q %v", data, err)
	}
}

func TestDeleteAfterRemovesSource(t *testing.T) {
	imp := t.TempDir()
	src := filepath.Join(imp, "(1999) The Matrix.mkv")
	if err := os.WriteFile(src, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, h, admin, catID := importServer(t, imp)

	do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	// Start with deleteAfter=true: the source should be vacuumed after a good import.
	if rr := do(t, h, "POST", "/api/admin/import/start", `{"deleteAfter":true}`, admin); rr.Code != 200 {
		t.Fatalf("start: %d %s", rr.Code, rr.Body.String())
	}

	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	rows, _ := db.ListImports(ctx, pool, db.StatusImport)
	if len(rows) != 1 || !rows[0].DeleteAfter {
		t.Fatalf("staged row deleteAfter not set: %+v", rows)
	}
	s.importOne(ctx, pool, rows[0])

	// Copy landed in the library and the original is gone.
	target := filepath.Join(s.cfg.DataDir, "Movies", "(1999) The Matrix", "(1999) The Matrix.mkv")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("copied media missing: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source should have been removed, stat err = %v", err)
	}
}

func TestAssessFlagsMediaAlreadyInLibrary(t *testing.T) {
	imp := t.TempDir()
	for _, name := range []string{"(1999) The Matrix.mkv", "(1982) Blade Runner.mkv"} {
		if err := os.WriteFile(filepath.Join(imp, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, h, admin, catID := importServer(t, imp)

	// Import The Matrix, then assess the same folder again: only that row is flagged.
	do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	do(t, h, "POST", "/api/admin/import/start", "", admin)
	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	rows, _ := db.ListImports(ctx, pool, db.StatusImport)
	for _, r := range rows {
		if r.Title == "The Matrix" {
			s.importOne(ctx, pool, r)
		}
	}

	rr := do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	var staged []db.Import
	if err := json.Unmarshal(rr.Body.Bytes(), &staged); err != nil {
		t.Fatal(err)
	}
	for _, r := range staged {
		switch r.Title {
		case "The Matrix":
			if !strings.Contains(r.Duplicate, "The Matrix (1999)") {
				t.Fatalf("imported media should be flagged as a duplicate, got %q", r.Duplicate)
			}
		default:
			if r.Duplicate != "" {
				t.Fatalf("%s is not in the library but was flagged: %q", r.Title, r.Duplicate)
			}
		}
	}
}

// A show already in the library is not a duplicate per se: only an episode it already
// holds is, so the next episode of a running series stages clean.
func TestDuplicateCheckIsPerEpisode(t *testing.T) {
	imp := t.TempDir()
	for _, name := range []string{"Firefly - S01E01.mkv", "Firefly - S01E02.mkv"} {
		if err := os.WriteFile(filepath.Join(imp, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, h, admin, catID := importServer(t, imp)

	do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	do(t, h, "POST", "/api/admin/import/start", "", admin)
	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	rows, _ := db.ListImports(ctx, pool, db.StatusImport)
	for _, r := range rows {
		if r.Episode == 1 {
			s.importOne(ctx, pool, r)
		}
	}

	rr := do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	var staged []db.Import
	if err := json.Unmarshal(rr.Body.Bytes(), &staged); err != nil {
		t.Fatal(err)
	}
	if len(staged) != 2 {
		t.Fatalf("want 2 staged rows, got %d", len(staged))
	}
	for _, r := range staged {
		if r.Episode == 1 && r.Duplicate == "" {
			t.Fatalf("the already-imported episode should be flagged: %+v", r)
		}
		if r.Episode == 2 && r.Duplicate != "" {
			t.Fatalf("a new episode must not be flagged: %q", r.Duplicate)
		}
	}
}

func TestDeleteAfterClearsFolderAndSidecars(t *testing.T) {
	imp := t.TempDir()
	dir := filepath.Join(imp, "The.Matrix.1999.1080p", "release")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "(1999) The Matrix.mkv")
	sub := filepath.Join(dir, "(1999) The Matrix.eng.srt")
	poster := filepath.Join(dir, "poster.jpg")
	for _, p := range []string{src, sub, poster} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, h, admin, catID := importServer(t, imp)

	do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	do(t, h, "POST", "/api/admin/import/start", `{"deleteAfter":true}`, admin)

	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	rows, _ := db.ListImports(ctx, pool, db.StatusImport)
	if len(rows) != 1 {
		t.Fatalf("want 1 staged row, got %+v", rows)
	}
	s.importOne(ctx, pool, rows[0])

	// Everything the video came with is gone, and the folders it emptied with it - but
	// never the import folder itself.
	for _, p := range []string{src, sub, poster, dir, filepath.Dir(dir)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should have been vacuumed, stat err = %v", p, err)
		}
	}
	if _, err := os.Stat(imp); err != nil {
		t.Fatalf("import folder must survive: %v", err)
	}
}

// A folder still holding a video that has not been imported yet is kept: os.Remove refuses
// a non-empty directory, so the prune stops there.
func TestDeleteAfterKeepsFolderWithRemainingMedia(t *testing.T) {
	imp := t.TempDir()
	dir := filepath.Join(imp, "Show")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "Show - S01E01.mkv")
	other := filepath.Join(dir, "Show - S01E02.mkv")
	for _, p := range []string{src, other} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, h, admin, catID := importServer(t, imp)

	do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	do(t, h, "POST", "/api/admin/import/start", `{"deleteAfter":true}`, admin)

	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	rows, _ := db.ListImports(ctx, pool, db.StatusImport)
	for _, r := range rows {
		if r.SourcePath == src {
			s.importOne(ctx, pool, r)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("the not-yet-imported episode must survive: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("a folder that still holds media must survive: %v", err)
	}
}

func TestImportKeepsSourceByDefault(t *testing.T) {
	imp := t.TempDir()
	src := filepath.Join(imp, "(1982) Blade Runner.mkv")
	if err := os.WriteFile(src, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, h, admin, catID := importServer(t, imp)

	do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	// No body => deleteAfter defaults to false; the original must survive.
	do(t, h, "POST", "/api/admin/import/start", "", admin)

	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	rows, _ := db.ListImports(ctx, pool, db.StatusImport)
	if len(rows) != 1 || rows[0].DeleteAfter {
		t.Fatalf("deleteAfter should default false: %+v", rows)
	}
	s.importOne(ctx, pool, rows[0])
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source should remain when deleteAfter is false: %v", err)
	}
}

func TestRebuildFromDisk(t *testing.T) {
	imp := t.TempDir()
	src := filepath.Join(imp, "(1999) The Matrix.mkv")
	if err := os.WriteFile(src, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, h, admin, catID := importServer(t, imp)
	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)

	// Import one item so the media + a now-stale import row exist.
	do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	do(t, h, "POST", "/api/admin/import/start", "", admin)
	rows, _ := db.ListImports(ctx, pool, db.StatusImport)
	s.importOne(ctx, pool, rows[0])

	// Corrupt the cache: drop the media row and leave a stale import behind.
	if err := db.ClearMedia(ctx, pool); err != nil {
		t.Fatal(err)
	}
	_, _ = db.InsertImport(ctx, pool, db.Import{Title: "stale", Status: db.StatusError})

	// Rebuild flushes imports and re-derives media from disk.
	rr := do(t, h, "POST", "/api/admin/rebuild", "", admin)
	if rr.Code != 200 {
		t.Fatalf("rebuild: %d %s", rr.Code, rr.Body.String())
	}
	waitForRebuild(t, s)

	var media int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`).Scan(&media); err != nil || media != 1 {
		t.Fatalf("media after rebuild = %d %v", media, err)
	}
	var title string
	if err := pool.QueryRowContext(ctx, `SELECT title FROM media`).Scan(&title); err != nil || title != "The Matrix" {
		t.Fatalf("rebuilt media title = %q %v", title, err)
	}
	var imports int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM imports`).Scan(&imports); err != nil || imports != 0 {
		t.Fatalf("imports should be flushed by rebuild, got %d %v", imports, err)
	}
	// Non-admin is forbidden.
	if rr := do(t, h, "POST", "/api/admin/rebuild", "", bobCookie(t, s)); rr.Code != 403 {
		t.Fatalf("non-admin rebuild: %d, want 403", rr.Code)
	}
}

// bobCookie mints a session for the non-admin user created by installedServer.
func bobCookie(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	id, _ := s.sessions.create("bob")
	return &http.Cookie{Name: sessionCookie, Value: id}
}

// TestNoLockUnderConcurrency drives an import while the active-imports endpoint is
// polled hard, asserting the WAL + serialized-writer setup never returns SQLITE_BUSY.
func TestNoLockUnderConcurrency(t *testing.T) {
	imp := t.TempDir()
	if err := os.WriteFile(filepath.Join(imp, "(1999) The Matrix.mkv"),
		[]byte(strings.Repeat("x", 2_000_000)), 0o644); err != nil {
		t.Fatal(err)
	}
	s, h, admin, catID := importServer(t, imp)

	do(t, h, "POST", "/api/admin/import/assess", `{"categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	do(t, h, "POST", "/api/admin/import/start", "", admin)

	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	rows, _ := db.ListImports(ctx, pool, db.StatusImport)
	if len(rows) != 1 {
		t.Fatalf("import rows = %d, want 1", len(rows))
	}

	done := make(chan struct{})
	go func() {
		s.importOne(ctx, pool, rows[0])
		close(done)
	}()
	for {
		select {
		case <-done:
			d, _ := db.ListImports(ctx, pool, db.StatusDone)
			if len(d) != 1 {
				t.Errorf("import did not complete: %d done rows", len(d))
			}
			return
		default:
			rr := do(t, h, "GET", "/api/admin/imports/active", "", admin)
			if rr.Code != 200 {
				t.Fatalf("active poll failed (likely SQLITE_BUSY): %d %s", rr.Code, rr.Body.String())
			}
		}
	}
}

// uploadPart posts one file to the upload endpoint as a multipart body, sending the session
// field before the file part (the order the handler requires).
func uploadPart(t *testing.T, h http.Handler, cookie *http.Cookie, session, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("session", session)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/api/admin/import/upload/file", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestUploadImport(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, bob := installedServer(t, dataDir)
	s.cfg.MediaFormat = mediafmt.FileFin
	pool, err := s.ensureDB(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	catID, err := db.InsertCategory(context.Background(), pool, "Movies", "Films", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.Create(dataDir, "", "Movies", "Films", catID, 0); err != nil {
		t.Fatal(err)
	}

	// Non-admin cannot start an upload session.
	if rr := do(t, h, "POST", "/api/admin/import/upload/begin", "", bob); rr.Code != 403 {
		t.Fatalf("non-admin begin: %d, want 403", rr.Code)
	}

	// Admin opens a session and gets an opaque token.
	rr := do(t, h, "POST", "/api/admin/import/upload/begin", "", admin)
	if rr.Code != 200 {
		t.Fatalf("begin: %d %s", rr.Code, rr.Body.String())
	}
	var begun struct{ Session string }
	if err := json.Unmarshal(rr.Body.Bytes(), &begun); err != nil {
		t.Fatal(err)
	}
	if begun.Session == "" {
		t.Fatal("empty session token")
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Join(os.TempDir(), begun.Session)) })

	// A bogus / traversal session token is rejected.
	if rr := uploadPart(t, h, admin, "../etc", "The.Matrix.1999.mkv", "x"); rr.Code != 400 {
		t.Fatalf("bogus session upload: %d, want 400", rr.Code)
	}

	// Upload a real file into the session; it lands in the session's /tmp dir.
	if rr := uploadPart(t, h, admin, begun.Session, "The.Matrix.1999.1080p.mkv", "movie-bytes"); rr.Code != 204 {
		t.Fatalf("upload: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(os.TempDir(), begun.Session, "The.Matrix.1999.1080p.mkv")); err != nil {
		t.Fatalf("uploaded file not on disk: %v", err)
	}

	// Assess the uploaded session: one preCheck row, recognised, with delete_after forced on.
	rr = do(t, h, "POST", "/api/admin/import/upload/assess",
		`{"session":"`+begun.Session+`","categoryId":`+strconv.FormatInt(catID, 10)+`}`, admin)
	if rr.Code != 200 {
		t.Fatalf("upload assess: %d %s", rr.Code, rr.Body.String())
	}
	var rows []db.Import
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("assess rows = %d, want 1: %s", len(rows), rr.Body.String())
	}
	if rows[0].Title != "The Matrix" {
		t.Fatalf("title = %q, want The Matrix", rows[0].Title)
	}
	if !rows[0].DeleteAfter {
		t.Fatal("upload row must have delete_after set")
	}
}
