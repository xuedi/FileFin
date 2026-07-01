// Package mal reads a user's public MyAnimeList anime list through the official API v2.
// Another member's public list is readable with only an X-MAL-CLIENT-ID header (no OAuth),
// so a one-time admin-set client id is all this needs. Unlike the MyDramaList scraper this
// parses JSON, not HTML, so it is not tied to any page markup.
package mal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"filefin/internal/httpsafe"
	"filefin/internal/watchlist"
)

var (
	// ErrNotFound means MyAnimeList has no such user.
	ErrNotFound = errors.New("mal: user not found")
	// ErrEmpty means the list loaded but held no entries: a private or genuinely empty list.
	ErrEmpty = errors.New("mal: list is empty or private")
	// ErrNotConfigured means no client id was provided.
	ErrNotConfigured = errors.New("mal: client id not configured")
	// ErrUnauthorized means MyAnimeList rejected the client id (401/403).
	ErrUnauthorized = errors.New("mal: client id rejected")
)

// fields is the field selector that brings back exactly what the matcher needs: the
// member's status and score plus each title, its alternatives, and its release year.
const fields = "list_status{status,score,updated_at},node{title,alternative_titles,start_season,start_date}"

// Client reads public MyAnimeList lists.
type Client struct {
	http     *http.Client
	baseURL  string
	clientID string
}

// New returns a Client with a sane timeout.
func New(clientID string) *Client {
	return &Client{
		http:     &http.Client{Timeout: 30 * time.Second, CheckRedirect: httpsafe.NoInternalRedirect},
		baseURL:  "https://api.myanimelist.net",
		clientID: clientID,
	}
}

// listResponse is one page of the animelist endpoint.
type listResponse struct {
	Data []struct {
		Node struct {
			Title             string `json:"title"`
			AlternativeTitles struct {
				En       string   `json:"en"`
				Synonyms []string `json:"synonyms"`
			} `json:"alternative_titles"`
			StartSeason struct {
				Year int `json:"year"`
			} `json:"start_season"`
			StartDate string `json:"start_date"`
		} `json:"node"`
		ListStatus struct {
			Status string `json:"status"`
			Score  int    `json:"score"`
		} `json:"list_status"`
	} `json:"data"`
	Paging struct {
		Next string `json:"next"`
	} `json:"paging"`
}

// GetUserList fetches and maps every page of a public anime list to source-neutral entries.
func (c *Client) GetUserList(ctx context.Context, username string) ([]watchlist.Entry, error) {
	if c.clientID == "" {
		return nil, ErrNotConfigured
	}
	next := fmt.Sprintf("%s/v2/users/%s/animelist?fields=%s&nsfw=true&limit=1000",
		c.baseURL, url.PathEscape(username), url.QueryEscape(fields))
	var entries []watchlist.Entry
	for next != "" {
		page, err := c.fetch(ctx, next)
		if err != nil {
			return nil, err
		}
		entries = append(entries, mapEntries(page)...)
		next = page.Paging.Next
	}
	if len(entries) == 0 {
		return nil, ErrEmpty
	}
	return entries, nil
}

// fetch GETs one page URL with the client-id header and decodes it.
func (c *Client) fetch(ctx context.Context, u string) (listResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return listResponse{}, fmt.Errorf("mal: %w", err)
	}
	req.Header.Set("X-MAL-CLIENT-ID", c.clientID)
	resp, err := c.http.Do(req)
	if err != nil {
		return listResponse{}, fmt.Errorf("mal fetch: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return listResponse{}, ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return listResponse{}, ErrUnauthorized
	default:
		return listResponse{}, fmt.Errorf("mal: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(httpsafe.LimitBody(resp.Body))
	if err != nil {
		return listResponse{}, fmt.Errorf("mal read: %w", err)
	}
	var page listResponse
	if err := json.Unmarshal(body, &page); err != nil {
		return listResponse{}, fmt.Errorf("mal parse: %w", err)
	}
	return page, nil
}

// mapEntries turns one decoded page into matcher entries. The romaji node title is the
// primary; the English alternative and any synonyms become aliases so either side can match
// the library. A synonym that carries no latin letters folds to an empty key and is dropped.
func mapEntries(page listResponse) []watchlist.Entry {
	out := make([]watchlist.Entry, 0, len(page.Data))
	for _, d := range page.Data {
		e := watchlist.Entry{
			Title:   d.Node.Title,
			Year:    d.Node.StartSeason.Year,
			Rating:  d.ListStatus.Score,
			Watched: d.ListStatus.Status == "completed",
		}
		if e.Year == 0 {
			e.Year = yearOf(d.Node.StartDate)
		}
		e.Aliases = aliasesFor(d.Node.Title, d.Node.AlternativeTitles.En, d.Node.AlternativeTitles.Synonyms)
		out = append(out, e)
	}
	return out
}

// aliasesFor keeps the English title and synonyms that carry a title distinct (after the
// shared normalization) from the primary title and from one another.
func aliasesFor(title, en string, synonyms []string) []string {
	seen := map[string]bool{watchlist.Normalize(title): true}
	var out []string
	for _, alt := range append([]string{en}, synonyms...) {
		alt = strings.TrimSpace(alt)
		key := watchlist.Normalize(alt)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, alt)
	}
	return out
}

// yearOf reads the leading four-digit year from a MyAnimeList start_date ("2013-04-07",
// "2013-04", or "2013"); anything shorter yields 0.
func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}
