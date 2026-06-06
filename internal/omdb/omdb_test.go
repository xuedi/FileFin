package omdb

import (
	"encoding/json"
	"testing"
)

// A full OMDb response captured live, with the Ratings[] array and all the
// expanded fields populated.
const fullResponse = `{
  "Title": "Blade Runner",
  "Year": "1982",
  "Rated": "R",
  "Released": "25 Jun 1982",
  "Runtime": "117 min",
  "Genre": "Action, Drama, Sci-Fi",
  "Director": "Ridley Scott",
  "Writer": "Hampton Fancher, David Webb Peoples, Philip K. Dick",
  "Actors": "Harrison Ford, Rutger Hauer, Sean Young",
  "Plot": "A blade runner must pursue and terminate four replicants.",
  "Language": "English, German, Cantonese",
  "Country": "United States, United Kingdom",
  "Awards": "Nominated for 2 Oscars. 13 wins & 22 nominations total",
  "Poster": "https://example.com/poster.jpg",
  "Ratings": [
    {"Source": "Internet Movie Database", "Value": "8.1/10"},
    {"Source": "Rotten Tomatoes", "Value": "89%"},
    {"Source": "Metacritic", "Value": "84/100"}
  ],
  "Metascore": "84",
  "imdbRating": "8.1",
  "imdbVotes": "835,123",
  "imdbID": "tt0083658",
  "Type": "movie",
  "BoxOffice": "$32,914,489",
  "Response": "True"
}`

// A sparse response: most fields are OMDb's "N/A" sentinel and Ratings is empty.
const sparseResponse = `{
  "Title": "Obscure Film",
  "Year": "1971",
  "Rated": "N/A",
  "Released": "N/A",
  "Runtime": "N/A",
  "Genre": "Drama",
  "Director": "N/A",
  "Writer": "N/A",
  "Actors": "N/A",
  "Plot": "N/A",
  "Language": "N/A",
  "Country": "N/A",
  "Awards": "N/A",
  "Poster": "N/A",
  "Ratings": [],
  "Metascore": "N/A",
  "imdbRating": "N/A",
  "imdbVotes": "N/A",
  "imdbID": "tt9999999",
  "Type": "movie",
  "BoxOffice": "N/A",
  "Response": "True"
}`

func TestUnmarshalFull(t *testing.T) {
	var m Movie
	if err := json.Unmarshal([]byte(fullResponse), &m); err != nil {
		t.Fatal(err)
	}
	if m.Rated != "R" || m.Country != "United States, United Kingdom" {
		t.Fatalf("rated/country: %q / %q", m.Rated, m.Country)
	}
	if m.Language != "English, German, Cantonese" || m.BoxOffice != "$32,914,489" {
		t.Fatalf("language/boxOffice: %q / %q", m.Language, m.BoxOffice)
	}
	if m.ImdbVotes != "835,123" || m.Awards == "" {
		t.Fatalf("votes/awards: %q / %q", m.ImdbVotes, m.Awards)
	}
	if rt := m.RatingBySource("Rotten Tomatoes"); rt != "89%" {
		t.Fatalf("rotten tomatoes: %q", rt)
	}
	if mc := m.RatingBySource("Metacritic"); mc != "84/100" {
		t.Fatalf("metacritic: %q", mc)
	}
	if mc := m.RatingBySource("metacritic"); mc != "84/100" {
		t.Fatalf("metacritic (case-insensitive): %q", mc)
	}
}

func TestUnmarshalSparse(t *testing.T) {
	var m Movie
	if err := json.Unmarshal([]byte(sparseResponse), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Ratings) != 0 {
		t.Fatalf("ratings should be empty: %+v", m.Ratings)
	}
	if rt := m.RatingBySource("Rotten Tomatoes"); rt != "" {
		t.Fatalf("missing source should yield empty, got %q", rt)
	}
	// The "N/A" sentinel is preserved verbatim on the struct; cleaning happens at
	// the meta-building layer, not here.
	if m.Released != "N/A" {
		t.Fatalf("sparse released: %q", m.Released)
	}
}
