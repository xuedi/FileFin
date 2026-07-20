package server

import (
	"strings"
	"testing"

	"filefin/internal/library"
)

// cat builds a category with markers, as the predictor reads them off disk.
func cat(id int64, leaf, alias, kind string, learned map[string]int, declared ...string) library.Category {
	m := library.Markers{Kind: kind, Learned: learned}
	for _, d := range declared {
		m.Keywords = append(m.Keywords, d)
	}
	return library.Category{ID: id, Name: leaf, Leaf: leaf, Alias: alias, Markers: m}
}

// row builds an import row the way a scan would hand it over.
func row(entry, title string, isShow bool) importItem {
	return importItem{Entry: entry, Title: title, IsShow: isShow, paths: []string{entry}}
}

// The corpus this was measured against: a region-pure fansub group points at one category,
// a pan-Asian uploader points nowhere, and the kind verdict picks the half.
func TestPredictionEvidence(t *testing.T) {
	cats := []library.Category{
		cat(1, "Films - China", "Films - China", library.KindFilms, nil),
		cat(2, "Films - Korea", "Films - Korea", library.KindFilms, nil),
		cat(3, "Shows - China", "Shows - China", library.KindShows, map[string]int{"grp:jkct": 3, "grp:unco": 4}),
		cat(4, "Shows - Korea", "Shows - Korea", library.KindShows, map[string]int{"grp:unco": 3, "grp:appletor": 1}),
	}
	tests := []struct {
		name   string
		item   importItem
		want   int64
		reason string
	}{
		{
			name:   "a region-pure group preselects the category it keeps landing in",
			item:   row("Nirvana.in.Fire.S01.1080p-JKCT", "Nirvana in Fire", true),
			want:   3,
			reason: "JKCT was imported into Shows - China 3 times",
		},
		{
			name: "a group split across categories preselects nothing",
			item: row("Some.Drama.S01.1080p-unco@AvistaZ", "Some Drama", true),
			want: 0,
		},
		{
			name: "a group seen once is not evidence yet",
			item: row("Jirisan.S01.1080p-AppleTor", "Jirisan", true),
			want: 0,
		},
		{
			name: "an unknown film preselects nothing on its own",
			item: row("The.Mad.Monk.1993.1080p-NOBODY", "The Mad Monk", false),
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPredictor(cats)
			got := p.predict(tt.item)
			if got.CategoryID != tt.want {
				t.Fatalf("category = %d (%q), want %d", got.CategoryID, got.Reason, tt.want)
			}
			if tt.reason != "" && got.Reason != tt.reason {
				t.Errorf("reason = %q, want %q", got.Reason, tt.reason)
			}
		})
	}
}

// With one category per kind the verdict alone decides, and it needs no other evidence.
func TestPredictionKindPicksTheOnlyHalf(t *testing.T) {
	cats := []library.Category{
		cat(1, "Films", "Films", library.KindFilms, nil),
		cat(2, "Shows", "Shows", library.KindShows, nil),
	}
	p := newPredictor(cats)
	if got := p.predict(row("Some.Show.S01E01", "Some Show", true)); got.CategoryID != 2 {
		t.Fatalf("show went to %d (%q), want the shows category", got.CategoryID, got.Reason)
	}
	if got := p.predict(row("Some.Film.1999", "Some Film", false)); got.CategoryID != 1 {
		t.Fatalf("film went to %d (%q), want the films category", got.CategoryID, got.Reason)
	}
}

// Nothing fires, nothing is guessed: a row with no evidence of its own keeps the plain
// default rather than borrowing an answer from the rows around it.
func TestPredictionWithoutEvidenceGuessesNothing(t *testing.T) {
	cats := []library.Category{
		cat(1, "Films - China", "Films - China", library.KindFilms, nil),
		cat(2, "Shows - China", "Shows - China", library.KindShows, map[string]int{"grp:jkct": 3}),
		cat(3, "Shows - Korea", "Shows - Korea", library.KindShows, nil),
	}
	p := newPredictor(cats)
	if got := p.predict(row("Nirvana.S01-JKCT", "Nirvana", true)); got.CategoryID != 2 {
		t.Fatalf("first row = %d, want 2", got.CategoryID)
	}
	// The row after it shares nothing, so it inherits nothing.
	if got := p.predict(row("Another.Drama.S01-UNKNOWNGRP", "Another Drama", true)); got.CategoryID != 0 {
		t.Fatalf("row without evidence = %d (%q), want no guess", got.CategoryID, got.Reason)
	}
	// A kind with one category left is still certain, evidence or not.
	if got := p.predict(row("Some.Film.1999-UNKNOWNGRP", "Some Film", false)); got.CategoryID != 1 {
		t.Fatalf("film = %d (%q), want the only films category", got.CategoryID, got.Reason)
	}
}

// A keyword the admin declared is evidence in its own right, weighted like a learned marker
// at full purity.
func TestPredictionDeclaredKeyword(t *testing.T) {
	cats := []library.Category{
		cat(1, "Shows - Korea", "Shows - Korea", library.KindShows, nil, "kdrama"),
		cat(2, "Shows - China", "Shows - China", library.KindShows, nil, "cdrama"),
	}
	p := newPredictor(cats)
	got := p.predict(row("Some.Kdrama.Thing.S01", "Some Kdrama Thing", true))
	if got.CategoryID != 1 || !strings.Contains(got.Reason, "kdrama") {
		t.Fatalf("declared keyword = %d (%q), want the Korean category", got.CategoryID, got.Reason)
	}
}

// The seeded vocabulary bridges a cold library, and loses to the first real evidence.
func TestSeedsBridgeAColdLibrary(t *testing.T) {
	cats := []library.Category{
		cat(1, "Shows - China", "Shows - China", library.KindShows, nil),
		cat(2, "Shows - Korea", "Shows - Korea", library.KindShows, nil),
	}
	p := newPredictor(cats)
	got := p.predict(row("Some.Drama.S01.iQIYI.WEB-DL.1080p", "Some Drama", true))
	if got.CategoryID != 1 {
		t.Fatalf("seeded platform = %d (%q), want the Chinese category", got.CategoryID, got.Reason)
	}
	// A library with no category the seed is about is never told about it.
	plain := []library.Category{
		cat(1, "Shows", "Shows", library.KindShows, nil),
		cat(2, "Documentaries", "Documentaries", library.KindShows, nil),
	}
	if got := newPredictor(plain).predict(row("Some.Drama.S01.iQIYI", "Some Drama", true)); got.CategoryID != 0 {
		t.Fatalf("seed fired at an unrelated category: %d (%q)", got.CategoryID, got.Reason)
	}
	// One learned marker pointing elsewhere beats the seed.
	learnedCats := []library.Category{
		cat(1, "Shows - China", "Shows - China", library.KindShows, nil),
		cat(2, "Shows - Korea", "Shows - Korea", library.KindShows, map[string]int{"grp:jkct": 2}),
	}
	got = newPredictor(learnedCats).predict(row("Some.Drama.S01.iQIYI.1080p-JKCT", "Some Drama", true))
	if got.CategoryID != 2 {
		t.Fatalf("learned marker lost to a seed: %d (%q)", got.CategoryID, got.Reason)
	}
}
