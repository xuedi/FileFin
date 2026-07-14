// Package mal reads a user's public MyAnimeList anime list. MyAnimeList's API v2 needs an
// application client id we do not have, but the public list page is backed by a JSON endpoint
// (myanimelist.net/animelist/{user}/load.json) that needs no auth, so this reads that - the
// same public-profile posture as the MyDramaList scraper. Unlike that HTML scraper the surface
// is JSON, so it is not tied to page markup. Only public lists are readable.
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
	// ErrNotFound means MyAnimeList has no such user (the list page 404s).
	ErrNotFound = errors.New("mal: user not found")
	// ErrEmpty means the list loaded but held no entries: a private or genuinely empty list.
	ErrEmpty = errors.New("mal: list is empty or private")
)

const (
	// statusCompleted is MyAnimeList's list-status code for a finished anime.
	statusCompleted = 2
	// pageSize is how many rows load.json returns per request; a short page is the last one.
	pageSize  = 300
	userAgent = "Mozilla/5.0 (compatible; FileFin)"
)

// Client reads public MyAnimeList lists.
type Client struct {
	http     *http.Client
	baseURL  string
	pageSize int
}

// New returns a Client with a sane timeout.
func New() *Client {
	return &Client{
		http:     &http.Client{Timeout: 30 * time.Second, CheckRedirect: httpsafe.NoInternalRedirect},
		baseURL:  "https://myanimelist.net",
		pageSize: pageSize,
	}
}

// listRow is one entry in a load.json page - only the fields the matcher needs. The public
// endpoint carries the romaji title, its English title, the member's status/score, and the
// anime's air date as "MM-DD-YY".
type listRow struct {
	Status        int    `json:"status"`
	Score         int    `json:"score"`
	AnimeTitle    string `json:"anime_title"`
	AnimeTitleEng string `json:"anime_title_eng"`
	StartDate     string `json:"anime_start_date_string"`
}

// GetUserList fetches every page of a public anime list and maps it to source-neutral entries.
func (c *Client) GetUserList(ctx context.Context, username string) ([]watchlist.Entry, error) {
	var entries []watchlist.Entry
	for offset := 0; ; offset += c.pageSize {
		rows, err := c.fetch(ctx, username, offset)
		if err != nil {
			return nil, err
		}
		entries = append(entries, mapEntries(rows)...)
		if len(rows) < c.pageSize {
			break // a page shorter than the page size is the last one
		}
	}
	if len(entries) == 0 {
		return nil, ErrEmpty
	}
	return entries, nil
}

// fetch GETs one page of the public list (status=7 is "all statuses") and decodes it.
func (c *Client) fetch(ctx context.Context, username string, offset int) ([]listRow, error) {
	u := fmt.Sprintf("%s/animelist/%s/load.json?status=7&offset=%d", c.baseURL, url.PathEscape(username), offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("mal: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mal fetch %q: %w", username, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("mal: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(httpsafe.LimitBody(resp.Body))
	if err != nil {
		return nil, fmt.Errorf("mal read: %w", err)
	}
	var rows []listRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("mal parse: %w", err)
	}
	return rows, nil
}

// mapEntries turns one decoded page into matcher entries. The romaji title is the primary; the
// English title becomes an alias so either romanization can match the library.
func mapEntries(rows []listRow) []watchlist.Entry {
	out := make([]watchlist.Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, watchlist.Entry{
			Title:   r.AnimeTitle,
			Aliases: aliasesFor(r.AnimeTitle, r.AnimeTitleEng),
			Year:    yearOf(r.StartDate),
			Rating:  r.Score,
			Watched: r.Status == statusCompleted,
		})
	}
	return out
}

// aliasesFor keeps the English title when it is distinct (after the shared normalization) from
// the primary title, so a library item filed under its English name can still match.
func aliasesFor(title, eng string) []string {
	eng = strings.TrimSpace(eng)
	key := watchlist.Normalize(eng)
	if key == "" || key == watchlist.Normalize(title) {
		return nil
	}
	return []string{eng}
}

// yearOf parses MyAnimeList's public "MM-DD-YY" start-date string to a four-digit year. The
// two-digit year is resolved on a fixed pivot (<=30 -> 20xx, else 19xx), which covers every
// year anime actually air in; an absent or unparseable date yields 0 (the matcher tolerates
// a missing year, so a library-unique title still matches without it).
func yearOf(date string) int {
	parts := strings.Split(date, "-")
	if len(parts) != 3 {
		return 0
	}
	yy, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0
	}
	switch len(parts[2]) {
	case 2:
		if yy <= 30 {
			return 2000 + yy
		}
		return 1900 + yy
	case 4:
		return yy
	default:
		return 0
	}
}
