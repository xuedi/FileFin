package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/mediafmt"

	_ "modernc.org/sqlite"
)

// writePlexDB builds a minimal Plex database with one movie library ("Films") whose
// two movies' files are recorded under dbPrefix. It returns the database path.
func writePlexDB(t *testing.T, dbPrefix string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "library.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	stmts := []string{
		`CREATE TABLE library_sections (id INTEGER PRIMARY KEY, name TEXT, section_type INTEGER)`,
		`CREATE TABLE metadata_items (id INTEGER PRIMARY KEY, library_section_id INTEGER, parent_id INTEGER,
			metadata_type INTEGER, title TEXT, year INTEGER, "index" INTEGER, summary TEXT,
			originally_available_at TEXT, duration INTEGER, rating REAL, content_rating TEXT,
			tags_genre TEXT, tags_director TEXT, tags_writer TEXT, tags_star TEXT, hash TEXT,
			user_thumb_url TEXT, deleted_at TEXT)`,
		`CREATE TABLE media_items (id INTEGER PRIMARY KEY, metadata_item_id INTEGER)`,
		`CREATE TABLE media_parts (id INTEGER PRIMARY KEY, media_item_id INTEGER, file TEXT, "index" INTEGER, deleted_at TEXT)`,
		`CREATE TABLE media_streams (id INTEGER PRIMARY KEY, media_part_id INTEGER, stream_type_id INTEGER, url TEXT, language TEXT, codec TEXT)`,
		`INSERT INTO library_sections VALUES (1,'Films',1)`,
		`INSERT INTO metadata_items (id,library_section_id,metadata_type,title,year,summary,tags_genre,tags_star) VALUES
			(10,1,1,'Alpha',2001,'an alpha movie','Action|Drama','Jane|Joe'),
			(11,1,1,'Beta',2002,'a beta movie','Comedy','Sam')`,
		`INSERT INTO media_items VALUES (100,10),(101,11)`,
		`INSERT INTO media_parts (id,media_item_id,file,"index") VALUES
			(1000,100,'` + dbPrefix + `/films/Alpha/Alpha.mkv',0),
			(1001,101,'` + dbPrefix + `/films/Beta/Beta.mkv',0)`,
	}
	for _, s := range stmts {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("plex db setup: %v\n%s", err, s)
		}
	}
	return path
}

// TestPlexImportEndToEnd drives the Plex front stage: check lists libraries, resolve
// auto-detects the remap from the DB prefix to the real files, prepare stages preCheck
// rows carrying Plex metadata, and the importer writes an enriched meta.json.
func TestPlexImportEndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, _ := installedServer(t, dataDir)
	s.cfg.MediaFormat = mediafmt.FileFin

	// The DB recorded files under /plexsrc/... but they really live under base/...
	base := t.TempDir()
	for _, p := range []string{"films/Alpha/Alpha.mkv", "films/Beta/Beta.mkv"} {
		full := filepath.Join(base, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("movie-bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := writePlexDB(t, "/plexsrc")

	// Check lists the Films library.
	rr := do(t, h, "POST", "/api/admin/import/plex/check", `{"dbPath":"`+dbPath+`"}`, admin)
	if rr.Code != 200 {
		t.Fatalf("check: %d %s", rr.Code, rr.Body.String())
	}
	var secs []struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &secs); err != nil {
		t.Fatal(err)
	}
	if len(secs) != 1 || secs[0].Name != "Films" || secs[0].Count != 2 {
		t.Fatalf("check sections = %+v", secs)
	}

	// Resolve with the search base auto-detects the prefix remap and goes green.
	rr = do(t, h, "POST", "/api/admin/import/plex/resolve",
		`{"dbPath":"`+dbPath+`","sections":["Films"],"searchBase":"`+base+`"}`, admin)
	if rr.Code != 200 {
		t.Fatalf("resolve: %d %s", rr.Code, rr.Body.String())
	}
	var res []plexResolution
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Status != "green" || res[0].From == "" {
		t.Fatalf("resolve result = %+v", res)
	}

	// Prepare stages preCheck rows, creating a category from the Plex library name.
	prepare := `{"dbPath":"` + dbPath + `","selections":[{"section":"Films","categoryId":0,"create":true}],` +
		`"remaps":[{"section":"Films","from":"` + res[0].From + `","to":"` + res[0].To + `"}]}`
	if rr := do(t, h, "POST", "/api/admin/import/plex/prepare", prepare, admin); rr.Code != http.StatusAccepted {
		t.Fatalf("prepare: %d %s", rr.Code, rr.Body.String())
	}

	// Poll progress until the background staging finishes.
	deadline := time.Now().Add(5 * time.Second)
	var prog stagingJobState
	for time.Now().Before(deadline) {
		rr := do(t, h, "GET", "/api/admin/import/plex/progress", "", admin)
		if err := json.Unmarshal(rr.Body.Bytes(), &prog); err != nil {
			t.Fatal(err)
		}
		if prog.Finished {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !prog.Finished || prog.Error != "" || prog.Staged != 2 || prog.Missing != 0 {
		t.Fatalf("staging job = %+v", prog)
	}

	// Two preCheck rows, tagged plex, carrying a metadata blob, delete-after off.
	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	rows, _ := db.ListImports(ctx, pool, db.StatusPreCheck)
	if len(rows) != 2 {
		t.Fatalf("preCheck rows = %d, want 2", len(rows))
	}
	for _, r := range rows {
		if r.Origin != db.OriginPlex {
			t.Fatalf("origin = %q, want plex", r.Origin)
		}
		if r.APIJSON == "" {
			t.Fatal("plex row should carry a metadata blob in api_json")
		}
		if r.DeleteAfter {
			t.Fatal("plex rows must never delete originals")
		}
	}

	// The category was created from the Plex library name (alias = the library name).
	if cat, ok := s.categoryByName("Films"); !ok || cat.Alias != "Films" {
		t.Fatalf("created category = %+v ok=%v", cat, ok)
	}

	// Importing a row writes Plex's metadata to meta.json but leaves the folder
	// unenriched, so the OMDb enricher will later fill any gaps additively.
	s.importOne(ctx, pool, rows[0])
	var enriched int
	if err := pool.QueryRowContext(ctx, `SELECT enriched FROM media`).Scan(&enriched); err != nil || enriched != 0 {
		t.Fatalf("media enriched = %d %v, want 0", enriched, err)
	}
	folder := mediafmt.FolderName(mediafmt.FileFin, rows[0].Year, rows[0].Title)
	meta, err := importer.ReadMeta(filepath.Join(dataDir, "Films", folder))
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if meta.Enriched || meta.Description == "" {
		t.Fatalf("Plex meta should be unenriched but carry Plex fields: %+v", meta)
	}
}

// TestPlexResolveAsIsNoBase confirms a co-located library (files already at their DB
// paths) goes green with no search base supplied.
func TestPlexResolveAsIsNoBase(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, _ := installedServer(t, dataDir)
	_ = s

	base := t.TempDir()
	for _, p := range []string{"films/Alpha/Alpha.mkv", "films/Beta/Beta.mkv"} {
		full := filepath.Join(base, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// DB paths point straight at the real files (co-located install).
	dbPath := writePlexDB(t, base)

	rr := do(t, h, "POST", "/api/admin/import/plex/resolve",
		`{"dbPath":"`+dbPath+`","sections":["Films"]}`, admin)
	if rr.Code != 200 {
		t.Fatalf("resolve: %d %s", rr.Code, rr.Body.String())
	}
	var res []plexResolution
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Status != "green" || res[0].From != "" {
		t.Fatalf("as-is resolve = %+v", res)
	}
}
