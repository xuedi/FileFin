package db

import (
	"context"
	"fmt"
	"testing"
)

// mediaID makes a distinct media id per index, for queues keyed on media_id alone.
func mediaID(i int) string { return fmt.Sprintf("m%d", i) }

func TestUpsertPendingEnrichDedupe(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	if err := UpsertPendingEnrich(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	// A second upsert for the same media must not create a duplicate.
	if err := UpsertPendingEnrich(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	if n, err := CountPendingEnrich(ctx, pool); err != nil || n != 1 {
		t.Fatalf("CountPendingEnrich = %d (%v), want 1", n, err)
	}
}

func TestClaimNextEnrichAtomicity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	if err := UpsertPendingEnrich(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}

	t1, ok, err := ClaimNextEnrich(ctx, pool, "omdb")
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if t1.MediaID != "m1" || t1.Status != EnrichStatusEnriching || t1.Agent != "omdb" {
		t.Fatalf("first claim row = %+v", t1)
	}
	// The only pending row is now enriching; a second claim returns nothing.
	if _, ok, err := ClaimNextEnrich(ctx, pool, "omdb"); err != nil || ok {
		t.Fatalf("second claim: ok=%v err=%v, want no row", ok, err)
	}
	if n, _ := CountPendingEnrich(ctx, pool); n != 0 {
		t.Fatalf("pending after claim = %d, want 0", n)
	}
}

func TestClaimNextEnrichDistinctRows(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	for i := 0; i < 3; i++ {
		if err := UpsertPendingEnrich(ctx, pool, mediaID(i)); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int64]bool{}
	for i := 0; i < 3; i++ {
		tk, ok, err := ClaimNextEnrich(ctx, pool, "omdb")
		if err != nil || !ok {
			t.Fatalf("claim %d: ok=%v err=%v", i, ok, err)
		}
		if seen[tk.ID] {
			t.Fatalf("claim returned duplicate id %d", tk.ID)
		}
		seen[tk.ID] = true
	}
}

func TestListActiveEnrich(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := InsertMedia(ctx, pool, Media{ID: "m1", Title: "Django"}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPendingEnrich(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ClaimNextEnrich(ctx, pool, "omdb"); err != nil {
		t.Fatal(err)
	}
	active, err := ListActiveEnrich(ctx, pool)
	if err != nil || len(active) != 1 {
		t.Fatalf("ListActiveEnrich = %v (%v)", active, err)
	}
	a := active[0]
	if a.Title != "Django" || a.Agent != "omdb" || a.Status != EnrichStatusEnriching {
		t.Fatalf("active enrich = %+v", a)
	}
}

func TestResetEnrichingToPending(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := UpsertPendingEnrich(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ClaimNextEnrich(ctx, pool, "omdb"); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPendingEnrich(ctx, pool); n != 0 {
		t.Fatalf("pending before reset = %d, want 0", n)
	}
	if err := ResetEnrichingToPending(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPendingEnrich(ctx, pool); n != 1 {
		t.Fatalf("pending after reset = %d, want 1", n)
	}
}

func TestPruneEnrich(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := UpsertPendingEnrich(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	// Pending row is pruned.
	if err := PruneEnrich(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPendingEnrich(ctx, pool); n != 0 {
		t.Fatalf("pending after prune = %d, want 0", n)
	}
	// An enriching row is left alone.
	if err := UpsertPendingEnrich(ctx, pool, "m2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ClaimNextEnrich(ctx, pool, "omdb"); err != nil {
		t.Fatal(err)
	}
	if err := PruneEnrich(ctx, pool, "m2"); err != nil {
		t.Fatal(err)
	}
	if active, _ := ListActiveEnrich(ctx, pool); len(active) != 1 {
		t.Fatalf("enriching row should survive prune, active = %v", active)
	}
}

func TestPruneEnrichedTasks(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	// m1 is already enriched, m2 is not.
	if err := InsertMedia(ctx, pool, Media{ID: "m1", Title: "Done", Enriched: true}); err != nil {
		t.Fatal(err)
	}
	if err := InsertMedia(ctx, pool, Media{ID: "m2", Title: "Todo"}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPendingEnrich(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPendingEnrich(ctx, pool, "m2"); err != nil {
		t.Fatal(err)
	}
	if err := PruneEnrichedTasks(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// Only the task for the still-unenriched m2 survives.
	if n, _ := CountPendingEnrich(ctx, pool); n != 1 {
		t.Fatalf("pending after prune-enriched = %d, want 1", n)
	}
}

func TestFinishAndFailEnrich(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := UpsertPendingEnrich(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	tk, _, _ := ClaimNextEnrich(ctx, pool, "omdb")
	if err := FinishEnrich(ctx, pool, tk.ID); err != nil {
		t.Fatal(err)
	}
	if active, _ := ListActiveEnrich(ctx, pool); len(active) != 0 {
		t.Fatalf("finished task should be gone, active = %v", active)
	}

	if err := UpsertPendingEnrich(ctx, pool, "m2"); err != nil {
		t.Fatal(err)
	}
	tk2, _, _ := ClaimNextEnrich(ctx, pool, "omdb")
	if err := FailEnrich(ctx, pool, tk2.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	// A failed row is neither pending nor active.
	if n, _ := CountPendingEnrich(ctx, pool); n != 0 {
		t.Fatalf("failed task counted as pending: %d", n)
	}
	if active, _ := ListActiveEnrich(ctx, pool); len(active) != 0 {
		t.Fatalf("failed task counted as active: %v", active)
	}
}

func TestClearEnrichTasksAll(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := UpsertPendingEnrich(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPendingEnrich(ctx, pool, "m2"); err != nil {
		t.Fatal(err)
	}
	if err := ClearEnrichTasksAll(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPendingEnrich(ctx, pool); n != 0 {
		t.Fatalf("pending after clear-all = %d, want 0", n)
	}
}
