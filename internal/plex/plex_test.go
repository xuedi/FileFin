package plex

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSplitTags(t *testing.T) {
	got := splitTags("Crime|Drama| |Thriller")
	want := []string{"Crime", "Drama", "Thriller"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if splitTags("") != nil {
		t.Fatal("empty should be nil")
	}
}

func TestToDate(t *testing.T) {
	cases := map[string]string{
		"-309657600":           "1960-03-10",
		"":                     "",
		"2014-09-01":           "2014-09-01",
		"1999-03-31T00:00:00Z": "1999-03-31",
	}
	for in, want := range cases {
		if got := toDate(in); got != want {
			t.Errorf("toDate(%q)=%q want %q", in, got, want)
		}
	}
}

func TestArtworkPath(t *testing.T) {
	dir := "/meta"
	hash := "a000ec1d539ef47158152454be418f84c2db89f2"
	url := "metadata://posters/tv.plex.agents.movie_19edb5"
	want := filepath.Join(dir, "Movies", "a", "000ec1d539ef47158152454be418f84c2db89f2.bundle",
		"Contents", "_combined", "posters", "tv.plex.agents.movie_19edb5")
	if got := artworkPath(dir, "Movies", hash, url); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// upload:// artwork lives under the bundle's Uploads/ dir.
	wantUpload := filepath.Join(dir, "Movies", "a", "000ec1d539ef47158152454be418f84c2db89f2.bundle",
		"Uploads", "posters", "im.dat")
	if got := artworkPath(dir, "Movies", hash, "upload://posters/im.dat"); got != wantUpload {
		t.Fatalf("upload got %q want %q", got, wantUpload)
	}
	// Unknown schemes and empty inputs resolve to "".
	if artworkPath(dir, "Movies", hash, "http://x/y.jpg") != "" {
		t.Fatal("unknown scheme should be empty")
	}
	if artworkPath("", "Movies", hash, url) != "" {
		t.Fatal("empty metadata dir should be empty")
	}
}

// artwork() resolves only when the bundle file actually exists.
func TestArtworkExistenceCheck(t *testing.T) {
	root := t.TempDir()
	hash := "abc123def456"
	bundle := filepath.Join(root, "Movies", "a", "bc123def456.bundle", "Contents", "_combined", "posters")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "p1"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &DB{metadataDir: root}
	if got := d.artwork("Movies", hash, "metadata://posters/p1"); got == "" {
		t.Fatal("expected resolved poster path")
	}
	if got := d.artwork("Movies", hash, "metadata://posters/missing"); got != "" {
		t.Fatalf("missing file should resolve to empty, got %q", got)
	}
}
