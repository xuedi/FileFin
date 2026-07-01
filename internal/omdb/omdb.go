// Package omdb is a small client for the OMDb API (https://www.omdbapi.com), used to
// enrich imported media with metadata and a poster. Enrichment is best-effort: with
// no API key configured the importer simply skips it.
package omdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"filefin/internal/httpsafe"
)

// Movie is the subset of OMDb fields used for enrichment. Absent values come back as
// the string "N/A".
type Movie struct {
	Title        string   `json:"Title"`
	Year         string   `json:"Year"`
	Rated        string   `json:"Rated"`
	Released     string   `json:"Released"`
	Runtime      string   `json:"Runtime"`
	Genre        string   `json:"Genre"`
	Director     string   `json:"Director"`
	Writer       string   `json:"Writer"`
	Actors       string   `json:"Actors"`
	Plot         string   `json:"Plot"`
	Language     string   `json:"Language"`
	Country      string   `json:"Country"`
	Awards       string   `json:"Awards"`
	Poster       string   `json:"Poster"`
	Ratings      []Rating `json:"Ratings"`
	Metascore    string   `json:"Metascore"`
	ImdbRating   string   `json:"imdbRating"`
	ImdbVotes    string   `json:"imdbVotes"`
	ImdbID       string   `json:"imdbID"`
	Type         string   `json:"Type"`
	TotalSeasons string   `json:"totalSeasons"`
	BoxOffice    string   `json:"BoxOffice"`
	Response     string   `json:"Response"`
	Error        string   `json:"Error"`
}

// Rating is one source's rating in OMDb's Ratings array.
type Rating struct {
	Source string `json:"Source"`
	Value  string `json:"Value"`
}

// RatingBySource returns the value for a named source, or "" if absent. The match is
// case-insensitive and tolerant of OMDb's "N/A".
func (m *Movie) RatingBySource(source string) string {
	for _, r := range m.Ratings {
		if strings.EqualFold(strings.TrimSpace(r.Source), source) {
			v := strings.TrimSpace(r.Value)
			if v == "N/A" {
				return ""
			}
			return v
		}
	}
	return ""
}

// SearchResult is one row of an OMDb title search (the s= endpoint): the lightweight
// fields used to offer candidates before a full lookup by imdb id.
type SearchResult struct {
	Title  string `json:"Title"`
	Year   string `json:"Year"`
	ImdbID string `json:"imdbID"`
	Type   string `json:"Type"`
	Poster string `json:"Poster"`
}

// Client talks to OMDb with an API key. baseURL/imgURL are the data and poster hosts,
// overridable in tests.
type Client struct {
	key     string
	http    *http.Client
	baseURL string
	imgURL  string
}

// New returns a Client for the given API key.
func New(key string) *Client {
	return &Client{
		key:     key,
		http:    &http.Client{Timeout: 15 * time.Second, CheckRedirect: httpsafe.NoInternalRedirect},
		baseURL: "https://www.omdbapi.com",
		imgURL:  "https://img.omdbapi.com",
	}
}

// get performs an OMDb data GET with the configured key and decodes the JSON body into v.
func (c *Client) get(ctx context.Context, q url.Values, v any) error {
	q.Set("apikey", c.key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	if err := json.NewDecoder(httpsafe.LimitBody(resp.Body)).Decode(v); err != nil {
		return err
	}
	return nil
}

// movie runs a single-title query (t= or i=) and returns the decoded record, mapping OMDb's
// "not found" envelope to an error.
func (c *Client) movie(ctx context.Context, q url.Values, what string) (*Movie, error) {
	q.Set("plot", "full")
	var m Movie
	if err := c.get(ctx, q, &m); err != nil {
		return nil, fmt.Errorf("omdb %s: %w", what, err)
	}
	if m.Response != "True" {
		if m.Error != "" {
			return nil, fmt.Errorf("omdb: %s", m.Error)
		}
		return nil, fmt.Errorf("omdb: not found")
	}
	return &m, nil
}

// Lookup finds a title by name and (optional) year.
func (c *Client) Lookup(ctx context.Context, title string, year int) (*Movie, error) {
	q := url.Values{}
	q.Set("t", title)
	if year > 0 {
		q.Set("y", strconv.Itoa(year))
	}
	return c.movie(ctx, q, "lookup "+strconv.Quote(title))
}

// LookupByID finds a title by its imdb id, the authoritative fetch behind the admin manual
// match once a candidate is picked.
func (c *Client) LookupByID(ctx context.Context, imdbID string) (*Movie, error) {
	q := url.Values{}
	q.Set("i", imdbID)
	return c.movie(ctx, q, "lookup id "+strconv.Quote(imdbID))
}

// Search finds candidate titles by name (the s= endpoint), optionally narrowed by year and
// kind ("movie"/"series"). A "not found" or "too many results" response yields an empty
// slice rather than an error, so the caller can present "no candidates" cleanly; only a
// transport or decode failure errors.
func (c *Client) Search(ctx context.Context, title string, year int, kind string) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("s", title)
	if year > 0 {
		q.Set("y", strconv.Itoa(year))
	}
	if kind != "" {
		q.Set("type", kind)
	}
	var body struct {
		Search   []SearchResult `json:"Search"`
		Response string         `json:"Response"`
		Error    string         `json:"Error"`
	}
	if err := c.get(ctx, q, &body); err != nil {
		return nil, fmt.Errorf("omdb search %s: %w", strconv.Quote(title), err)
	}
	if body.Response != "True" {
		return []SearchResult{}, nil // no candidates (not found / too many results)
	}
	return body.Search, nil
}

// Poster downloads the poster image for an imdb id at the requested height. It
// returns the image bytes and the content type.
func (c *Client) Poster(ctx context.Context, imdbID string, height int) ([]byte, string, error) {
	q := url.Values{}
	q.Set("i", imdbID)
	if height > 0 {
		q.Set("h", strconv.Itoa(height))
	}
	q.Set("apikey", c.key)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.imgURL+"/?"+q.Encode(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("omdb poster %s: %w", imdbID, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("omdb poster %s: %w", imdbID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("omdb poster: http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(httpsafe.LimitBody(resp.Body))
	if err != nil {
		return nil, "", fmt.Errorf("omdb poster read %s: %w", imdbID, err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// PosterExt maps a poster content type to a file extension, defaulting to .jpg.
func PosterExt(contentType string) string {
	switch {
	case strings.Contains(contentType, "png"):
		return ".png"
	case strings.Contains(contentType, "gif"):
		return ".gif"
	default:
		return ".jpg"
	}
}
