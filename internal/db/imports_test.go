package db

import (
	"context"
	"testing"
)

func TestImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	id, err := InsertImport(ctx, pool, Import{
		CategoryID: 1, SourcePath: "/src/a.mkv", Filename: "a.mkv",
		Title: "The Matrix", Year: 1999, Status: StatusPreCheck,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	imp, err := GetImport(ctx, pool, id)
	if err != nil || imp.Title != "The Matrix" || imp.Year != 1999 || imp.Status != StatusPreCheck {
		t.Fatalf("get: %+v %v", imp, err)
	}
	if imp.HasPoster {
		t.Fatal("HasPoster should be false without a poster")
	}

	if err := UpdateImportFields(ctx, pool, id, "Matrix", 2000); err != nil {
		t.Fatalf("update fields: %v", err)
	}
	imp, _ = GetImport(ctx, pool, id)
	if imp.Title != "Matrix" || imp.Year != 2000 {
		t.Fatalf("after update fields: %+v", imp)
	}

	if err := UpdateImportProgress(ctx, pool, id, StatusImporting, 50, 100, ""); err != nil {
		t.Fatalf("update progress: %v", err)
	}
	imp, _ = GetImport(ctx, pool, id)
	if imp.Status != StatusImporting || imp.Copied != 50 || imp.Total != 100 {
		t.Fatalf("after progress: %+v", imp)
	}

	rows, err := ListImports(ctx, pool, StatusImporting)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list importing: %d %v", len(rows), err)
	}
	active, _ := ListActiveImports(ctx, pool)
	if len(active) != 1 {
		t.Fatalf("active: %d", len(active))
	}

	if err := DeleteImport(ctx, pool, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rows, _ := ListImports(ctx, pool, ""); len(rows) != 0 {
		t.Fatalf("after delete: %d rows", len(rows))
	}
}

func TestSetImportStatusAndClear(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	for i := 0; i < 3; i++ {
		if _, err := InsertImport(ctx, pool, Import{CategoryID: 7, Status: StatusPreCheck}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := SetImportStatus(ctx, pool, StatusPreCheck, StatusImport)
	if err != nil || n != 3 {
		t.Fatalf("bulk status: %d %v", n, err)
	}
	if rows, _ := ListImports(ctx, pool, StatusImport); len(rows) != 3 {
		t.Fatalf("import rows: %d", len(rows))
	}

	// ClearImports only removes the matching (category, status) pair.
	_, _ = InsertImport(ctx, pool, Import{CategoryID: 7, Status: StatusPreCheck})
	if err := ClearImports(ctx, pool, 7, StatusPreCheck); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if rows, _ := ListImports(ctx, pool, StatusPreCheck); len(rows) != 0 {
		t.Fatalf("preCheck remaining: %d", len(rows))
	}
	if rows, _ := ListImports(ctx, pool, StatusImport); len(rows) != 3 {
		t.Fatalf("import rows after clear: %d", len(rows))
	}
}

func TestResetInterruptedImports(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	// One row mid-copy (importing), one queued, one done.
	mid, _ := InsertImport(ctx, pool, Import{Status: StatusImporting, Copied: 50, Total: 100})
	_, _ = InsertImport(ctx, pool, Import{Status: StatusImport})
	_, _ = InsertImport(ctx, pool, Import{Status: StatusDone})

	n, err := ResetInterruptedImports(ctx, pool)
	if err != nil || n != 1 {
		t.Fatalf("reset = %d %v, want 1", n, err)
	}
	// The interrupted row is requeued with its progress cleared; others are untouched.
	row, _ := GetImport(ctx, pool, mid)
	if row.Status != StatusImport || row.Copied != 0 || row.Total != 0 {
		t.Fatalf("recovered row = %+v", row)
	}
	if imports, _ := ListImports(ctx, pool, StatusImport); len(imports) != 2 {
		t.Fatalf("import rows after reset = %d, want 2", len(imports))
	}
	if done, _ := ListImports(ctx, pool, StatusDone); len(done) != 1 {
		t.Fatalf("done rows = %d, want 1", len(done))
	}
}

func TestMigrateAddsDeleteAfter(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	pool, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })

	// Simulate a cache built before delete_after existed: a minimal old imports table.
	if _, err := pool.ExecContext(ctx, `CREATE TABLE imports (
        id INTEGER PRIMARY KEY AUTOINCREMENT, category_id INTEGER, category TEXT,
        source_path TEXT, filename TEXT, title TEXT, year INTEGER, status TEXT,
        api_json TEXT, poster TEXT, copied INTEGER, total INTEGER, error TEXT)`); err != nil {
		t.Fatal(err)
	}
	if has, _ := hasColumn(ctx, pool, "imports", "delete_after"); has {
		t.Fatal("precondition: column should be absent")
	}

	// Build migrates the existing table in place.
	if err := Build(ctx, pool); err != nil {
		t.Fatalf("build/migrate: %v", err)
	}
	if has, _ := hasColumn(ctx, pool, "imports", "delete_after"); !has {
		t.Fatal("delete_after not added by migration")
	}
	// Inserts and round-trips work after migration.
	id, err := InsertImport(ctx, pool, Import{Status: StatusPreCheck, DeleteAfter: true})
	if err != nil {
		t.Fatalf("insert after migrate: %v", err)
	}
	imp, _ := GetImport(ctx, pool, id)
	if !imp.DeleteAfter {
		t.Fatalf("deleteAfter not persisted: %+v", imp)
	}
	// Build is idempotent: a second call does not re-add the column.
	if err := Build(ctx, pool); err != nil {
		t.Fatalf("second build: %v", err)
	}
}

func TestMediaInsert(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	m := Media{ID: "abc123", CategoryID: 1,
		Path: "/data/Movies/(1999) The Matrix", Year: 1999, Title: "The Matrix", Description: "d"}
	if err := InsertMedia(ctx, pool, m); err != nil {
		t.Fatalf("insert media: %v", err)
	}
	// REPLACE keeps a reimport idempotent.
	if err := InsertMedia(ctx, pool, m); err != nil {
		t.Fatalf("reinsert media: %v", err)
	}
	if err := InsertMediaFile(ctx, pool, MediaFile{MediaID: "abc123", Idx: 0, Path: "/x.mkv", Name: "x.mkv", Ext: ".mkv"}); err != nil {
		t.Fatalf("insert media file: %v", err)
	}

	var n int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("media count: %d %v", n, err)
	}
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_files`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("media_files count: %d %v", n, err)
	}
}
