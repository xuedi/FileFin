package sources

import (
	"reflect"
	"testing"

	"filefin/internal/importer"
)

// item builds a meta.json with an OMDb and a TMDb snapshot.
func item(top importer.Meta, o, t *importer.Snapshot) importer.Meta {
	top.Sources = map[string]*importer.Snapshot{}
	if o != nil {
		top.Sources[OMDb] = o
	}
	if t != nil {
		top.Sources[TMDb] = t
	}
	return top
}

func row(t *testing.T, rows []Row, field string) Row {
	t.Helper()
	for _, r := range rows {
		if r.Field == field {
			return r
		}
	}
	t.Fatalf("no row %q", field)
	return Row{}
}

func matrixOMDb() *importer.Snapshot {
	return &importer.Snapshot{
		ID: "tt0133093", Kind: KindMovie, ImdbID: "tt0133093", Title: "The Matrix", Year: 1999,
		Description: "A hacker learns the truth.",
		Metadata: map[string]string{
			"release": "31 Mar 1999", "runtime": "136", "language": "English", "origin": "United States",
			"directedBy": "Lana Wachowski, Lilly Wachowski", "writtenBy": "Lilly Wachowski, Lana Wachowski",
			"contentRating": "R", "imdbID": "tt0133093", "awards": "Won 4 Oscars",
		},
		Ratings: map[string]string{"imdb": "8.7 (2,000,000 votes)"},
		Actors:  []string{"Keanu Reeves", "Laurence Fishburne"},
		Genres:  []string{"action", "sci-fi"},
	}
}

func matrixTMDb() *importer.Snapshot {
	return &importer.Snapshot{
		ID: "603", Kind: KindMovie, ImdbID: "tt0133093", Title: "The Matrix", Year: 1999,
		AltTitles:   []string{"Matrix"},
		Description: "Set in the 22nd century...",
		Metadata: map[string]string{
			"release": "1999-03-31", "runtime": "136", "language": "English", "origin": "United States",
			"directedBy": "Lana Wachowski, Lilly Wachowski", "writtenBy": "Lana Wachowski",
			"contentRating": "R", "imdbID": "tt0133093", "tmdbID": "movie/603", "tagline": "Believe the unbelievable.",
		},
		Ratings: map[string]string{"tmdb": "8.3 (28,730 votes)"},
		Actors:  []string{"Keanu Reeves"},
		Genres:  []string{"action", "sci-fi", "thriller"},
	}
}

// enriched is an item as the OMDb enricher leaves it: the top level is OMDb's record.
func enriched(o *importer.Snapshot) importer.Meta {
	return importer.Meta{
		Title: o.Title, Year: o.Year, Description: o.Description,
		Metadata: copyMap(o.Metadata), Ratings: copyMap(o.Ratings),
		Actors: append([]string(nil), o.Actors...), Genres: append([]string(nil), o.Genres...),
	}
}

func TestAgreeingSourcesFillAndCombine(t *testing.T) {
	o, tm := matrixOMDb(), matrixTMDb()
	got, rows := Resolve(item(enriched(o), o, tm), nil)

	if len(Conflicts(rows)) != 0 {
		t.Fatalf("the two records of one film must not conflict: %+v", Conflicts(rows))
	}
	if got.Metadata["tagline"] != "Believe the unbelievable." || got.Metadata["tmdbID"] != "movie/603" {
		t.Errorf("a field only TMDb has must be filled: %v", got.Metadata)
	}
	if got.Metadata["release"] != "31 Mar 1999" {
		t.Errorf("an agreeing field keeps the value it has, got %q", got.Metadata["release"])
	}
	if !reflect.DeepEqual(got.Genres, []string{"action", "sci-fi", "thriller"}) {
		t.Errorf("genres are the union, got %v", got.Genres)
	}
	if got.Description != o.Description {
		t.Errorf("prose keeps the incumbent without a rule, got %q", got.Description)
	}
	if got.Ratings["tmdb"] == "" || got.Ratings["imdb"] == "" {
		t.Errorf("ratings sit side by side, got %v", got.Ratings)
	}
	if r := row(t, rows, "writtenBy"); r.Status != StatusAgree {
		t.Errorf("writing credits that share a name agree, got %+v", r)
	}
	// Idempotent: resolving the result again changes nothing.
	again, _ := Resolve(got, nil)
	if !reflect.DeepEqual(again, got) {
		t.Errorf("a second resolve changed the item:\n%+v\n%+v", got, again)
	}
}

