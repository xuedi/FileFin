package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Details is a movie or series record as the metadata merge reads it: the descriptive fields,
// the ids that tie it to other databases, and the alternative titles a title comparison needs.
type Details struct {
	Kind          string
	ID            int
	ImdbID        string
	Title         string
	OriginalTitle string
	AltTitles     []string
	Year          int
	Release       string // YYYY-MM-DD: a movie's release date, a series' first air date
	Runtime       int    // minutes; per episode for a series, 0 when TMDb has none
	Overview      string
	Tagline       string
	Genres        []string
	Languages     []string // English names ("Korean"), the way OMDb writes them
	Countries     []Country
	Directors     []string
	Writers       []string // a movie's writing credits, a series' creators
	Cast          []string // top billed names
	ContentRating string   // the US certification, "" when TMDb has none
	VoteAverage   float64
	VoteCount     int
	PosterPath    string
}

// Country is one production country.
type Country struct {
	Code string
	Name string
}

// SearchResult is one hit of a title search.
type SearchResult struct {
	Kind          string
	ID            int
	Title         string
	OriginalTitle string
	Year          int
	PosterPath    string
	Overview      string
}

// detailsCast is how many billed names a details record keeps.
const detailsCast = 6

type namedItem struct {
	Name string `json:"name"`
}

type details struct {
	ID              int         `json:"id"`
	ImdbID          string      `json:"imdb_id"`
	Title           string      `json:"title"`
	Name            string      `json:"name"`
	OriginalTitle   string      `json:"original_title"`
	OriginalName    string      `json:"original_name"`
	ReleaseDate     string      `json:"release_date"`
	FirstAirDate    string      `json:"first_air_date"`
	Runtime         int         `json:"runtime"`
	EpisodeRunTime  []int       `json:"episode_run_time"`
	Overview        string      `json:"overview"`
	Tagline         string      `json:"tagline"`
	Genres          []namedItem `json:"genres"`
	CreatedBy       []namedItem `json:"created_by"`
	VoteAverage     float64     `json:"vote_average"`
	VoteCount       int         `json:"vote_count"`
	PosterPath      string      `json:"poster_path"`
	SpokenLanguages []struct {
		EnglishName string `json:"english_name"`
	} `json:"spoken_languages"`
	ProductionCountries []struct {
		Code string `json:"iso_3166_1"`
		Name string `json:"name"`
	} `json:"production_countries"`
	ExternalIDs struct {
		ImdbID string `json:"imdb_id"`
	} `json:"external_ids"`
	AlternativeTitles struct {
		Titles  []struct{ Title string } `json:"titles"`
		Results []struct{ Title string } `json:"results"`
	} `json:"alternative_titles"`
	ReleaseDates struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Dates   []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
	ContentRatings struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
	Credits struct {
		Cast []struct {
			Name  string `json:"name"`
			Order int    `json:"order"`
		} `json:"cast"`
		Crew []struct {
			Name       string `json:"name"`
			Job        string `json:"job"`
			Department string `json:"department"`
		} `json:"crew"`
	} `json:"credits"`
	AggregateCredits struct {
		Cast []struct {
			Name  string `json:"name"`
			Order int    `json:"order"`
		} `json:"cast"`
	} `json:"aggregate_credits"`
}

