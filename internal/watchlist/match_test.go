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
		{ID: "b", Title: "Goblin", Year: 1999},      // title matches, year is far off
	}
	m := MatchLibrary(entries, lib)

	if m[0].Item == nil || m[0].Item.ID != "a" || m[0].Confidence != ConfidenceExact || m[0].LibraryYear != 2023 {
		t.Errorf("Death's Game should match item a exactly: %+v", m[0])
	}
	if m[1].Item == nil || m[1].Item.ID != "b" || m[1].Confidence != ConfidenceApproximate || m[1].LibraryYear != 1999 {
		t.Errorf("Goblin (unique title, 17 years off) should match item b approximately: %+v", m[1])
	}
	if m[2].Item != nil {
		t.Errorf("Vincenzo should be unmatched: %+v", m[2])
	}
}

// TestUniqueYearTiers covers the core of the uniqueness-aware decision: a title unique in the
// library is trusted (confident) when its year is absent or off by at most the tolerance, and
// only slips to approximate when a known year is far off.
func TestUniqueYearTiers(t *testing.T) {
	lib := []LibraryItem{{ID: "v", Title: "Vincenzo", Year: 2021}}

	got := MatchLibrary([]Entry{{Title: "Vincenzo"}}, lib)[0] // no year
	if got.Item == nil || got.Confidence != ConfidenceConfident {
		t.Errorf("unique title with no year should be confident: %+v", got)
	}
	got = MatchLibrary([]Entry{{Title: "Vincenzo", Year: 2022}}, lib)[0] // 1 off
	if got.Item == nil || got.Confidence != ConfidenceConfident {
		t.Errorf("unique title one year off should be confident: %+v", got)
	}
	got = MatchLibrary([]Entry{{Title: "Vincenzo", Year: 2016}}, lib)[0] // 5 off
	if got.Item == nil || got.Confidence != ConfidenceApproximate {
		t.Errorf("unique title five years off should be approximate: %+v", got)
	}
	got = MatchLibrary([]Entry{{Title: "Vincenzo", Year: 2021}}, lib)[0] // exact
	if got.Item == nil || got.Confidence != ConfidenceExact {
		t.Errorf("unique title on the year should be exact: %+v", got)
	}
}

// TestKingdomYearStrict is the bug this matcher fixes: an anime "Kingdom" must not be
// marked watched from the Korean drama "Kingdom". With a colliding title the year stays
// strict - the exact-year candidate is exact, and a far-off year falls to the closest
// candidate flagged approximate (so the UI leaves it unselected).
func TestKingdomYearStrict(t *testing.T) {
	lib := []LibraryItem{
		{ID: "drama", Title: "Kingdom", Year: 2019},
		{ID: "movie", Title: "Kingdom", Year: 2017},
	}

	got := MatchLibrary([]Entry{{Title: "Kingdom", Year: 2019}}, lib)[0]
	if got.Item == nil || got.Item.ID != "drama" || got.Confidence != ConfidenceExact {
		t.Errorf("exact year 2019 should pick the drama exactly: %+v", got)
	}

	got = MatchLibrary([]Entry{{Title: "Kingdom", Year: 2025}}, lib)[0]
	if got.Item == nil || got.Confidence != ConfidenceApproximate || got.Item.ID != "drama" || got.LibraryYear != 2019 {
		t.Errorf("year 2025 should pick the closest (2019) approximately: %+v", got)
	}

	got = MatchLibrary([]Entry{{Title: "Kingdom"}}, lib)[0] // year unknown, two candidates
	if got.Item == nil || got.Confidence == ConfidenceExact {
		t.Errorf("ambiguous yearless match should not be exact: %+v", got)
	}
}

// TestCollisionWithinTolerance covers the collision middle tier: with no exact-year hit, a
// single candidate within tolerance is trusted, but two within tolerance stay approximate.
func TestCollisionWithinTolerance(t *testing.T) {
	lib := []LibraryItem{
		{ID: "a", Title: "Kingdom", Year: 2019},
		{ID: "b", Title: "Kingdom", Year: 2010},
	}
	got := MatchLibrary([]Entry{{Title: "Kingdom", Year: 2018}}, lib)[0] // 1 off a, 8 off b
	if got.Item == nil || got.Item.ID != "a" || got.Confidence != ConfidenceConfident {
		t.Errorf("a lone candidate within tolerance should be confident: %+v", got)
	}

	lib = []LibraryItem{
		{ID: "a", Title: "Kingdom", Year: 2019},
		{ID: "b", Title: "Kingdom", Year: 2018},
	}
	got = MatchLibrary([]Entry{{Title: "Kingdom", Year: 2018}}, lib)[0] // exact hit on b
	if got.Item == nil || got.Item.ID != "b" || got.Confidence != ConfidenceExact {
		t.Errorf("an exact-year candidate still wins even when a sibling is within tolerance: %+v", got)
	}
}

// TestAliasMatch covers a romaji primary title that only matches via its English alias.
func TestAliasMatch(t *testing.T) {
	lib := []LibraryItem{{ID: "aot", Title: "Attack on Titan", Year: 2013}}
	entries := []Entry{{Title: "Shingeki no Kyojin", Aliases: []string{"Attack on Titan"}, Year: 2013, Watched: true}}

	got := MatchLibrary(entries, lib)[0]
	if got.Item == nil || got.Item.ID != "aot" || got.Confidence != ConfidenceExact {
		t.Errorf("romaji title should match via the English alias: %+v", got)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct{ a, b string }{
		{"Tōkyō", "Tokyo"},       // macron folds to the base letter
		{"Café", "Cafe"},         // accents drop
		{"The Goblin", "Goblin"}, // a single leading article is stripped
		{"A Werewolf Boy", "Werewolf Boy"},
		{"An Empress & the Warriors", "Empress and the Warriors"}, // article + "&"
	}
	for _, c := range cases {
		if got, want := normalize(c.a), normalize(c.b); got != want {
			t.Errorf("normalize(%q)=%q, normalize(%q)=%q; want equal", c.a, got, c.b, want)
		}
	}
	// A leading article must not be confused with a word that merely starts with those
	// letters, and an interior article is untouched.
	if normalize("Antique") == normalize("tique") {
		t.Error("the 'an' in 'Antique' must not be stripped as an article")
	}
	if normalize("Beauty and the Beast") != "beautyandthebeast" {
		t.Errorf("an interior article must be kept: %q", normalize("Beauty and the Beast"))
	}
}

// TestFuzzyFallback proves a near-miss romanization the exact-key path misses is surfaced as
// an approximate proposal, while an unrelated title stays unmatched.
func TestFuzzyFallback(t *testing.T) {
	lib := []LibraryItem{{ID: "bof", Title: "Boys Over Flowers", Year: 2009}}

	got := MatchLibrary([]Entry{{Title: "Boys Over Flower", Year: 2009}}, lib)[0] // one letter short
	if got.Item == nil || got.Item.ID != "bof" || got.Confidence != ConfidenceApproximate || got.Reason != "similar title" {
		t.Errorf("a near-miss title should fuzzy-match approximately: %+v", got)
	}

	got = MatchLibrary([]Entry{{Title: "Completely Unrelated"}}, lib)[0]
	if got.Item != nil {
		t.Errorf("an unrelated title must stay unmatched: %+v", got)
	}
}
