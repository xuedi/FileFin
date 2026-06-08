package db

import (
	"context"
	"testing"
)

func TestUpsertPendingTaskDedupe(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	if err := UpsertPendingTask(ctx, pool, "m1", 0, "/src/a.avi", "/src/a.optimized.mp4"); err != nil {
		t.Fatal(err)
	}
	// A second upsert for the same media/file must not create a duplicate.
	if err := UpsertPendingTask(ctx, pool, "m1", 0, "/src/a.avi", "/src/a.optimized.mp4"); err != nil {
		t.Fatal(err)
	}
	if n, err := CountPending(ctx, pool); err != nil || n != 1 {
		t.Fatalf("CountPending = %d (%v), want 1", n, err)
	}
}

func TestClaimAtomicity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	if err := UpsertPendingTask(ctx, pool, "m1", 0, "/src/a.avi", "/src/a.optimized.mp4"); err != nil {
		t.Fatal(err)
	}

	t1, ok, err := ClaimNextTask(ctx, pool, "GPU")
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if t1.MediaID != "m1" || t1.Status != OptimizeStatusEncoding || t1.Agent != "GPU" {
		t.Fatalf("first claim row = %+v", t1)
	}
	// The only pending row is now encoding; a second claim returns nothing.
	if _, ok, err := ClaimNextTask(ctx, pool, "CPU"); err != nil || ok {
		t.Fatalf("second claim: ok=%v err=%v, want no row", ok, err)
	}
	if n, _ := CountPending(ctx, pool); n != 0 {
		t.Fatalf("pending after claim = %d, want 0", n)
	}
}

func TestClaimDistinctRows(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	for i := 0; i < 3; i++ {
		if err := UpsertPendingTask(ctx, pool, "m", i, "/src/a.avi", "/src/a.optimized.mp4"); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int64]bool{}
	for i := 0; i < 3; i++ {
		tk, ok, err := ClaimNextTask(ctx, pool, "CPU")
		if err != nil || !ok {
			t.Fatalf("claim %d: ok=%v err=%v", i, ok, err)
		}
		if seen[tk.ID] {
			t.Fatalf("claim returned duplicate id %d", tk.ID)
		}
		seen[tk.ID] = true
	}
}

func TestListActiveAndPercent(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := InsertMedia(ctx, pool, Media{ID: "m1", Title: "Django"}); err != nil {
		t.Fatal(err)
	}
	if err := InsertMediaFile(ctx, pool, MediaFile{MediaID: "m1", Idx: 0, Name: "Django.avi", Ext: ".avi"}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPendingTask(ctx, pool, "m1", 0, "/src/Django.avi", "/src/Django.optimized.mp4"); err != nil {
		t.Fatal(err)
	}
	tk, _, err := ClaimNextTask(ctx, pool, "GPU")
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateTaskPercent(ctx, pool, tk.ID, 42); err != nil {
		t.Fatal(err)
	}
	active, err := ListActiveTasks(ctx, pool)
	if err != nil || len(active) != 1 {
		t.Fatalf("ListActiveTasks = %v (%v)", active, err)
	}
	a := active[0]
	if a.Title != "Django" || a.File != "Django.avi" || a.Agent != "GPU" || a.Percent != 42 {
		t.Fatalf("active task = %+v", a)
	}
}

func TestResetEncodingToPending(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := UpsertPendingTask(ctx, pool, "m1", 0, "/src/a.avi", "/src/a.optimized.mp4"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ClaimNextTask(ctx, pool, "GPU"); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPending(ctx, pool); n != 0 {
		t.Fatalf("pending before reset = %d, want 0", n)
	}
	if err := ResetEncodingToPending(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPending(ctx, pool); n != 1 {
		t.Fatalf("pending after reset = %d, want 1", n)
	}
}

func TestPruneTask(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := UpsertPendingTask(ctx, pool, "m1", 0, "/src/a.avi", "/src/a.optimized.mp4"); err != nil {
		t.Fatal(err)
	}
	// Pending row is pruned.
	if err := PruneTask(ctx, pool, "m1", 0); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPending(ctx, pool); n != 0 {
		t.Fatalf("pending after prune = %d, want 0", n)
	}
	// An encoding row is left alone.
	if err := UpsertPendingTask(ctx, pool, "m2", 0, "/src/b.avi", "/src/b.optimized.mp4"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ClaimNextTask(ctx, pool, "GPU"); err != nil {
		t.Fatal(err)
	}
	if err := PruneTask(ctx, pool, "m2", 0); err != nil {
		t.Fatal(err)
	}
	if active, _ := ListActiveTasks(ctx, pool); len(active) != 1 {
		t.Fatalf("encoding row should survive prune, active = %v", active)
	}
}

func TestFinishAndFailTask(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := UpsertPendingTask(ctx, pool, "m1", 0, "/src/a.avi", "/src/a.optimized.mp4"); err != nil {
		t.Fatal(err)
	}
	tk, _, _ := ClaimNextTask(ctx, pool, "GPU")
	if err := FinishTask(ctx, pool, tk.ID); err != nil {
		t.Fatal(err)
	}
	if active, _ := ListActiveTasks(ctx, pool); len(active) != 0 {
		t.Fatalf("finished task should be gone, active = %v", active)
	}

	if err := UpsertPendingTask(ctx, pool, "m2", 0, "/src/b.avi", "/src/b.optimized.mp4"); err != nil {
		t.Fatal(err)
	}
	tk2, _, _ := ClaimNextTask(ctx, pool, "CPU")
	if err := FailTask(ctx, pool, tk2.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	// A failed row is neither pending nor active.
	if n, _ := CountPending(ctx, pool); n != 0 {
		t.Fatalf("failed task counted as pending: %d", n)
	}
	if active, _ := ListActiveTasks(ctx, pool); len(active) != 0 {
		t.Fatalf("failed task counted as active: %v", active)
	}
}
