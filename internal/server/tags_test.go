package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filefin/internal/importer"
)

// TestSetMediaTags checks the per-item write path: normalisation, meta.json as the source of
// truth, the facet mirror behind the tag-scoped search, and that genres are left alone.
func TestSetMediaTags(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	id, dir := seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mp4",
		importer.Meta{Title: "The Matrix", Year: 1999, Genres: []string{"action", "sci-fi"}})

	rr := do(t, h, "POST", "/api/admin/media/"+id+"/tags",
		`{"tags":["  Rewatch ","rewatch","4K","","favourite"]}`, admin)
	if rr.Code != 200 {
		t.Fatalf("set tags: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// Trimmed, lowercased, deduplicated, blanks dropped, order preserved.
	if len(got.Tags) != 3 || got.Tags[0] != "rewatch" || got.Tags[1] != "4k" || got.Tags[2] != "favourite" {
		t.Fatalf("normalised tags = %v", got.Tags)
	}

	// meta.json is the source of truth, and the genres survived the tag write.
	meta, err := importer.ReadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Tags) != 3 || meta.Tags[0] != "rewatch" {
		t.Fatalf("meta tags = %v", meta.Tags)
	}
	if len(meta.Genres) != 2 || meta.Genres[0] != "action" {
		t.Fatalf("genres clobbered by a tag write: %v", meta.Genres)
	}

	// The vocabulary endpoint sees them, with counts.
	rr = do(t, h, "GET", "/api/tags", "", admin)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"tag":"rewatch","count":1`) {
		t.Fatalf("list tags: %d %s", rr.Code, rr.Body.String())
	}

	// And the tag scope finds it while the genre scope does not.
	if rr := do(t, h, "GET", "/api/search?field=tag&q=4k", "", admin); !strings.Contains(rr.Body.String(), `"title":"The Matrix"`) {
		t.Fatalf("tag search: %s", rr.Body.String())
	}
	if rr := do(t, h, "GET", "/api/search?field=genre&q=4k", "", admin); strings.Contains(rr.Body.String(), `"title":"The Matrix"`) {
		t.Fatalf("genre scope matched a curated tag: %s", rr.Body.String())
	}

	// The detail payload carries the two lists separately.
	rr = do(t, h, "GET", "/api/media/"+id, "", admin)
	if !strings.Contains(rr.Body.String(), `"genres":["action","sci-fi"]`) ||
		!strings.Contains(rr.Body.String(), `"tags":["rewatch","4k","favourite"]`) {
		t.Fatalf("detail lists: %s", rr.Body.String())
	}
}

// TestTagsSurviveReEnrich checks that the enricher, which replaces the genres, never touches
// the curated tags - the whole reason the two lists are separate.
func TestTagsSurviveReEnrich(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	id, dir := seedMedia(t, s, dataDir, "Movies", catID, "(1994) Leon", "(1994) Leon.mp4",
		importer.Meta{Title: "Leon", Year: 1994, Genres: []string{"thriller"}})
	if rr := do(t, h, "POST", "/api/admin/media/"+id+"/tags", `{"tags":["rewatch"]}`, admin); rr.Code != 200 {
		t.Fatalf("set tags: %d", rr.Code)
	}

	// A metadata save is the manual replace-mode write the enricher shares its shape with.
	if rr := do(t, h, "POST", "/api/admin/media/"+id+"/meta",
		`{"title":"Leon","year":1994,"genres":["crime","drama"],"tags":["rewatch"]}`, admin); rr.Code != 204 {
		t.Fatalf("save meta: %d %s", rr.Code, rr.Body.String())
	}
	meta, err := importer.ReadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Genres) != 2 || meta.Genres[0] != "crime" {
		t.Fatalf("genres not replaced: %v", meta.Genres)
	}
	if len(meta.Tags) != 1 || meta.Tags[0] != "rewatch" {
		t.Fatalf("curated tags lost on a metadata save: %v", meta.Tags)
	}
}

// TestRenameAndDeleteTag covers the library-wide operations, including the merge that a
// rename onto an existing tag performs.
func TestRenameAndDeleteTag(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	a, dirA := seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mp4",
		importer.Meta{Title: "The Matrix", Year: 1999})
	b, dirB := seedMedia(t, s, dataDir, "Movies", catID, "(1994) Leon", "(1994) Leon.mp4",
		importer.Meta{Title: "Leon", Year: 1994})
	do(t, h, "POST", "/api/admin/media/"+a+"/tags", `{"tags":["scifi","rewatch"]}`, admin)
	do(t, h, "POST", "/api/admin/media/"+b+"/tags", `{"tags":["scifi"]}`, admin)

	// Rename across the library.
	if rr := do(t, h, "PUT", "/api/admin/tags/scifi", `{"tag":"sci-fi"}`, admin); rr.Code != 200 ||
		!strings.Contains(rr.Body.String(), `"changed":2`) {
		t.Fatalf("rename: %d %s", rr.Code, rr.Body.String())
	}
	if m, _ := importer.ReadMeta(dirB); len(m.Tags) != 1 || m.Tags[0] != "sci-fi" {
		t.Fatalf("rename did not reach disk: %v", m.Tags)
	}

	// Renaming onto an existing tag merges: the item carrying both ends with one.
	if rr := do(t, h, "PUT", "/api/admin/tags/rewatch", `{"tag":"sci-fi"}`, admin); rr.Code != 200 {
		t.Fatalf("merge: %d %s", rr.Code, rr.Body.String())
	}
	if m, _ := importer.ReadMeta(dirA); len(m.Tags) != 1 || m.Tags[0] != "sci-fi" {
		t.Fatalf("merge should deduplicate: %v", m.Tags)
	}

	// Delete strips it everywhere.
	if rr := do(t, h, "DELETE", "/api/admin/tags/sci-fi", "", admin); rr.Code != 200 ||
		!strings.Contains(rr.Body.String(), `"changed":2`) {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	if m, _ := importer.ReadMeta(dirA); len(m.Tags) != 0 {
		t.Fatalf("delete left tags behind: %v", m.Tags)
	}
	if rr := do(t, h, "GET", "/api/tags", "", admin); strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatalf("vocabulary not empty after delete: %s", rr.Body.String())
	}
}

// TestLegacyMetaUpgrade checks the version fold: a pre-version-2 meta.json stores its genres
// under the old "tags" key, and must read back as genres with no curated tags - never as tags.
func TestLegacyMetaUpgrade(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"title":"Alien","year":1979,"actors":["Sigourney Weaver"],"tags":["horror","sci-fi"]}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if !importer.NeedsUpgrade(dir) {
		t.Fatal("a file with no version key should need an upgrade")
	}
	m, err := importer.ReadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Genres) != 2 || m.Genres[0] != "horror" {
		t.Fatalf("legacy tags should fold into genres: %v", m.Genres)
	}
	if len(m.Tags) != 0 {
		t.Fatalf("legacy file must not yield curated tags: %v", m.Tags)
	}

	// Writing it back settles the on-disk shape, and the fold is then a no-op.
	if err := importer.WriteMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	if importer.NeedsUpgrade(dir) {
		t.Fatal("a rewritten file should be current")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "meta.json"))
	if !strings.Contains(string(raw), `"version": 2`) || !strings.Contains(string(raw), `"genres"`) {
		t.Fatalf("rewritten file: %s", raw)
	}
	again, _ := importer.ReadMeta(dir)
	if len(again.Genres) != 2 || len(again.Tags) != 0 {
		t.Fatalf("second read drifted: genres=%v tags=%v", again.Genres, again.Tags)
	}
}

// TestTagWritesAreAdminOnly checks a non-admin can read the vocabulary but not change it.
func TestTagWritesAreAdminOnly(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, bob := installedServer(t, dataDir)
	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Movies","alias":"Films"}`, admin); rr.Code != 200 {
		t.Fatalf("create category: %d %s", rr.Code, rr.Body.String())
	}
	catID := categoryRowID(t, s, "Movies")
	id, _ := seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mp4",
		importer.Meta{Title: "The Matrix", Year: 1999})

	if rr := do(t, h, "GET", "/api/tags", "", bob); rr.Code != 200 {
		t.Fatalf("a signed-in user should read the vocabulary: %d", rr.Code)
	}
	if rr := do(t, h, "POST", "/api/admin/media/"+id+"/tags", `{"tags":["nope"]}`, bob); rr.Code == 200 {
		t.Fatalf("a non-admin must not tag: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do(t, h, "DELETE", "/api/admin/tags/nope", "", bob); rr.Code == 200 {
		t.Fatalf("a non-admin must not delete a tag: %d", rr.Code)
	}
}
