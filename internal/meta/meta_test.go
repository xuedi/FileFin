package meta

import "testing"

const sample = `# The Gods Must Be Crazy

## description
A comedy film.

## metadata
 - release: 1980-10-10
 - runtime: 109

## ratings
 - imdb: 7.3 (50,000 votes)
 - rottenTomatoes: 95%
 - metacritic: 73/100

## technical
 - codec:

## actors
 - Nixau (Xi)
 - Marius Weyers (Andrew)

## tags
 - comedy
 - nature

## unknownsection
 - should be ignored
`

func TestParse(t *testing.T) {
	m := Parse(sample)
	if m.Title != "The Gods Must Be Crazy" {
		t.Fatalf("title: %q", m.Title)
	}
	if m.Description != "A comedy film." {
		t.Fatalf("description: %q", m.Description)
	}
	if len(m.Metadata) != 2 || m.Metadata[0].Key != "release" || m.Metadata[0].Value != "1980-10-10" {
		t.Fatalf("metadata: %+v", m.Metadata)
	}
	if len(m.Ratings) != 3 || m.Ratings[0].Key != "imdb" || m.Ratings[0].Value != "7.3 (50,000 votes)" {
		t.Fatalf("ratings: %+v", m.Ratings)
	}
	if m.Ratings[1].Key != "rottenTomatoes" || m.Ratings[1].Value != "95%" {
		t.Fatalf("ratings rt: %+v", m.Ratings)
	}
	if len(m.Technical) != 1 || m.Technical[0].Key != "codec" || m.Technical[0].Value != "" {
		t.Fatalf("technical: %+v", m.Technical)
	}
	if len(m.Actors) != 2 || m.Actors[0] != "Nixau (Xi)" {
		t.Fatalf("actors: %+v", m.Actors)
	}
	if len(m.Tags) != 2 || m.Tags[1] != "nature" {
		t.Fatalf("tags: %+v", m.Tags)
	}
}

func TestParseEmpty(t *testing.T) {
	m := Parse("")
	if m.Title != "" || len(m.Metadata) != 0 || len(m.Actors) != 0 {
		t.Fatalf("expected empty meta, got %+v", m)
	}
}