func TestConflictKeepsValueUntilDecided(t *testing.T) {
	o, tm := matrixOMDb(), matrixTMDb()
	tm.Metadata["runtime"] = "170"
	tm.Metadata["directedBy"] = "Someone Else"
	m := item(enriched(o), o, tm)

	got, rows := Resolve(m, nil)
	conflicts := Conflicts(rows)
	if len(conflicts) != 2 {
		t.Fatalf("want runtime and director in conflict, got %+v", conflicts)
	}
	if got.Metadata["runtime"] != "136" {
		t.Errorf("a conflict leaves the value in place, got %q", got.Metadata["runtime"])
	}

	// A rule decides a field conflict library-wide.
	got, rows = Resolve(m, map[string][]string{"runtime": {TMDb, OMDb}})
	if got.Metadata["runtime"] != "170" || row(t, rows, "runtime").Status != StatusRule {
		t.Errorf("rule: runtime %q, row %+v", got.Metadata["runtime"], row(t, rows, "runtime"))
	}
	if len(Conflicts(rows)) != 1 {
		t.Errorf("the director is still undecided, got %+v", Conflicts(rows))
	}

	// A per-item pick beats the rule; "manual" beats both.
	m.Choices = map[string]string{"runtime": OMDb, "directedBy": ChoiceManual}
	got, rows = Resolve(m, map[string][]string{"runtime": {TMDb}})
	if got.Metadata["runtime"] != "136" || row(t, rows, "runtime").Status != StatusChosen {
		t.Errorf("a pick beats the rule, got %q", got.Metadata["runtime"])
	}
	if len(Conflicts(rows)) != 0 {
		t.Errorf("both fields are decided, got %+v", Conflicts(rows))
	}
}

func TestTolerances(t *testing.T) {
	o, tm := matrixOMDb(), matrixTMDb()
	tm.Metadata["runtime"] = "140"        // within 10%
	tm.Metadata["release"] = "1999-06-11" // another country's date, same year
	tm.Metadata["writtenBy"] = "Lana Wachowski (screenplay)"
	_, rows := Resolve(item(enriched(o), o, tm), nil)
	for _, f := range []string{"runtime", "release", "writtenBy"} {
		if r := row(t, rows, f); r.Status == StatusConflict {
			t.Errorf("%s must be within tolerance: %+v", f, r)
		}
	}
}

func TestIdentityConflictIgnoresRules(t *testing.T) {
	o, tm := matrixOMDb(), matrixTMDb()
	tm.Year, tm.Kind = 2003, KindSeries
	rules := map[string][]string{"year": {TMDb}, "kind": {TMDb}}
	got, rows := Resolve(item(enriched(o), o, tm), rules)
	for _, f := range []string{"year", "kind"} {
		if r := row(t, rows, f); r.Status != StatusConflict || r.Class != ClassIdentity {
			t.Errorf("%s: want an identity conflict, got %+v", f, r)
		}
	}
	if got.Year != 1999 {
		t.Errorf("no rule may move an identity field, got %d", got.Year)
	}

	// One year apart is a field conflict, which a rule may settle.
	tm.Year, tm.Kind = 2000, KindMovie
	got, rows = Resolve(item(enriched(o), o, tm), rules)
	if r := row(t, rows, "year"); r.Status != StatusRule || got.Year != 2000 {
		t.Errorf("year one apart under a rule: %+v, year %d", r, got.Year)
	}
}

