package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"filefin/internal/db"
	"filefin/internal/library"
)

// misfiledList reads the misfiled-media report.
func misfiledList(t *testing.T, h http.Handler, admin *http.Cookie) []misfiledMedia {
	t.Helper()
	rr := do(t, h, "GET", "/api/admin/misfiled", "", admin)
	if rr.Code != 200 {
		t.Fatalf("misfiled: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Items []misfiledMedia `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Items
}

func TestMisfiledMediaFlagsContradictedOriginOnly(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, _ := installedServer(t, dataDir)
	ctx := context.Background()
	pool, err := s.ensureDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Two categories that declare their origin, and one that declares nothing.
	mk := func(leaf, alias string, m library.Markers) int64 {
		id, err := db.InsertCategory(ctx, pool, leaf, alias, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := library.Create(dataDir, "", leaf, alias, id, 0); err != nil {
			t.Fatal(err)
		}
		if !m.Empty() {
			if err := library.SetMarkers(dataDir, leaf, m); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	china := mk("Films - China", "Films - China", library.Markers{Languages: []string{"Mandarin"}, Countries: []string{"China"}})
	mk("Films - Korea", "Films - Korea", library.Markers{Languages: []string{"Korean"}, Countries: []string{"South Korea"}})
	plain := mk("Films - Misc", "Films - Misc", library.Markers{})
	if err := s.mirrorCategories(ctx, pool); err != nil {
		t.Fatal(err)
	}

	add := func(id, title string, catID int64, lang, country string, enriched bool) {
		if err := db.InsertMedia(ctx, pool, db.Media{
			ID: id, CategoryID: catID, Path: "/x/" + id, Title: title, Year: 2021,
			Enriched: enriched, Language: lang, Country: country,
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("a", "Jirisan", china, "Korean", "South Korea", true)   // contradicted
	add("b", "The Knockout", china, "Mandarin", "China", true)  // agrees
	add("c", "Some Film", plain, "Korean", "South Korea", true) // no rule to break
	add("d", "Not Yet", china, "", "", false)                   // not looked up yet

	items := misfiledList(t, h, admin)
	if len(items) != 1 {
		t.Fatalf("want exactly the contradicted item, got %+v", items)
	}
	if items[0].ID != "a" || items[0].Category != "Films - China" || items[0].Suggest != "Films - Korea" {
		t.Fatalf("flagged item = %+v, want Jirisan suggested into the Korean category", items[0])
	}
}
