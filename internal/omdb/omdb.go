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
	"time"
)

// Movie is the subset of OMDb fields used for enrichment. Absent values come
// back as the string "N/A".
type Movie struct {
	Title      string `json:"Title"`
	Year       string `json:"Year"`
	Released   string `json:"Released"`
	Runtime    string `json:"Runtime"`
	Genre      string `json:"Genre"`
	Director   string `json:"Director"`
	Writer     string `json:"Writer"`
	Actors     string `json:"Actors"`
	Plot       string `json:"Plot"`
	Poster     string `json:"Poster"`
	ImdbID     string `json:"imdbID"`
	ImdbRating string `json:"imdbRating"`
	Response   string `json:"Response"`
	Error      string `json:"Error"`
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
