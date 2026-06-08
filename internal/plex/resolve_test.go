package plex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemapApply(t *testing.T) {
	r := Remap{From: "/mnt/plex/", To: "/srv/media/"}
	if got := r.Apply("/mnt/plex/films/X/x.mkv"); got != "/srv/media/films/X/x.mkv" {
		t.Fatalf("apply: %q", got)
	}
	if got := r.Apply("/other/x.mkv"); got != "/other/x.mkv" {
		t.Fatal("non-matching prefix should be unchanged")
	}
	if got := (Remap{}).Apply("/a/b"); got != "/a/b" {
		t.Fatal("identity remap should be unchanged")
	}
}

// mkfiles creates each path (with parent dirs) under root and returns root.
func mkfiles(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestResolveAsIs(t *testing.T) {
	// Files exist exactly at their DB paths -> identity remap, green.
	root := mkfiles(t, "films/A/a.mkv", "films/B/b.mkv")
	samples := []string{
		filepath.Join(root, "films/A/a.mkv"),
		filepath.Join(root, "films/B/b.mkv"),
	}
	res := Resolve(samples, "")
	if res.Status != PathGreen || res.Remap.From != "" {
		t.Fatalf("as-is: %+v", res)
	}
}

func TestResolveNeedsInputWithoutBase(t *testing.T) {
	// DB paths do not exist and no search base is given -> needsInput.
	samples := []string{"/nope/films/A/a.mkv", "/nope/films/B/b.mkv"}
	res := Resolve(samples, "")
	if res.Status != PathNeedsInput {
		t.Fatalf("want needsInput, got %+v", res)
	}
}

func TestResolveRemapDetected(t *testing.T) {
	// The DB recorded /mnt/data/plex/... but the files live under root/<section>/...
	base := mkfiles(t,
		"films-japan/(2020) A/a.mkv",
		"films-japan/(2019) B/b.mkv",
		"films-japan/(2018) C/c.mkv",
	)
	samples := []string{
		"/mnt/data/plex/films-japan/(2020) A/a.mkv",
		"/mnt/data/plex/films-japan/(2019) B/b.mkv",
		"/mnt/data/plex/films-japan/(2018) C/c.mkv",
	}
	res := Resolve(samples, base)
	if res.Status != PathGreen {
		t.Fatalf("want green, got %+v", res)
	}
	if res.Remap.Apply(samples[0]) != filepath.Join(base, "films-japan/(2020) A/a.mkv") {
		t.Fatalf("remap wrong: %+v", res.Remap)
	}
	if res.Found != 3 {
		t.Fatalf("found %d want 3", res.Found)
	}
}

func TestResolveUnresolvedWhenOnlyBasenameMatches(t *testing.T) {
	// Only the basenames coincide under the base (flat dump), nothing structural ->
	// not auto-accepted.
	base := mkfiles(t, "a.mkv", "b.mkv")
	samples := []string{
		"/mnt/data/plex/films/deepA/a.mkv",
		"/mnt/data/plex/films/deepB/b.mkv",
	}
	res := Resolve(samples, base)
	if res.Status == PathGreen {
		t.Fatalf("basename-only must not be green: %+v", res)
	}
}
