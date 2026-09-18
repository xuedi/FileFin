// Package tmdb is a small client for The Movie Database API (https://www.themoviedb.org),
// used to resolve an item's cast by the IMDb id OMDb already gave it and to fetch each
// person's details and profile photo. Every call runs server side; the browser only ever
// loads what FileFin stored.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"filefin/internal/httpsafe"
)

// ErrNotFound is returned when TMDb has no record for what was asked.
var ErrNotFound = errors.New("tmdb: not found")

// Kinds of title TMDb distinguishes, and the path segment each one lives under.
const (
	KindMovie = "movie"
	KindTV    = "tv"
)

// ProfileSize is the TMDb image size fetched for a person's photo: 185 px wide, which is
// already the size the Cast card shows, so nothing larger is ever downloaded.
const ProfileSize = "w185"

const (
	defaultPace     = 100 * time.Millisecond // minimum gap between two requests
	maxRetryAfter   = 10 * time.Second
	defaultRetryGap = 2 * time.Second
)

// Title is the movie or series an IMDb id resolved to.
type Title struct {
	Kind string
	ID   int
	Name string
}

// Credit is one credited cast member, in billing order.
type Credit struct {
	ID          int
	Name        string
	Character   string
	Department  string
	ProfilePath string
	Order       int
}

// Person is TMDb's record of one person.
type Person struct {
	ID          int
	Name        string
	Department  string
	Birthday    string
	Deathday    string
	ProfilePath string
}

// Client talks to TMDb with either credential TMDb hands out: the short v3 API key (sent
// as a query parameter) or the long v4 read access token (sent as a bearer header).
// Requests are paced so a long queue never bursts against the API.
type Client struct {
	key     string
	token   string
	http    *http.Client
	baseURL string
	imgURL  string
	pace    time.Duration

	mu   sync.Mutex
	last time.Time
}

// New returns a Client for a v3 API key or a v4 read access token.
func New(credential string) *Client {
	c := &Client{
		http:    &http.Client{Timeout: 15 * time.Second, CheckRedirect: httpsafe.NoInternalRedirect},
		baseURL: "https://api.themoviedb.org/3",
		imgURL:  "https://image.tmdb.org/t/p",
		pace:    defaultPace,
	}
	credential = strings.TrimSpace(credential)
	if isReadToken(credential) {
		c.token = credential
	} else {
		c.key = credential
	}
	return c
}

// NewAt returns a Client that talks to a TMDb-compatible API and image host other than the
// public ones, such as a test server. imageURL is the base the size segment is appended to.
func NewAt(credential, apiURL, imageURL string) *Client {
	c := New(credential)
	c.baseURL, c.imgURL = apiURL, imageURL
	return c
}

// isReadToken reports whether a credential is a v4 read access token, which is a JWT.
func isReadToken(s string) bool {
	return strings.HasPrefix(s, "eyJ") && strings.Count(s, ".") == 2
}

// FindByIMDb resolves an IMDb id to the TMDb movie or series it names. An id TMDb does not
// know, or one that names an episode or a person, is ErrNotFound.
func (c *Client) FindByIMDb(ctx context.Context, imdbID string) (Title, error) {
	var body struct {
		Movies []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"movie_results"`
		TV []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"tv_results"`
	}
	q := url.Values{"external_source": {"imdb_id"}}
	if err := c.get(ctx, "/find/"+url.PathEscape(imdbID), q, &body); err != nil {
		return Title{}, fmt.Errorf("tmdb find %s: %w", imdbID, err)
	}
	switch {
	case len(body.Movies) > 0:
		return Title{Kind: KindMovie, ID: body.Movies[0].ID, Name: body.Movies[0].Title}, nil
	case len(body.TV) > 0:
		return Title{Kind: KindTV, ID: body.TV[0].ID, Name: body.TV[0].Name}, nil
	}
	return Title{}, ErrNotFound
}

