package db

import (
	"context"
	"testing"
)

func TestUpsertPendingThumbnailDedupe(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	if err := UpsertPendingThumbnail(ctx, pool, "m1", false); err != nil {
		t.Fatal(err)
	}
	// A second upsert for the same media must not create a duplicate.
	if err := UpsertPendingThumbnail(ctx, pool, "m1", false); err != nil {
		t.Fatal(err)
	}
	if n, err := CountPendingThumbnail(ctx, pool); err != nil || n != 1 {
		t.Fatalf("CountPendingThumbnail = %d (%v), want 1", n, err)
	}
}

func TestClaimNextThumbnailAtomicity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	// other_media flag must round-trip through claim.
	if err := UpsertPendingThumbnail(ctx, pool, "m1", true); err != nil {
		t.Fatal(err)
	}

	t1, ok, err := ClaimNextThumbnail(ctx, pool, "thumb")
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if t1.MediaID != "m1" || !t1.OtherMedia || t1.Status != ThumbStatusGenerating || t1.Agent != "thumb" {
		t.Fatalf("first claim row = %+v", t1)
	}
	// The only pending row is now generating; a second claim returns nothing.
	if _, ok, err := ClaimNextThumbnail(ctx, pool, "thumb"); err != nil || ok {
		t.Fatalf("second claim: ok=%v err=%v, want no row", ok, err)
	}
	if n, _ := CountPendingThumbnail(ctx, pool); n != 0 {
		t.Fatalf("pending after claim = %d, want 0", n)
	}
}

func TestClaimNextThumbnailDistinctRows(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	for i := 0; i < 3; i++ {
		if err := UpsertPendingThumbnail(ctx, pool, mediaID(i), false); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int64]bool{}
	for i := 0; i < 3; i++ {
		tk, ok, err := ClaimNextThumbnail(ctx, pool, "thumb")
		if err != nil || !ok {
			t.Fatalf("claim %d: ok=%v err=%v", i, ok, err)
		}
		if seen[tk.ID] {
			t.Fatalf("claim returned duplicate id %d", tk.ID)
		}
		seen[tk.ID] = true
	}
}

func TestListActiveThumbnail(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := InsertMedia(ctx, pool, Media{ID: "m1", Title: "Django"}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPendingThumbnail(ctx, pool, "m1", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ClaimNextThumbnail(ctx, pool, "thumb"); err != nil {
		t.Fatal(err)
	}
	active, err := ListActiveThumbnail(ctx, pool)
	if err != nil || len(active) != 1 {
		t.Fatalf("ListActiveThumbnail = %v (%v)", active, err)
	}
	a := active[0]
	if a.Title != "Django" || a.Agent != "thumb" || a.Status != ThumbStatusGenerating {
		t.Fatalf("active thumbnail = %+v", a)
	}
}

func TestResetGeneratingToPending(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := UpsertPendingThumbnail(ctx, pool, "m1", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ClaimNextThumbnail(ctx, pool, "thumb"); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPendingThumbnail(ctx, pool); n != 0 {
		t.Fatalf("pending before reset = %d, want 0", n)
	}
	if err := ResetGeneratingToPending(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPendingThumbnail(ctx, pool); n != 1 {
		t.Fatalf("pending after reset = %d, want 1", n)
	}
}

func TestPruneThumbnail(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := UpsertPendingThumbnail(ctx, pool, "m1", false); err != nil {
		t.Fatal(err)
	}
	// Pending row is pruned.
	if err := PruneThumbnail(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPendingThumbnail(ctx, pool); n != 0 {
		t.Fatalf("pending after prune = %d, want 0", n)
	}
	// A generating row is left alone.
	if err := UpsertPendingThumbnail(ctx, pool, "m2", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ClaimNextThumbnail(ctx, pool, "thumb"); err != nil {
		t.Fatal(err)
	}
	if err := PruneThumbnail(ctx, pool, "m2"); err != nil {
		t.Fatal(err)
	}
	if active, _ := ListActiveThumbnail(ctx, pool); len(active) != 1 {
		t.Fatalf("generating row should survive prune, active = %v", active)
	}
}

func TestFinishAndFailThumbnail(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := UpsertPendingThumbnail(ctx, pool, "m1", false); err != nil {
		t.Fatal(err)
	}
	tk, _, _ := ClaimNextThumbnail(ctx, pool, "thumb")
	if err := FinishThumbnail(ctx, pool, tk.ID); err != nil {
		t.Fatal(err)
	}
	if active, _ := ListActiveThumbnail(ctx, pool); len(active) != 0 {
		t.Fatalf("finished task should be gone, active = %v", active)
	}

	if err := UpsertPendingThumbnail(ctx, pool, "m2", false); err != nil {
		t.Fatal(err)
	}
	tk2, _, _ := ClaimNextThumbnail(ctx, pool, "thumb")
	if err := FailThumbnail(ctx, pool, tk2.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	// A failed row is neither pending nor active.
	if n, _ := CountPendingThumbnail(ctx, pool); n != 0 {
		t.Fatalf("failed task counted as pending: %d", n)
	}
	if active, _ := ListActiveThumbnail(ctx, pool); len(active) != 0 {
		t.Fatalf("failed task counted as active: %v", active)
	}
}

func TestClearThumbnailTasksAll(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := UpsertPendingThumbnail(ctx, pool, "m1", false); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPendingThumbnail(ctx, pool, "m2", false); err != nil {
		t.Fatal(err)
	}
	if err := ClearThumbnailTasksAll(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPendingThumbnail(ctx, pool); n != 0 {
		t.Fatalf("pending after clear-all = %d, want 0", n)
	}
}
