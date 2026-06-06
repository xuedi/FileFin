package cache

import (
	"path/filepath"
	"testing"

	"filefin/internal/scanner"
)

func build(t *testing.T) *Store {
	t.Helper()
	scan, err := scanner.Scan("../scanner/testdata")
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
	return store
}

func TestRebuildAndQuery(t *testing.T) {
	store := build(t)

	cats, err := store.Categories()
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 2 || cats[0].Name != "Films - English" || cats[0].Count != 1 {
		t.Fatalf("categories: %+v", cats)
	}

	media, err := store.MediaByCategory("Films - English")
	if err != nil {
		t.Fatal(err)
	}
	if len(media) != 1 || media[0].Title != "The Gods Must Be Crazy" || !media[0].HasPoster {
		t.Fatalf("media: %+v", media)
	}

	d, err := store.MediaDetail(media[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Year != 1980 || len(d.Files) != 1 || len(d.Tags) != 1 || d.Tags[0] != "comedy" {
		t.Fatalf("detail: %+v", d)
	}

	p, err := store.FilePath(media[0].ID, 0)
	if err != nil || p == "" {
		t.Fatalf("file path: %q err=%v", p, err)
	}
}

// Rebuilding twice must produce identical query results (determinism).
func TestRebuildDeterministic(t *testing.T) {
	scan, err := scanner.Scan("../scanner/testdata")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cache.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Rebuild(scan); err != nil {
		t.Fatal(err)
	}
	first, _ := store.MediaByCategory("Shows - China")
	if err := store.Rebuild(scan); err != nil {
		t.Fatal(err)
	}
	second, _ := store.MediaByCategory("Shows - China")

	if len(first) != len(second) || len(first) != 1 || first[0].ID != second[0].ID {
		t.Fatalf("non-deterministic rebuild: %+v vs %+v", first, second)
	}
}
