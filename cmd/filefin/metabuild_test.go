package main

import (
	"testing"

	"filefin/internal/importer"
	"filefin/internal/model"
	"filefin/internal/omdb"
)

func kvMap(kvs []model.KV) map[string]string {
	m := map[string]string{}
	for _, kv := range kvs {
		m[kv.Key] = kv.Value
	}
	return m
}

func TestMetaFromOMDbFull(t *testing.T) {
	mc := metaFromOMDb(&omdb.Movie{
		Title: "Blade Runner", Released: "25 Jun 1982", Runtime: "117 min",
		Genre: "Action, Sci-Fi", Director: "Ridley Scott", Writer: "Hampton Fancher",
		Actors: "Harrison Ford, Rutger Hauer", Plot: "A blade runner.",
		Language: "English, German", Country: "United States, United Kingdom",
		Awards: "Nominated for 2 Oscars.", Rated: "R", BoxOffice: "$32,914,489",
		ImdbRating: "8.1", ImdbVotes: "835,123", ImdbID: "tt0083658", Type: "movie",
		Ratings: []omdb.Rating{
			{Source: "Rotten Tomatoes", Value: "89%"},
			{Source: "Metacritic", Value: "84/100"},
		},
	}, "Blade Runner", 1982)

	md := kvMap(mc.Metadata)
	if md["release"] != "25 Jun 1982" || md["runtime"] != "117" || md["language"] != "English, German" {
		t.Fatalf("metadata: %+v", mc.Metadata)
	}
	if md["origin"] != "United States, United Kingdom" || md["contentRating"] != "R" {
		t.Fatalf("metadata origin/rated: %+v", mc.Metadata)
	}
	if md["awards"] != "Nominated for 2 Oscars." || md["boxOffice"] != "$32,914,489" || md["imdbID"] != "tt0083658" {
		t.Fatalf("metadata awards/box/id: %+v", mc.Metadata)
	}
	if _, ok := md["seasons"]; ok {
		t.Fatalf("a movie should not get seasons: %+v", mc.Metadata)
	}

	rt := kvMap(mc.Ratings)
	if rt["imdb"] != "8.1 (835,123 votes)" || rt["rottenTomatoes"] != "89%" || rt["metacritic"] != "84/100" {
		t.Fatalf("ratings: %+v", mc.Ratings)
	}
	if mc.Description != "A blade runner." || len(mc.Actors) != 2 || len(mc.Tags) != 2 || mc.Tags[0] != "action" {
		t.Fatalf("desc/actors/tags: %+v", mc)
	}
}

func TestMetaFromOMDbSparse(t *testing.T) {
	// Series with most fields "N/A": only the present keys appear, plus seasons.
	mc := metaFromOMDb(&omdb.Movie{
		Title: "A Show", Released: "N/A", Runtime: "N/A", Genre: "Drama",
		Director: "N/A", Writer: "N/A", Plot: "N/A", Language: "N/A", Country: "N/A",
		Awards: "N/A", Rated: "N/A", BoxOffice: "N/A", ImdbRating: "N/A", ImdbVotes: "N/A",
		ImdbID: "tt1234567", Type: "series", TotalSeasons: "5",
	}, "A Show", 1990)

	md := kvMap(mc.Metadata)
	if md["release"] != "1990" { // falls back to the year when OMDb omits a date
		t.Fatalf("release fallback: %+v", mc.Metadata)
	}
	if md["seasons"] != "5" {
		t.Fatalf("series seasons: %+v", mc.Metadata)
	}
	if _, ok := md["boxOffice"]; ok {
		t.Fatalf("N/A boxOffice should be dropped: %+v", mc.Metadata)
	}
	if len(mc.Ratings) != 0 {
		t.Fatalf("no ratings expected: %+v", mc.Ratings)
	}
}

func TestMetaFromOMDbMetascoreFallback(t *testing.T) {
	mc := metaFromOMDb(&omdb.Movie{
		Title: "X", ImdbRating: "7.0", Metascore: "60", Type: "movie",
	}, "X", 2000)
	rt := kvMap(mc.Ratings)
	if rt["imdb"] != "7.0" { // no votes -> bare rating
		t.Fatalf("imdb bare: %+v", mc.Ratings)
	}
	if rt["metacritic"] != "60/100" { // from Metascore when Ratings[] lacks it
		t.Fatalf("metacritic fallback: %+v", mc.Ratings)
	}
}

func TestMergeMeta(t *testing.T) {
	primary := importer.MetaContent{
		Description: "from omdb",
		Metadata:    []model.KV{{Key: "release", Value: "1982-06-25"}, {Key: "runtime", Value: "117"}},
		Ratings:     []model.KV{{Key: "imdb", Value: "8.1"}},
		Actors:      []string{"Harrison Ford"},
	}
	fallback := importer.MetaContent{
		Description: "from plex",
		Plot:        "plex plot",
		Metadata:    []model.KV{{Key: "release", Value: "1982"}, {Key: "contentRating", Value: "R"}},
		Ratings:     []model.KV{{Key: "imdb", Value: "9.9"}, {Key: "rottenTomatoes", Value: "89%"}},
		Actors:      []string{"Someone Else"},
		Tags:        []string{"sci-fi"},
	}
	out := mergeMeta(primary, fallback)

	if out.Description != "from omdb" { // primary wins
		t.Fatalf("description: %q", out.Description)
	}
	if out.Plot != "plex plot" { // empty in primary -> filled from fallback
		t.Fatalf("plot: %q", out.Plot)
	}
	if len(out.Actors) != 1 || out.Actors[0] != "Harrison Ford" { // primary non-empty wins whole
		t.Fatalf("actors: %+v", out.Actors)
	}
	if len(out.Tags) != 1 || out.Tags[0] != "sci-fi" { // empty in primary -> taken from fallback
		t.Fatalf("tags: %+v", out.Tags)
	}
	md := kvMap(out.Metadata)
	if md["release"] != "1982-06-25" { // primary value wins for shared key
		t.Fatalf("merged release: %+v", out.Metadata)
	}
	if md["contentRating"] != "R" { // missing key appended from fallback
		t.Fatalf("merged contentRating: %+v", out.Metadata)
	}
	rt := kvMap(out.Ratings)
	if rt["imdb"] != "8.1" || rt["rottenTomatoes"] != "89%" {
		t.Fatalf("merged ratings: %+v", out.Ratings)
	}
}
