package db

import (
	"context"
	"testing"
)

func TestRenameUser(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	if _, err := InsertUser(ctx, pool, User{Username: "xuedi", Admin: true}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertUserState(ctx, pool, "xuedi", "m1", UserStateRow{Watched: true}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertUserState(ctx, pool, "xuedi", "m2", UserStateRow{Favorite: true}); err != nil {
		t.Fatal(err)
	}
	// An unrelated account must be left alone.
	if err := UpsertUserState(ctx, pool, "other", "m1", UserStateRow{Rating: 5}); err != nil {
		t.Fatal(err)
	}

	if err := RenameUser(ctx, pool, "xuedi", "xuedi@beijingcode.org"); err != nil {
		t.Fatal(err)
	}

	users, err := ListUsers(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Username != "xuedi@beijingcode.org" || !users[0].Admin {
		t.Fatalf("users mirror not renamed: %+v", users)
	}

	watched, err := WatchedSet(ctx, pool, "xuedi@beijingcode.org")
	if err != nil {
		t.Fatal(err)
	}
	if !watched["m1"] {
		t.Fatalf("watched state not carried to new user: %+v", watched)
	}
	if old, _ := WatchedSet(ctx, pool, "xuedi"); len(old) != 0 {
		t.Fatalf("old user still has state: %+v", old)
	}
	if other, _ := WatchedSet(ctx, pool, "other"); other["m1"] {
		t.Fatal("unrelated user m1 should not be watched")
	}
}
