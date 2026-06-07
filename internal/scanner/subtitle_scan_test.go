package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

// Sidecar .srt files are attached to the media file whose base name they share,
// with the language read from the infix; a foreign .srt is not attached.
func TestScanAttachesSubtitles(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "Films", "(1967) The Assassin")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"(1967) The Assassin.avi",
		"(1967) The Assassin.srt",     // no infix -> default language
		"(1967) The Assassin.en.srt",  // english
		"(1967) The Assassin.zho.srt", // alias -> zh
		"Other Movie.srt",             // foreign, must be ignored
		"(1967) The Assassin.en.ass",  // not .srt, must be ignored
	} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	scan, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Categories) != 1 || len(scan.Categories[0].Media) != 1 {
		t.Fatalf("scan shape: %+v", scan.Categories)
	}
	files := scan.Categories[0].Media[0].Files
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	subs := files[0].Subtitles
	if len(subs) != 3 {
		t.Fatalf("want 3 subtitles, got %d: %+v", len(subs), subs)
	}
	// Sorted by language: "en", "en" (the untagged default), "zh".
	langs := []string{subs[0].Lang, subs[1].Lang, subs[2].Lang}
	wantLangs := []string{"en", "en", "zh"}
	for i := range wantLangs {
		if langs[i] != wantLangs[i] {
			t.Fatalf("languages = %v, want %v", langs, wantLangs)
		}
	}
	for _, s := range subs {
		if filepath.Base(s.Path) == "Other Movie.srt" {
			t.Errorf("foreign subtitle attached: %q", s.Path)
		}
		if filepath.Ext(s.Path) != ".srt" {
			t.Errorf("non-srt attached: %q", s.Path)
		}
	}
}
