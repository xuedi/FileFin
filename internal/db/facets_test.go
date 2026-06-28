package db

import (
	"context"
	"database/sql"
	"testing"
)

func countFacets(t *testing.T, ctx context.Context, pool *sql.DB, id, kind string) int {
	t.Helper()
	var n int
	if err := pool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_facets WHERE media_id = ? AND kind = ?`, id, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMediaFacets(t *testing.T) {
	ctx, pool := mediaTestPool(t)

	// Scalar facets ride on the media row.
	if err := InsertMedia(ctx, pool, Media{
		ID: "m1", CategoryID: 1, Path: "/d/m1", Year: 1999, Title: "The Matrix",
		Language: "English", Country: "USA", Director: "The Wachowskis", Writer: "The Wachowskis",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := GetMedia(ctx, pool, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Language != "English" || got.Country != "USA" || got.Director != "The Wachowskis" {
		t.Fatalf("scalar facets not round-tripped: %+v", got)
	}

	// Multivalued facets land in media_facets, tagged by kind, skipping empties.
	if err := ReplaceMediaFacets(ctx, pool, "m1",
		[]string{"Keanu Reeves", "Carrie-Anne Moss", ""}, []string{"action", "sci-fi"}); err != nil {
		t.Fatal(err)
	}
	if n := countFacets(t, ctx, pool, "m1", "actor"); n != 2 {
		t.Fatalf("actors: got %d, want 2 (empty dropped)", n)
	}
	if n := countFacets(t, ctx, pool, "m1", "tag"); n != 2 {
		t.Fatalf("tags: got %d, want 2", n)
	}

	// Replace swaps the whole set rather than appending.
	if err := ReplaceMediaFacets(ctx, pool, "m1", []string{"Neo"}, nil); err != nil {
		t.Fatal(err)
	}
	if n := countFacets(t, ctx, pool, "m1", "actor"); n != 1 {
		t.Fatalf("after replace actors: got %d, want 1", n)
	}
	if n := countFacets(t, ctx, pool, "m1", "tag"); n != 0 {
		t.Fatalf("after replace tags: got %d, want 0", n)
	}

	// SetMediaFacets updates just the scalar columns (the enricher's path).
	if err := SetMediaFacets(ctx, pool, "m1", "French", "France", "Luc Besson", "Luc Besson"); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetMedia(ctx, pool, "m1"); got.Language != "French" || got.Director != "Luc Besson" {
		t.Fatalf("SetMediaFacets did not update: %+v", got)
	}

	// DeleteMedia also clears the facets.
	if err := DeleteMedia(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	if n := countFacets(t, ctx, pool, "m1", "actor"); n != 0 {
		t.Fatalf("facets not cleared on delete: got %d", n)
	}
}