// Details fetches one movie or series with everything the merge compares, in one request.
func (c *Client) Details(ctx context.Context, kind string, id int) (Details, error) {
	var d details
	appended := "external_ids,alternative_titles,release_dates,credits"
	if kind == KindTV {
		appended = "external_ids,alternative_titles,content_ratings,aggregate_credits"
	}
	q := url.Values{"append_to_response": {appended}}
	if err := c.get(ctx, "/"+kind+"/"+strconv.Itoa(id), q, &d); err != nil {
		return Details{}, fmt.Errorf("tmdb details %s/%d: %w", kind, id, err)
	}
	out := Details{
		Kind: kind, ID: d.ID, Overview: strings.TrimSpace(d.Overview), Tagline: strings.TrimSpace(d.Tagline),
		VoteAverage: d.VoteAverage, VoteCount: d.VoteCount, PosterPath: d.PosterPath,
		ImdbID: firstNonEmpty(d.ExternalIDs.ImdbID, d.ImdbID),
	}
	if kind == KindTV {
		out.Title, out.OriginalTitle, out.Release = d.Name, d.OriginalName, d.FirstAirDate
		if len(d.EpisodeRunTime) > 0 {
			out.Runtime = d.EpisodeRunTime[0]
		}
		out.Writers = names(d.CreatedBy)
		for _, r := range d.ContentRatings.Results {
			if r.Country == "US" {
				out.ContentRating = strings.TrimSpace(r.Rating)
			}
		}
		for _, p := range d.AggregateCredits.Cast {
			if len(out.Cast) < detailsCast && p.Name != "" {
				out.Cast = append(out.Cast, p.Name)
			}
		}
	} else {
		out.Title, out.OriginalTitle, out.Release, out.Runtime = d.Title, d.OriginalTitle, d.ReleaseDate, d.Runtime
		for _, r := range d.ReleaseDates.Results {
			if r.Country != "US" {
				continue
			}
			for _, rd := range r.Dates {
				if cert := strings.TrimSpace(rd.Certification); cert != "" {
					out.ContentRating = cert
					break
				}
			}
		}
		for _, p := range d.Credits.Crew {
			switch {
			case p.Job == "Director":
				out.Directors = appendUnique(out.Directors, p.Name)
			case p.Department == "Writing":
				out.Writers = appendUnique(out.Writers, p.Name)
			}
		}
		for _, p := range d.Credits.Cast {
			if len(out.Cast) < detailsCast && p.Name != "" {
				out.Cast = append(out.Cast, p.Name)
			}
		}
	}
	out.Year = yearOf(out.Release)
	out.Genres = names(d.Genres)
	for _, l := range d.SpokenLanguages {
		out.Languages = appendUnique(out.Languages, l.EnglishName)
	}
	for _, pc := range d.ProductionCountries {
		out.Countries = append(out.Countries, Country{Code: pc.Code, Name: pc.Name})
	}
	for _, t := range append(d.AlternativeTitles.Titles, d.AlternativeTitles.Results...) {
		out.AltTitles = appendUnique(out.AltTitles, t.Title)
	}
	return out, nil
}

// Search looks a title up by name, narrowed by year when one is given. kind is KindMovie or
// KindTV, or "" to search both.
func (c *Client) Search(ctx context.Context, query string, year int, kind string) ([]SearchResult, error) {
	kinds := []string{KindMovie, KindTV}
	if kind != "" {
		kinds = []string{kind}
	}
	out := []SearchResult{}
	for _, k := range kinds {
		q := url.Values{"query": {query}}
		if year > 0 {
			if k == KindTV {
				q.Set("first_air_date_year", strconv.Itoa(year))
			} else {
				q.Set("primary_release_year", strconv.Itoa(year))
			}
		}
		var body struct {
			Results []struct {
				ID            int    `json:"id"`
				Title         string `json:"title"`
				Name          string `json:"name"`
				OriginalTitle string `json:"original_title"`
				OriginalName  string `json:"original_name"`
				ReleaseDate   string `json:"release_date"`
				FirstAirDate  string `json:"first_air_date"`
				PosterPath    string `json:"poster_path"`
				Overview      string `json:"overview"`
			} `json:"results"`
		}
		if err := c.get(ctx, "/search/"+k, q, &body); err != nil {
			return nil, fmt.Errorf("tmdb search %q: %w", query, err)
		}
		for _, r := range body.Results {
			res := SearchResult{Kind: k, ID: r.ID, PosterPath: r.PosterPath, Overview: r.Overview}
			if k == KindTV {
				res.Title, res.OriginalTitle, res.Year = r.Name, r.OriginalName, yearOf(r.FirstAirDate)
			} else {
				res.Title, res.OriginalTitle, res.Year = r.Title, r.OriginalTitle, yearOf(r.ReleaseDate)
			}
			out = append(out, res)
		}
	}
	return out, nil
}

// Poster downloads a poster from TMDb's image host at a size such as "w154" or "w780".
func (c *Client) Poster(ctx context.Context, size, posterPath string) ([]byte, error) {
	return c.image(ctx, size, posterPath)
}

func names(items []namedItem) []string {
	var out []string
	for _, it := range items {
		out = appendUnique(out, it.Name)
	}
	return out
}

func appendUnique(list []string, v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return list
	}
	for _, have := range list {
		if have == v {
			return list
		}
	}
	return append(list, v)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// yearOf reads the year of a YYYY-MM-DD date, 0 when there is none.
func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}
