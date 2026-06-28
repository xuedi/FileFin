package mdl

import (
	"os"
	"testing"
)

func TestParseList(t *testing.T) {
	f, err := os.Open("testdata/dramalist.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	entries, err := parseList(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(entries), entries)
	}

	got := entries[0]
	if got.Title != "Death's Game" || got.Year != 2023 || got.Rating != 9 {
		t.Errorf("entry 0 = %+v", got)
	}
	if got.Status != StatusCompleted || !got.Watched() {
		t.Errorf("entry 0 status = %q, watched = %v", got.Status, got.Watched())
	}

	plan := entries[2]
	if plan.Title != "Vincenzo" || plan.Status != StatusPlanToWatch {
		t.Errorf("entry 2 = %+v", plan)
	}
	if plan.Rating != 0 || plan.Watched() {
		t.Errorf("unrated plan-to-watch should not be rated or watched: %+v", plan)
	}
}

func TestToWatchlist(t *testing.T) {
	e := Entry{Title: "Goblin", Year: 2016, Rating: 8, Status: StatusCompleted}
	w := e.ToWatchlist()
	if w.Title != "Goblin" || w.Year != 2016 || w.Rating != 8 || !w.Watched || len(w.Aliases) != 0 {
		t.Errorf("ToWatchlist = %+v", w)
	}
	if (Entry{Status: StatusDropped}).ToWatchlist().Watched {
		t.Error("a dropped entry must not be watched")
	}
}
