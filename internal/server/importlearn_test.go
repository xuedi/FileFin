package server

import (
	"os"
	"path/filepath"
	"testing"

	"filefin/internal/library"
)

// learnedOf reads what a category has learned from disk (config.json is the source of truth).
func learnedOf(t *testing.T, dataDir, name string) map[string]int {
	t.Helper()
	cats, err := library.List(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cats {
		if c.Name == name {
			return c.Markers.Learned
		}
	}
	t.Fatalf("no category %q", name)
	return nil
}

func TestImportLearnsMarkersOncePerMedia(t *testing.T) {
	imp := t.TempDir()
	// A film signed by one group, and a twelve-episode show signed by another.
	if err := os.WriteFile(filepath.Join(imp, "The.Mad.Monk.1993.1080p-JKCT.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	show := filepath.Join(imp, "[LostYears] Call of the Night")
	if err := os.MkdirAll(show, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 12; i++ {
		name := filepath.Join(show, "[LostYears] Call of the Night - "+pad(i)+" [1080p].mkv")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s, h, admin, catID := importServer(t, imp)
	items := scanImport(t, h, admin)
	if len(items) != 2 {
		t.Fatalf("want 2 media, got %d: %+v", len(items), items)
	}
	if rr := startImportOf(t, h, admin, catID, false, items); rr.Code != 200 {
		t.Fatalf("start: %d %s", rr.Code, rr.Body.String())
	}

	learned := learnedOf(t, s.cfg.DataDir, "Movies")
	if learned["grp:jkct"] != 1 {
		t.Errorf("the film's release group = %d, want 1: %+v", learned["grp:jkct"], learned)
	}
	if learned["tag:lostyears"] != 1 {
		t.Errorf("a 12-file show must teach once, not twelve times: %+v", learned)
	}
	// Packaging is never a marker: it says nothing about who released the file.
	if _, ok := learned["tag:1080p"]; ok {
		t.Errorf("packaging leaked into the learned markers: %+v", learned)
	}

	// A second import of the same film raises its count.
	items = scanImport(t, h, admin)
	for _, it := range items {
		if it.Title == "The Mad Monk" {
			if rr := startImportOf(t, h, admin, catID, false, []scanItem{it}); rr.Code != 200 {
				t.Fatalf("second start: %d %s", rr.Code, rr.Body.String())
			}
		}
	}
	if learned = learnedOf(t, s.cfg.DataDir, "Movies"); learned["grp:jkct"] != 2 {
		t.Errorf("second import of the same group = %d, want 2: %+v", learned["grp:jkct"], learned)
	}
}

func TestFailedImportTeachesNothing(t *testing.T) {
	imp := t.TempDir()
	if err := os.WriteFile(filepath.Join(imp, "Jirisan.2021.1080p-AppleTor.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, h, admin, _ := importServer(t, imp)
	items := scanImport(t, h, admin)
	// An unknown category stages nothing, so nothing is learned by anyone.
	if rr := startImportOf(t, h, admin, 999, false, items); rr.Code != 200 {
		t.Fatalf("start: %d %s", rr.Code, rr.Body.String())
	}
	if learned := learnedOf(t, s.cfg.DataDir, "Movies"); len(learned) != 0 {
		t.Fatalf("a staged-nothing import must teach nothing: %+v", learned)
	}
}

func pad(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