func TestLocalValuesAreKept(t *testing.T) {
	o, tm := matrixOMDb(), matrixTMDb()
	top := enriched(o)
	top.Metadata["runtime"] = "131"   // an older hand edit, neither source's value
	top.Description = "My own words." // likewise
	tm.Metadata["runtime"] = "150"    // the sources disagree as well
	got, rows := Resolve(item(top, o, tm), map[string][]string{"description": {TMDb}})
	if got.Metadata["runtime"] != "131" || row(t, rows, "runtime").Status != StatusLocal {
		t.Errorf("a local runtime is kept and not reported, got %q %+v", got.Metadata["runtime"], row(t, rows, "runtime"))
	}
	if got.Description != "My own words." {
		t.Errorf("not even a rule overwrites a local value, got %q", got.Description)
	}
}

func TestTitleMatchesAlternativeTitles(t *testing.T) {
	o := &importer.Snapshot{Kind: KindSeries, Title: "Sarangui Bulsichak", Year: 2019}
	tm := &importer.Snapshot{Kind: KindSeries, Title: "Crash Landing on You", Year: 2019,
		AltTitles: []string{"사랑의 불시착", "Sarangui Bulsichak"}}
	top := importer.Meta{Title: "Crash Landing on You", Year: 2019}
	_, rows := Resolve(item(top, o, tm), nil)
	if r := row(t, rows, "title"); r.Status == StatusConflict {
		t.Errorf("a title TMDb lists as an alternative agrees: %+v", r)
	}
	tm.AltTitles = nil
	_, rows = Resolve(item(top, o, tm), nil)
	if r := row(t, rows, "title"); r.Status != StatusConflict || r.Class != ClassField {
		t.Errorf("unrelated titles are a field conflict: %+v", r)
	}
}

func TestOnlyTMDbFillsAnEmptyItem(t *testing.T) {
	tm := matrixTMDb()
	top := importer.Meta{Title: "The Matrix", Year: 1999}
	got, rows := Resolve(item(top, nil, tm), nil)
	if got.Description == "" || got.Metadata["imdbID"] != "tt0133093" || len(got.Actors) != 1 || len(got.Genres) != 3 {
		t.Errorf("a TMDb-only item is filled from TMDb: %+v", got)
	}
	if len(Conflicts(rows)) != 0 {
		t.Errorf("one source cannot conflict with itself: %+v", Conflicts(rows))
	}
}

func TestRowsAreNeverNull(t *testing.T) {
	_, rows := Resolve(item(importer.Meta{}, matrixOMDb(), nil), nil)
	for _, r := range rows {
		if r.Current == nil || r.Values == nil {
			t.Errorf("row %s has a nil list: %+v", r.Field, r)
		}
	}
}

func TestFailedSnapshotIsIgnored(t *testing.T) {
	o := matrixOMDb()
	top := enriched(o)
	failed := &importer.Snapshot{Error: "not on TMDb", Fetched: 1}
	got, rows := Resolve(item(top, o, failed), nil)
	if len(Conflicts(rows)) != 0 || got.Metadata["tmdbID"] != "" {
		t.Errorf("a failed lookup contributes nothing: %+v", got.Metadata)
	}
}

func TestUnionChoiceAndSingleSourcePick(t *testing.T) {
	o, tm := matrixOMDb(), matrixTMDb()
	m := item(enriched(o), o, tm)
	m.Choices = map[string]string{"genres": OMDb}
	got, _ := Resolve(m, nil)
	if !reflect.DeepEqual(got.Genres, []string{"action", "sci-fi"}) {
		t.Errorf("a genre list pinned to OMDb drops TMDb's extra, got %v", got.Genres)
	}
	m.Choices = map[string]string{"genres": ChoiceUnion}
	got, _ = Resolve(m, nil)
	if len(got.Genres) != 3 {
		t.Errorf("the union pick combines them, got %v", got.Genres)
	}
}