// Credits returns a title's cast in billing order: a movie's credits, or a series' cast
// aggregated across every season. For a series the character is the role an actor played
// in the most episodes.
func (c *Client) Credits(ctx context.Context, t Title) ([]Credit, error) {
	type person struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Department  string `json:"known_for_department"`
		ProfilePath string `json:"profile_path"`
		Character   string `json:"character"`
		Order       int    `json:"order"`
		Roles       []struct {
			Character    string `json:"character"`
			EpisodeCount int    `json:"episode_count"`
		} `json:"roles"`
	}
	var body struct {
		Cast []person `json:"cast"`
	}
	path := "/movie/" + strconv.Itoa(t.ID) + "/credits"
	if t.Kind == KindTV {
		path = "/tv/" + strconv.Itoa(t.ID) + "/aggregate_credits"
	}
	if err := c.get(ctx, path, url.Values{}, &body); err != nil {
		return nil, fmt.Errorf("tmdb credits %s/%d: %w", t.Kind, t.ID, err)
	}
	out := make([]Credit, 0, len(body.Cast))
	for _, p := range body.Cast {
		character, best := p.Character, -1
		for _, r := range p.Roles {
			if r.EpisodeCount > best && strings.TrimSpace(r.Character) != "" {
				character, best = r.Character, r.EpisodeCount
			}
		}
		out = append(out, Credit{
			ID: p.ID, Name: strings.TrimSpace(p.Name), Character: strings.TrimSpace(character),
			Department: p.Department, ProfilePath: p.ProfilePath, Order: p.Order,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out, nil
}

// Person returns one person's details.
func (c *Client) Person(ctx context.Context, id int) (Person, error) {
	var body struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Department  string `json:"known_for_department"`
		Birthday    string `json:"birthday"`
		Deathday    string `json:"deathday"`
		ProfilePath string `json:"profile_path"`
	}
	if err := c.get(ctx, "/person/"+strconv.Itoa(id), url.Values{}, &body); err != nil {
		return Person{}, fmt.Errorf("tmdb person %d: %w", id, err)
	}
	return Person{
		ID: body.ID, Name: body.Name, Department: body.Department,
		Birthday: body.Birthday, Deathday: body.Deathday, ProfilePath: body.ProfilePath,
	}, nil
}

// ProfileImage downloads a person's photo at ProfileSize from TMDb's image host.
func (c *Client) ProfileImage(ctx context.Context, profilePath string) ([]byte, error) {
	return c.image(ctx, ProfileSize, profilePath)
}

// image downloads one file from TMDb's image host. Only a plain "/name.ext" path at a
// "w<digits>" or "original" size is accepted, so nothing a caller passes can steer the request
// anywhere else.
func (c *Client) image(ctx context.Context, size, imagePath string) ([]byte, error) {
	if !validImagePath(imagePath) || !validImageSize(size) {
		return nil, fmt.Errorf("tmdb image: bad path %q", imagePath)
	}
	resp, err := c.do(ctx, c.imgURL+"/"+size+imagePath, false)
	if err != nil {
		return nil, fmt.Errorf("tmdb image %s: %w", imagePath, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(httpsafe.LimitBody(resp.Body))
	if err != nil {
		return nil, fmt.Errorf("tmdb image read %s: %w", imagePath, err)
	}
	return data, nil
}

func validImagePath(p string) bool {
	if len(p) < 2 || p[0] != '/' {
		return false
	}
	for _, r := range p[1:] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return !strings.Contains(p, "..")
}

func validImageSize(s string) bool {
	if s == "original" {
		return true
	}
	if len(s) < 2 || s[0] != 'w' {
		return false
	}
	_, err := strconv.Atoi(s[1:])
	return err == nil
}

// get performs an authenticated API GET and decodes the JSON body into v.
func (c *Client) get(ctx context.Context, path string, q url.Values, v any) error {
	if c.key != "" {
		q.Set("api_key", c.key)
	}
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	resp, err := c.do(ctx, u, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(httpsafe.LimitBody(resp.Body)).Decode(v)
}

// do sends one paced GET, retrying once on 429 after the delay TMDb asks for, and maps the
// statuses callers care about onto errors. The caller closes the body of a nil-error reply.
func (c *Client) do(ctx context.Context, rawURL string, auth bool) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if auth && c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			// A url.Error quotes the whole URL, and with it the v3 key, into logs and task rows.
			var ue *url.Error
			if errors.As(err, &ue) {
				return nil, ue.Err
			}
			return nil, err
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			return resp, nil
		case resp.StatusCode == http.StatusTooManyRequests && attempt == 0:
			gap := retryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			if err := sleep(ctx, gap); err != nil {
				return nil, err
			}
			continue
		}
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusNotFound:
			return nil, ErrNotFound
		case http.StatusUnauthorized:
			return nil, errors.New("invalid API key")
		}
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
}

// wait holds a request back until the pace since the previous one has passed.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	next := c.last.Add(c.pace)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	c.last = next
	c.mu.Unlock()
	return sleep(ctx, time.Until(next))
}

// retryAfter reads a Retry-After header in seconds, bounded so a hostile value cannot stall
// the agent.
func retryAfter(h string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || n < 0 {
		return defaultRetryGap
	}
	if d := time.Duration(n) * time.Second; d < maxRetryAfter {
		return d
	}
	return maxRetryAfter
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
