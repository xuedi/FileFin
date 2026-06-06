// Package omdb is a small client for the OMDb API (https://www.omdbapi.com),
// used to enrich imported media with metadata and a poster.
package omdb

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Movie is the subset of OMDb fields used for enrichment. Absent values come
// back as the string "N/A".
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

// Rating is one source's rating in OMDb's Ratings array (e.g. "Rotten Tomatoes",
// "Metacritic", "Internet Movie Database").
type Rating struct {
	Source string `json:"Source"`
	Value  string `json:"Value"`
}

// RatingBySource returns the value for a named source in the Ratings array, or ""
// if absent. The match is case-insensitive and tolerant of OMDb's "N/A".
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

// Client talks to OMDb with an API key.
type Client struct {
	key  string
	http *http.Client
}

// New returns a Client for the given API key.
func New(key string) *Client {
	return &Client{key: key, http: &http.Client{Timeout: 15 * time.Second}}
}

// Lookup finds a title by name and (optional) year.
func (c *Client) Lookup(title string, year int) (*Movie, error) {
	q := url.Values{}
	q.Set("t", title)
	if year > 0 {
		q.Set("y", strconv.Itoa(year))
	}
	q.Set("plot", "full")
	q.Set("apikey", c.key)

	resp, err := c.http.Get("https://www.omdbapi.com/?" + q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("omdb: http %d", resp.StatusCode)
	}
	var m Movie
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	if m.Response != "True" {
		if m.Error != "" {
			return nil, fmt.Errorf("omdb: %s", m.Error)
		}
		return nil, fmt.Errorf("omdb: not found")
	}
	return &m, nil
}

// Poster downloads the poster image for an imdb id at the requested height.
// It returns the image bytes and the content type.
func (c *Client) Poster(imdbID string, height int) ([]byte, string, error) {
	q := url.Values{}
	q.Set("i", imdbID)
	if height > 0 {
		q.Set("h", strconv.Itoa(height))
	}
	q.Set("apikey", c.key)

	resp, err := c.http.Get("https://img.omdbapi.com/?" + q.Encode())
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("omdb poster: http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}
