package watchlist

import "testing"

func TestMatchLibrary(t *testing.T) {
	entries := []Entry{
		{Title: "Death's Game", Year: 2023, Rating: 9, Watched: true},
		{Title: "Goblin", Year: 2016, Rating: 8, Watched: true},
		{Title: "Vincenzo", Year: 2021},
	}
	lib := []LibraryItem{
		{ID: "a", Title: "Deaths Game", Year: 2023}, // punctuation differs
		{ID: "b", Title: "Goblin", Year: 1999},      // title matches, year does not
	}
	m := MatchLibrary(entries, lib)

	if m[0].Item == nil || m[0].Item.ID != "a" || !m[0].Exact || m[0].LibraryYear != 2023 {
		t.Errorf("Death's Game should match item a exactly: %+v", m[0])
	}
	if m[1].Item == nil || m[1].Item.ID != "b" || m[1].Exact || m[1].LibraryYear != 1999 {
		t.Errorf("Goblin should match item b but not exactly (year differs): %+v", m[1])
	}
	if m[2].Item != nil {
		t.Errorf("Vincenzo should be unmatched: %+v", m[2])
	}
}

// TestKingdomYearStrict is the bug this matcher fixes: an anime "Kingdom" must not be
// marked watched from the Korean drama "Kingdom". With both years present the matcher
// picks the exact year; with a far-off year it picks the closest and flags it approximate
// (so the UI leaves it unselected) rather than blindly taking the first candidate.
func TestKingdomYearStrict(t *testing.T) {
	lib := []LibraryItem{
		{ID: "drama", Title: "Kingdom", Year: 2019},
		{ID: "movie", Title: "Kingdom", Year: 2017},
	}

	got := MatchLibrary([]Entry{{Title: "Kingdom", Year: 2019}}, lib)[0]
	if got.Item == nil || got.Item.ID != "drama" || !got.Exact {
		t.Errorf("exact year 2019 should pick the drama exactly: %+v", got)
	}

	got = MatchLibrary([]Entry{{Title: "Kingdom", Year: 2025}}, lib)[0]
	if got.Item == nil || got.Exact || got.Item.ID != "drama" || got.LibraryYear != 2019 {
		t.Errorf("year 2025 should pick the closest (2019) approximately: %+v", got)
	}

	got = MatchLibrary([]Entry{{Title: "Kingdom"}}, lib)[0] // year unknown, two candidates
	if got.Item == nil || got.Exact {
		t.Errorf("ambiguous yearless match should be approximate: %+v", got)
	}
}

// TestAliasMatch covers a romaji primary title that only matches via its English alias.
func TestAliasMatch(t *testing.T) {
	lib := []LibraryItem{{ID: "aot", Title: "Attack on Titan", Year: 2013}}
	entries := []Entry{{Title: "Shingeki no Kyojin", Aliases: []string{"Attack on Titan"}, Year: 2013, Watched: true}}

	got := MatchLibrary(entries, lib)[0]
	if got.Item == nil || got.Item.ID != "aot" || !got.Exact {
		t.Errorf("romaji title should match via the English alias: %+v", got)
	}
}
