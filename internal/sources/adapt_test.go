package sources

import (
	"reflect"
	"testing"

	"filefin/internal/omdb"
	"filefin/internal/tmdb"
)

func TestGenresMapOntoOMDbNames(t *testing.T) {
	got := Genres([]string{"Action & Adventure", "Sci-Fi & Fantasy", "Drama", "TV Movie", "Science Fiction"})
	want := []string{"action", "adventure", "sci-fi", "fantasy", "drama"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Genres = %v, want %v", got, want)
	}
}

func TestFromTMDbSpeaksOMDbsLanguage(t *testing.T) {
	s := FromTMDb(tmdb.Details{
		Kind: tmdb.KindTV, ID: 94796, ImdbID: "tt10850932", Title: "Crash Landing on You",
		OriginalTitle: "사랑의 불시착", Year: 2019, Release: "2019-12-14", Languages: []string{"Korean"},
		Countries: []tmdb.Country{{Code: "KR", Name: "Korea, Republic of"}, {Code: "US", Name: "United States of America"}},
		Writers:   []string{"Park Ji-eun"}, VoteAverage: 8.529, VoteCount: 969, PosterPath: "/p.jpg",
	}, 7)
	if s.Kind != KindSeries || s.ID != "94796" || s.ImdbID != "tt10850932" || s.Fetched != 7 {
		t.Errorf("snapshot ids = %+v", s)
	}
	if s.Metadata["origin"] != "South Korea, United States" || s.Metadata["language"] != "Korean" {
		t.Errorf("countries/languages = %q / %q", s.Metadata["origin"], s.Metadata["language"])
	}
	if s.Metadata["originalTitle"] != "사랑의 불시착" || s.AltTitles[0] != "사랑의 불시착" || s.Metadata["tmdbID"] != "tv/94796" {
		t.Errorf("titles/ids = %v %v", s.Metadata, s.AltTitles)
	}
	if s.Ratings["tmdb"] != "8.5 (969 votes)" {
		t.Errorf("rating = %q", s.Ratings["tmdb"])
	}
}

func TestFromOMDbKeepsOMDbsOwnTitle(t *testing.T) {
	s := FromOMDb(&omdb.Movie{
		Title: "Sarangui Bulsichak", Year: "2019–2020", Type: "series", ImdbID: "tt10850932",
		Plot: "A paragliding mishap...", Genre: "Comedy, Drama", Poster: "N/A", Runtime: "70 min",
	}, 3)
	if s.Title != "Sarangui Bulsichak" || s.Year != 2019 || s.Kind != KindSeries || s.Poster != "" {
		t.Errorf("snapshot = %+v", s)
	}
	if s.Metadata["runtime"] != "70" || !reflect.DeepEqual(s.Genres, []string{"comedy", "drama"}) {
		t.Errorf("fields = %v %v", s.Metadata, s.Genres)
	}
}
