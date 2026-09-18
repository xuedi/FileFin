package sources

import (
	"strconv"
	"strings"

	"filefin/internal/importer"
	"filefin/internal/omdb"
	"filefin/internal/tmdb"
)

// Kinds a snapshot records, the same for every source.
const (
	KindMovie  = "movie"
	KindSeries = "series"
)

// FromOMDb turns an OMDb record into its snapshot, in OMDb's own words: its title and year,
// not the folder's.
func FromOMDb(mv *omdb.Movie, fetched int64) *importer.Snapshot {
	year := YearFromOMDb(mv.Year)
	m := importer.MetaFromOMDb(mv, strings.TrimSpace(mv.Title), year)
	imdb := strings.TrimSpace(mv.ImdbID)
	poster := strings.TrimSpace(mv.Poster)
	if strings.EqualFold(poster, "N/A") {
		poster = ""
	}
	kind := strings.ToLower(strings.TrimSpace(mv.Type))
	return &importer.Snapshot{
		ID: imdb, Kind: kind, ImdbID: imdb, Fetched: fetched,
		Title: m.Title, Year: m.Year, Description: m.Description,
		Metadata: m.Metadata, Ratings: m.Ratings, Actors: m.Actors, Genres: m.Genres,
		Poster: poster,
	}
}

// FromTMDb turns a TMDb record into its snapshot, mapping its vocabulary onto OMDb's so the
// two compare: genre names, language and country names, the rating shape.
func FromTMDb(d tmdb.Details, fetched int64) *importer.Snapshot {
	kind := KindMovie
	if d.Kind == tmdb.KindTV {
		kind = KindSeries
	}
	md := map[string]string{}
	add := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			md[k] = v
		}
	}
	add("release", d.Release)
	if d.Runtime > 0 {
		add("runtime", strconv.Itoa(d.Runtime))
	}
	add("language", strings.Join(d.Languages, ", "))
	var countries []string
	for _, c := range d.Countries {
		countries = append(countries, CountryName(c.Code, c.Name))
	}
	add("origin", strings.Join(countries, ", "))
	add("directedBy", strings.Join(d.Directors, ", "))
	add("writtenBy", strings.Join(d.Writers, ", "))
	add("contentRating", d.ContentRating)
	add("tagline", d.Tagline)
	if !strings.EqualFold(strings.TrimSpace(d.OriginalTitle), strings.TrimSpace(d.Title)) {
		add("originalTitle", d.OriginalTitle)
	}
	add("imdbID", d.ImdbID)
	add("tmdbID", d.Kind+"/"+strconv.Itoa(d.ID))
	var ratings map[string]string
	if d.VoteCount > 0 {
		ratings = map[string]string{"tmdb": strconv.FormatFloat(d.VoteAverage, 'f', 1, 64) + " (" + thousands(d.VoteCount) + " votes)"}
	}
	var alt []string
	if d.OriginalTitle != "" {
		alt = append(alt, d.OriginalTitle)
	}
	alt = append(alt, d.AltTitles...)
	return &importer.Snapshot{
		ID: strconv.Itoa(d.ID), Kind: kind, ImdbID: d.ImdbID, Fetched: fetched,
		Title: strings.TrimSpace(d.Title), AltTitles: alt, Year: d.Year, Description: d.Overview,
		Metadata: md, Ratings: ratings, Actors: d.Cast, Genres: Genres(d.Genres), Poster: d.PosterPath,
	}
}

// genreMap maps TMDb's genre names onto OMDb's (lowercased, the way meta.json stores them).
// TMDb's series genres pair two ideas in one name; they split into both. A genre OMDb has no
// counterpart for is dropped rather than invented.
var genreMap = map[string][]string{
	"science fiction":    {"sci-fi"},
	"action & adventure": {"action", "adventure"},
	"sci-fi & fantasy":   {"sci-fi", "fantasy"},
	"war & politics":     {"war"},
	"reality":            {"reality-tv"},
	"talk":               {"talk-show"},
	"kids":               {"family"},
	"tv movie":           nil,
	"soap":               {"drama"},
}

// Genres maps TMDb genre names onto the library's genre vocabulary.
func Genres(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		key := strings.ToLower(strings.TrimSpace(n))
		mapped, ok := genreMap[key]
		if !ok {
			mapped = []string{key}
		}
		for _, g := range mapped {
			if g != "" && !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	return out
}

// countryNames are the ISO codes whose TMDb name differs from the one OMDb writes.
var countryNames = map[string]string{
	"US": "United States",
	"GB": "United Kingdom",
	"KR": "South Korea",
	"KP": "North Korea",
	"RU": "Russia",
	"IR": "Iran",
	"VN": "Vietnam",
	"CZ": "Czech Republic",
	"TW": "Taiwan",
	"HK": "Hong Kong",
}

// CountryName is the name OMDb uses for a country, falling back to TMDb's own.
func CountryName(code, name string) string {
	if n, ok := countryNames[strings.ToUpper(code)]; ok {
		return n
	}
	return strings.TrimSpace(name)
}

// YearFromOMDb reads the leading year of an OMDb Year ("2011", "2011-2013").
func YearFromOMDb(s string) int {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(s[:4])
	return y
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
