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

func TestMatchLibrary(t *testing.T) {
	entries := []Entry{
		{Title: "Death's Game", Year: 2023, Rating: 9, Status: StatusCompleted},
		{Title: "Goblin", Year: 2016, Rating: 8, Status: StatusCompleted},
		{Title: "Vincenzo", Year: 2021, Status: StatusPlanToWatch},
	}
	lib := []LibraryItem{
		{ID: "a", Title: "Deaths Game", Year: 2023}, // punctuation differs
		{ID: "b", Title: "Goblin", Year: 1999},      // title matches, year does not
	}
	m := MatchLibrary(entries, lib)

	if m[0].Item == nil || m[0].Item.ID != "a" || !m[0].Exact {
		t.Errorf("Death's Game should match item a exactly: %+v", m[0])
	}
	if m[1].Item == nil || m[1].Item.ID != "b" || m[1].Exact {
		t.Errorf("Goblin should match item b but not exactly (year differs): %+v", m[1])
	}
	if m[2].Item != nil {
		t.Errorf("Vincenzo should be unmatched: %+v", m[2])
	}
}
