package db

import (
	"fmt"
	"testing"
)

// TestListMediaLimitAndTotal covers what a capped home row needs: it gets at most Limit items
// but learns the full count behind them, so its "+N more" tile counts what is really there.
func TestListMediaLimitAndTotal(t *testing.T) {
	ctx, pool := mediaTestPool(t)

	for i := 0; i < 30; i++ {
		if err := InsertMedia(ctx, pool, Media{
			ID: fmt.Sprintf("m%02d", i), CategoryID: 1, Path: "/x",
			Title: fmt.Sprintf("Film %02d", i), Year: 2000 + i, Added: int64(i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// One item watched, so the state filters have something to separate.
	if err := UpsertUserState(ctx, pool, "admin", "m05", UserStateRow{Watched: true, Updated: 99}); err != nil {
		t.Fatal(err)
	}

	items, total, err := ListMedia(ctx, pool, ListOpts{User: "admin", Sort: SortAdded, Desc: true, Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 8 || total != 30 {
		t.Fatalf("got %d items / total %d, want 8 / 30", len(items), total)
	}
	if items[0].ID != "m29" {
		t.Fatalf("newest added first: got %s", items[0].ID)
	}

	// A cap larger than the result set needs no second count, and the total still holds.
	items, total, err = ListMedia(ctx, pool, ListOpts{User: "admin", Status: StatusWatched, Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || total != 1 || !items[0].Watched {
		t.Fatalf("watched listing = %d items / total %d, want the one watched item", len(items), total)
	}

	// The remaining 29 are unwatched: never started, never finished.
	if _, total, err = ListMedia(ctx, pool, ListOpts{User: "admin", Status: StatusUnwatched}); err != nil {
		t.Fatal(err)
	} else if total != 29 {
		t.Fatalf("unwatched total = %d, want 29", total)
	}

	// Another user shares the library but not the state.
	if _, total, err = ListMedia(ctx, pool, ListOpts{User: "bob", Status: StatusUnwatched}); err != nil {
		t.Fatal(err)
	} else if total != 30 {
		t.Fatalf("bob's unwatched total = %d, want 30", total)
	}
}
