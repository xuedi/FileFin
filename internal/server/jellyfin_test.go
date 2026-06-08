package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/mediafmt"
)

// writeFile creates a file (and its parents) with the given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestJellyfinImportEndToEnd drives the Jellyfin front stage: prepare stages preCheck
// rows from an NFO library (creating a category), and the importer writes the NFO
// metadata to meta.json while leaving the folder unenriched.
func TestJellyfinImportEndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, bob := installedServer(t, dataDir)
	s.cfg.MediaFormat = mediafmt.FileFin

	// A small Jellyfin library: a foldered movie with a poster, and a show with one
	// episode under a Season 01 folder.
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "The Matrix (1999)", "The Matrix.mkv"), "movie-bytes")
	writeFile(t, filepath.Join(src, "The Matrix (1999)", "poster.jpg"), "img")
	writeFile(t, filepath.Join(src, "The Matrix (1999)", "movie.nfo"),
		`<movie><title>The Matrix</title><year>1999</year><plot>Truth.</plot><genre>Action</genre></movie>`)
	writeFile(t, filepath.Join(src, "Firefly (2002)", "tvshow.nfo"),
		`<tvshow><title>Firefly</title><year>2002</year></tvshow>`)
	writeFile(t, filepath.Join(src, "Firefly (2002)", "Season 01", "Firefly S01E01.mkv"), "ep")

	// Non-admin is forbidden.
	if rr := do(t, h, "POST", "/api/admin/import/jellyfin/prepare",
		`{"sourceDir":"`+src+`","create":true,"category":"Library"}`, bob); rr.Code != 403 {
		t.Fatalf("non-admin prepare: %d, want 403", rr.Code)
	}

	// Prepare stages the library, creating a category named "Library".
	body := `{"sourceDir":"` + src + `","create":true,"category":"Library"}`
	if rr := do(t, h, "POST", "/api/admin/import/jellyfin/prepare", body, admin); rr.Code != http.StatusAccepted {
		t.Fatalf("prepare: %d %s", rr.Code, rr.Body.String())
	}

	// Poll progress until the background staging finishes.
	deadline := time.Now().Add(5 * time.Second)
	var prog stagingJobState
	for time.Now().Before(deadline) {
		rr := do(t, h, "GET", "/api/admin/import/jellyfin/progress", "", admin)
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

	// Two preCheck rows, tagged jellyfin, carrying a metadata blob, delete-after off.
	ctx := context.Background()
	pool, _ := s.ensureDB(ctx)
	rows, _ := db.ListImports(ctx, pool, db.StatusPreCheck)
	if len(rows) != 2 {
		t.Fatalf("preCheck rows = %d, want 2", len(rows))
	}
	var matrix, episode *db.Import
	for i := range rows {
		if rows[i].Origin != db.OriginJellyfin {
			t.Fatalf("origin = %q, want jellyfin", rows[i].Origin)
		}
		if rows[i].APIJSON == "" {
			t.Fatal("jellyfin row should carry a metadata blob in api_json")
		}
		if rows[i].DeleteAfter {
			t.Fatal("jellyfin rows must never delete originals")
		}
		switch rows[i].Title {
		case "The Matrix":
			matrix = &rows[i]
		case "Firefly":
			episode = &rows[i]
		}
	}
	if matrix == nil || matrix.Year != 1999 || !matrix.HasPoster {
		t.Fatalf("matrix row = %+v", matrix)
	}
	if episode == nil || episode.Season != 1 || episode.Episode != 1 {
		t.Fatalf("firefly episode row = %+v", episode)
	}

	// The category was created from the typed name (folder and alias "Library").
	if cat, ok := s.categoryByName("Library"); !ok || cat.Alias != "Library" {
		t.Fatalf("created category = %+v ok=%v", cat, ok)
	}

	// Importing the movie row writes the NFO metadata to meta.json but leaves the
	// folder unenriched, so the OMDb enricher fills gaps additively later.
	s.importOne(ctx, pool, *matrix)
	folder := mediafmt.FolderName(mediafmt.FileFin, matrix.Year, matrix.Title)
	meta, err := importer.ReadMeta(filepath.Join(dataDir, "Library", folder))
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if meta.Enriched || meta.Description != "Truth." {
		t.Fatalf("jellyfin meta should be unenriched but carry NFO fields: %+v", meta)
	}
	if len(meta.Tags) != 1 || meta.Tags[0] != "action" {
		t.Fatalf("meta tags = %v, want [action]", meta.Tags)
	}
}
