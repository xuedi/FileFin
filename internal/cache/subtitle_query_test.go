package cache

import (
	"os"
	"path/filepath"
	"testing"

	"filefin/internal/scanner"
)

// A scan with subtitles round-trips through Rebuild: MediaDetail surfaces them per
// file, and SubtitlePath resolves each back to its on-disk file.
func TestSubtitlesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "Films", "(1967) The Assassin")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"(1967) The Assassin.avi",
		"(1967) The Assassin.en.srt",
		"(1967) The Assassin.de.srt",
	} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	scan, err := scanner.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Rebuild(scan); err != nil {
		t.Fatal(err)
	}

	id := scan.Categories[0].Media[0].ID
	d, err := store.MediaDetail(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(d.Files))
	}
	subs := d.Files[0].Subtitles
	if len(subs) != 2 {
		t.Fatalf("want 2 subtitles, got %d: %+v", len(subs), subs)
	}
	if subs[0].Lang != "de" || subs[0].Label != "German" || subs[0].Index != 0 {
		t.Errorf("subtitle 0: %+v", subs[0])
	}
	if subs[1].Lang != "en" || subs[1].Label != "English" || subs[1].Index != 1 {
		t.Errorf("subtitle 1: %+v", subs[1])
	}

	p, err := store.SubtitlePath(id, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "(1967) The Assassin.en.srt" {
		t.Errorf("SubtitlePath = %q, want the .en.srt", p)
	}
}
