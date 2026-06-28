package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// mediaTestPool opens a fresh built cache isolated to the test.
func mediaTestPool(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	pool, err := Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	ctx := context.Background()
	if err := Build(ctx, pool); err != nil {
		t.Fatalf("build: %v", err)
	}
	return ctx, pool
}

func TestMediaQueries(t *testing.T) {
	ctx, pool := mediaTestPool(t)

	if _, err := pool.ExecContext(ctx,
		`INSERT INTO categories (id, name, alias) VALUES (7, 'Movies', 'Films')`); err != nil {
		t.Fatal(err)
	}

	dir := "/data/Movies/(1999) The Matrix"
	if err := InsertMedia(ctx, pool, Media{
		ID: "abc123", CategoryID: 7, Path: dir,
		Year: 1999, Title: "The Matrix", Description: "desc", Plot: "plot", Poster: "poster.jpg",
	}); err != nil {
		t.Fatal(err)
	}
	if err := InsertMediaFile(ctx, pool, MediaFile{
		MediaID: "abc123", Idx: 0, Path: filepath.Join(dir, "(1999) The Matrix.mkv"),
		Name: "(1999) The Matrix.mkv", Ext: ".mkv",
	}); err != nil {
		t.Fatal(err)
	}
	// A second item with no poster, in the same category.
	if err := InsertMedia(ctx, pool, Media{
		ID: "def456", CategoryID: 7, Path: "/data/Movies/(2000) Other",
		Year: 2000, Title: "Other",
	}); err != nil {
		t.Fatal(err)
	}

	byCat, err := ListMediaByCategory(ctx, pool, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(byCat) != 2 {
		t.Fatalf("by category: got %d, want 2", len(byCat))
	}
	// Year-sorted: "The Matrix" (1999) before "Other" (2000).
	if byCat[0].Title != "The Matrix" || byCat[1].Title != "Other" {
		t.Fatalf("not year-sorted: %+v", byCat)
	}
	if !byCat[0].HasPoster || byCat[1].HasPoster {
		t.Fatalf("poster flags wrong: %+v", byCat)
	}

	all, err := AllMedia(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all media: got %d, want 2", len(all))
	}

	m, err := GetMedia(ctx, pool, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "The Matrix" || m.Year != 1999 || m.Poster != "poster.jpg" {
		t.Fatalf("get media: %+v", m)
	}

	files, err := MediaFiles(ctx, pool, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Ext != ".mkv" {
		t.Fatalf("media files: %+v", files)
	}

	folder, err := FolderPath(ctx, pool, "abc123")
	if err != nil || folder != dir {
		t.Fatalf("folder path: %q %v", folder, err)
	}
	if folder, _ := FolderPath(ctx, pool, "nope"); folder != "" {
		t.Fatalf("unknown id folder: %q", folder)
	}

	poster, err := PosterPath(ctx, pool, "abc123")
	if err != nil || poster != filepath.Join(dir, "poster.jpg") {
		t.Fatalf("poster path: %q %v", poster, err)
	}
	if p, _ := PosterPath(ctx, pool, "def456"); p != "" {
		t.Fatalf("no-poster item should yield empty path, got %q", p)
	}

	f, ok, err := FileAt(ctx, pool, "abc123", 0)
	if err != nil || !ok || f.Ext != ".mkv" || f.Path != filepath.Join(dir, "(1999) The Matrix.mkv") {
		t.Fatalf("file: %+v ok=%v %v", f, ok, err)
	}
	if _, ok, _ := FileAt(ctx, pool, "abc123", 9); ok {
		t.Fatalf("unknown index should yield ok=false")
	}
}

func TestListMediaInCategorySubtree(t *testing.T) {
	ctx, pool := mediaTestPool(t)

	// Anime (10) -> Seasonal (11); an item filed under the child must surface when the
	// parent is the scope root.
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO categories (id, name, alias, parent_id) VALUES (10, 'Anime', 'Anime', NULL), (11, 'Seasonal', 'Seasonal', 10)`); err != nil {
		t.Fatal(err)
	}
	if err := InsertMedia(ctx, pool, Media{ID: "p", CategoryID: 10, Path: "/d/a", Year: 2013, Title: "Parent Show"}); err != nil {
		t.Fatal(err)
	}
	if err := InsertMedia(ctx, pool, Media{ID: "c", CategoryID: 11, Path: "/d/b", Year: 2020, Title: "Child Show"}); err != nil {
		t.Fatal(err)
	}
	// A sibling outside the subtree must be excluded.
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO categories (id, name, alias) VALUES (20, 'Drama', 'Drama')`); err != nil {
		t.Fatal(err)
	}
	if err := InsertMedia(ctx, pool, Media{ID: "x", CategoryID: 20, Path: "/d/c", Year: 2019, Title: "Other"}); err != nil {
		t.Fatal(err)
	}

	got, err := ListMediaInCategorySubtree(ctx, pool, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "p" || got[1].ID != "c" {
		t.Fatalf("subtree of Anime should be [p, c] year-sorted: %+v", got)
	}
}
