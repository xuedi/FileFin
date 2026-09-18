package people

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNameKey(t *testing.T) {
	same := [][2]string{
		{"Xiwei Tian", "Tian Xiwei"},          // given and family name swapped between sources
		{"Marie Laforêt", "Marie Laforet"},    // accents
		{"Choi Ji-woo", "Choi Jiwoo"},         // hyphens
		{"Peter O'Toole", "Peter OToole"},     // apostrophes
		{"Peter O’Toole", "Peter O'Toole"},    // typographic apostrophe
		{"  keanu   REEVES ", "Keanu Reeves"}, // case and spacing
		{"Samuel L. Jackson", "Samuel L Jackson"},
	}
	for _, p := range same {
		if a, b := NameKey(p[0]), NameKey(p[1]); a != b {
			t.Errorf("NameKey(%q) = %q, NameKey(%q) = %q; want equal", p[0], a, p[1], b)
		}
	}
	different := [][2]string{
		{"Choi Ji-woo", "Choi Ji-won"},
		{"Tian Xiwei", "Tian Xi"},
	}
	for _, p := range different {
		if NameKey(p[0]) == NameKey(p[1]) {
			t.Errorf("NameKey(%q) and NameKey(%q) must differ", p[0], p[1])
		}
	}
	if NameKey(" - ") != "" {
		t.Errorf("a name with no letters folds to empty, got %q", NameKey(" - "))
	}
}

func TestStore(t *testing.T) {
	dataDir := t.TempDir()
	s := New(dataDir)

	if s.Has(6384) || s.PhotoPath(6384) != "" || s.Count() != 0 {
		t.Fatal("a fresh store holds nobody")
	}
	rec := Record{ID: 6384, Name: "Keanu Reeves", Department: "Acting", Birthday: "1964-09-02", Fetched: 1}
	if err := s.Write(rec); err != nil {
		t.Fatal(err)
	}
	if !s.Has(6384) || s.Count() != 1 {
		t.Fatal("a written person must be stored")
	}
	got, err := s.Read(6384)
	if err != nil || got != rec {
		t.Fatalf("read = %+v, %v; want %+v", got, err, rec)
	}
	if _, err := os.Stat(filepath.Join(dataDir, DirName, "6384", recordName)); err != nil {
		t.Fatalf("person.json must live under the data dir's %s: %v", DirName, err)
	}
	if err := s.Write(Record{Name: "nobody"}); err == nil {
		t.Error("a record without an id must be refused")
	}
}

// TestSavePhotoWithoutEncoder: when ffmpeg cannot encode the photo, the downloaded JPEG is
// kept, so the person still has a face.
func TestSavePhotoWithoutEncoder(t *testing.T) {
	s := New(t.TempDir())
	if err := s.SavePhoto(context.Background(), "/nonexistent/ffmpeg", 42, []byte("jpeg")); err != nil {
		t.Fatal(err)
	}
	p := s.PhotoPath(42)
	if filepath.Base(p) != photoJPEG {
		t.Fatalf("photo path = %q, want the kept %s", p, photoJPEG)
	}
	if s.Has(42) {
		t.Error("a photo alone does not mark a person stored; the record does")
	}
}
