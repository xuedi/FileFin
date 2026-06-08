package db

import (
	"context"
	"database/sql"
	"testing"
)

func testPool(t *testing.T) *sql.DB {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	pool, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := Build(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestBuildAndCategories(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	// Build is idempotent.
	if err := Build(ctx, pool); err != nil {
		t.Fatalf("build again: %v", err)
	}

	id, err := InsertCategory(ctx, pool, "Movies", "Films", 0)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id <= 0 {
		t.Fatalf("want a positive auto id, got %d", id)
	}
	n, err := CountCategories(ctx, pool)
	if err != nil || n != 1 {
		t.Fatalf("count = %d, %v; want 1", n, err)
	}

	if err := UpdateCategoryAlias(ctx, pool, "Movies", "Cinema", true); err != nil {
		t.Fatalf("update alias: %v", err)
	}
	var alias string
	var otherMedia bool
	if err := pool.QueryRowContext(ctx, `SELECT alias, other_media FROM categories WHERE name = ?`, "Movies").Scan(&alias, &otherMedia); err != nil {
		t.Fatal(err)
	}
	if alias != "Cinema" || !otherMedia {
		t.Fatalf("alias = %q otherMedia = %v, want Cinema true", alias, otherMedia)
	}

	if err := DeleteCategory(ctx, pool, "Movies"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n, _ := CountCategories(ctx, pool); n != 0 {
		t.Fatalf("count after delete = %d, want 0", n)
	}
}

func TestReplaceCategoriesKeepsIDs(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	want := []Category{{ID: 5, Name: "Movies", Alias: "Films"}, {ID: 9, Name: "Shows", Alias: "TV"}}
	if err := ReplaceCategories(ctx, pool, want); err != nil {
		t.Fatalf("replace: %v", err)
	}
	var id int64
	if err := pool.QueryRowContext(ctx, `SELECT id FROM categories WHERE name = ?`, "Shows").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != 9 {
		t.Fatalf("explicit id not preserved: got %d, want 9", id)
	}
	// Replace again (rebuild) is clean, not additive.
	if err := ReplaceCategories(ctx, pool, want); err != nil {
		t.Fatalf("replace again: %v", err)
	}
	if n, _ := CountCategories(ctx, pool); n != 2 {
		t.Fatalf("count after rebuild = %d, want 2", n)
	}
}

func TestReplaceCategoriesPropagatesOtherMedia(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	// Movies (other-media root) -> Movies/Home -> Movies/Home/2024; Shows is a normal root.
	cats := []Category{
		{ID: 1, Name: "Movies", Alias: "Home", OtherMedia: true},
		{ID: 2, Name: "Movies/Home", ParentID: 1, Alias: "Home", OtherMedia: false},
		{ID: 3, Name: "Movies/Home/2024", ParentID: 2, Alias: "2024", OtherMedia: false},
		{ID: 4, Name: "Shows", Alias: "TV", OtherMedia: false},
	}
	if err := ReplaceCategories(ctx, pool, cats); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got := map[int64]bool{}
	rows, err := pool.QueryContext(ctx, `SELECT id, other_media FROM categories`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var other bool
		if err := rows.Scan(&id, &other); err != nil {
			t.Fatal(err)
		}
		got[id] = other
	}
	// The whole Movies subtree inherits the root's other-media flag; Shows stays false.
	for id, want := range map[int64]bool{1: true, 2: true, 3: true, 4: false} {
		if got[id] != want {
			t.Errorf("effective other_media for id %d = %v, want %v", id, got[id], want)
		}
	}
}
