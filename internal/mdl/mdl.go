// Package mdl reads a user's public MyDramaList drama list. MyDramaList's official API is
// not open to the public, so this fetches the public profile list page
// (mydramalist.com/dramalist/{user}) and parses the server-rendered status tables. It is
// best-effort and inherently fragile: a markup change upstream breaks parsing, which the
// offline fixture test exists to catch early. Only public lists are readable.
package mdl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Status is a normalized MyDramaList list bucket.
type Status string

const (
	StatusCompleted   Status = "completed"
	StatusWatching    Status = "watching"
	StatusOnHold      Status = "onhold"
	StatusDropped     Status = "dropped"
	StatusPlanToWatch Status = "plantowatch"
	StatusUnknown     Status = ""
)

// Entry is one title on a user's list.
type Entry struct {
	Title  string // display title as shown on MDL
	Year   int    // release year; 0 when MDL shows "TBA"
	Rating int    // the user's score rounded to 1-10; 0 when unrated
	Status Status
}

// Watched reports whether this entry's status counts as fully watched.
func (e Entry) Watched() bool { return e.Status == StatusCompleted }

var (
	// ErrNotFound means the list page does not exist (no such user).
	ErrNotFound = errors.New("mdl: list not found")
	// ErrEmpty means the page loaded but held no rows: a private or genuinely empty list,
	// neither of which MyDramaList renders for a third party.
	ErrEmpty = errors.New("mdl: list is empty or private")
)

const userAgent = "Mozilla/5.0 (compatible; FileFin)"

// Client fetches public MyDramaList pages.
type Client struct {
	http    *http.Client
	baseURL string
}

// New returns a Client with a sane timeout.
func New() *Client {
	return &Client{http: &http.Client{Timeout: 30 * time.Second}, baseURL: "https://mydramalist.com"}
}

// GetUserList fetches and parses the public drama list for username.
func (c *Client) GetUserList(ctx context.Context, username string) ([]Entry, error) {
	u := c.baseURL + "/dramalist/" + url.PathEscape(username)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("mdl: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mdl fetch %q: %w", username, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mdl: http %d", resp.StatusCode)
	}
	entries, err := parseList(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, ErrEmpty
	}
	return entries, nil
}

// parseList walks the list page in document order: each status label (an
// <h3 class="mdl-style-list-label">) sets the current bucket, and every following table
// row that carries a title cell becomes an entry in that bucket.
func parseList(r io.Reader) ([]Entry, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("mdl parse: %w", err)
	}
	var entries []Entry
	current := StatusUnknown
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch {
			case n.Data == "h3" && hasClass(n, "mdl-style-list-label"):
				current = statusFromLabel(textOf(n))
			case n.Data == "tr" && findClass(n, "mdl-style-col-title") != nil:
				if e, ok := parseRow(n, current); ok {
					entries = append(entries, e)
				}
				return // a parsed row has no nested rows to descend into
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return entries, nil
}

// parseRow extracts one entry from a list <tr>. A row missing its title is skipped.
func parseRow(tr *html.Node, status Status) (Entry, bool) {
	titleCell := findClass(tr, "mdl-style-col-title")
	if titleCell == nil {
		return Entry{}, false
	}
	title := strings.TrimSpace(textOf(findTitleAnchor(titleCell)))
	if title == "" {
		return Entry{}, false
	}
	e := Entry{Title: title, Status: status}
	if y := findClass(tr, "mdl-style-col-year"); y != nil {
		e.Year, _ = strconv.Atoi(strings.TrimSpace(textOf(y))) // "TBA" -> 0
	}
	if sc := findClassDeep(tr, "score"); sc != nil {
		e.Rating = roundScore(strings.TrimSpace(textOf(sc)))
	}
	return e, true
}

// roundScore turns MyDramaList's "0.0".."10.0" string into a 1-10 int; "0.0" (unrated)
// and any unparseable value become 0.
func roundScore(s string) int {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0
	}
	n := int(math.Round(f))
	if n < 1 {
		n = 1
	}
	if n > 10 {
		n = 10
	}
	return n
}

// statusFromLabel maps a MyDramaList section label to a normalized status.
func statusFromLabel(label string) Status {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "completed":
		return StatusCompleted
	case "currently watching", "watching":
		return StatusWatching
	case "on hold":
		return StatusOnHold
	case "dropped":
		return StatusDropped
	case "plan to watch":
		return StatusPlanToWatch
	default:
		return StatusUnknown
	}
}

// --- small html helpers ---

// hasClass reports whether element n carries the exact CSS class cls.
func hasClass(n *html.Node, cls string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, f := range strings.Fields(a.Val) {
				if f == cls {
					return true
				}
			}
		}
	}
	return false
}

// findClass returns the first descendant (or n itself) that carries the exact class cls.
func findClass(n *html.Node, cls string) *html.Node {
	if n.Type == html.ElementNode && hasClass(n, cls) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findClass(c, cls); got != nil {
			return got
		}
	}
	return nil
}

// findClassDeep is findClass but never returns n itself, so a row whose own class happens
// to contain the token still matches a true descendant cell.
func findClassDeep(n *html.Node, cls string) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findClass(c, cls); got != nil {
			return got
		}
	}
	return nil
}

// findTitleAnchor returns the title's <a> (preferring one classed "title"), or the cell
// itself as a fallback so textOf still yields something.
func findTitleAnchor(cell *html.Node) *html.Node {
	if a := findClass(cell, "title"); a != nil {
		return a
	}
	var first *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if first != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			first = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(cell)
	if first != nil {
		return first
	}
	return cell
}

// textOf returns the concatenated, space-collapsed text content of a node.
func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
