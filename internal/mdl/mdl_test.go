package mdl

import (
	"os"
	"testing"

	"filefin/internal/watchlist"
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
	// The anchor's distinct oldtitle and de-slugged href both surface as aliases; the
	// href/oldtitle that merely echo the visible title (Goblin, Vincenzo) yield none.
	if len(got.Aliases) != 2 || got.Aliases[0] != "The Death Game" || got.Aliases[1] != "i will die soon" {
		t.Errorf("entry 0 aliases = %+v", got.Aliases)
	}
	if len(entries[1].Aliases) != 0 {
		t.Errorf("Goblin's slug echoes its title, so it has no aliases: %+v", entries[1].Aliases)
	}

	plan := entries[2]
	if plan.Title != "Vincenzo" || plan.Status != StatusPlanToWatch {
		t.Errorf("entry 2 = %+v", plan)
	}
	if plan.Rating != 0 || plan.Watched() {
		t.Errorf("unrated plan-to-watch should not be rated or watched: %+v", plan)
	}
}

// TestAliasOnlyMatch proves the alias path works end to end for MDL: a library item filed
// only under the de-slugged href title still matches through the shared matcher.
func TestAliasOnlyMatch(t *testing.T) {
	f, err := os.Open("testdata/dramalist.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	entries, err := parseList(f)
	if err != nil {
		t.Fatal(err)
	}
	lib := []watchlist.LibraryItem{{ID: "die", Title: "I Will Die Soon", Year: 2023}} // only the alias
	wl := []watchlist.Entry{entries[0].ToWatchlist()}
	got := watchlist.MatchLibrary(wl, lib)[0]
	if got.Item == nil || got.Item.ID != "die" {
		t.Errorf("Death's Game should match the library item via its slug alias: %+v", got)
	}
}

func TestToWatchlist(t *testing.T) {
	e := Entry{Title: "Goblin", Aliases: []string{"Dokkaebi"}, Year: 2016, Rating: 8, Status: StatusCompleted}
	w := e.ToWatchlist()
	if w.Title != "Goblin" || w.Year != 2016 || w.Rating != 8 || !w.Watched {
		t.Errorf("ToWatchlist = %+v", w)
	}
	if len(w.Aliases) != 1 || w.Aliases[0] != "Dokkaebi" {
		t.Errorf("ToWatchlist should carry aliases through: %+v", w.Aliases)
	}
	if (Entry{Status: StatusDropped}).ToWatchlist().Watched {
		t.Error("a dropped entry must not be watched")
	}
}
