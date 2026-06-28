// Package watchlist matches a user's external watch-history entries (from MyDramaList,
// MyAnimeList, ...) against the local library. The match is deliberately approximate:
// source titles and on-disk titles rarely agree on punctuation or romanization, so the
// caller is expected to surface the result for review rather than apply it blindly. The
// year is part of the decision so a shared normalized title (e.g. an anime "Kingdom" and
// a Korean drama "Kingdom") never auto-selects the wrong release.
package watchlist

import "strings"

// LibraryItem is the minimal library entry the matcher pairs entries against.
type LibraryItem struct {
	ID    string
	Title string
	Year  int
}

// Entry is one source-neutral watch-history row. Aliases are alternative titles to try
// when the primary title finds nothing (e.g. a MyAnimeList romaji title plus its English
// alternative). Year is 0 when the source carries none; Rating is 0 when unrated.
type Entry struct {
	Title   string
	Aliases []string
	Year    int
	Rating  int
	Watched bool
}

// Match is one entry paired (or not) to a library item.
type Match struct {
	Entry       Entry
	Item        *LibraryItem // nil when no library title matched
	Exact       bool         // true only when matched on normalized title AND year
	LibraryYear int          // the matched item's year, for the UI to render a mismatch
}

// MatchLibrary pairs each entry to a library item by normalized title. When both sides
// carry a year, an exact year match wins and is flagged exact; otherwise the candidate
// whose year is closest is offered, marked approximate, so a year-mismatched row can be
// surfaced and left unselected rather than applied blindly.
func MatchLibrary(entries []Entry, lib []LibraryItem) []Match {
	idx := map[string][]LibraryItem{}
	for _, it := range lib {
		key := normalize(it.Title)
		if key == "" {
			continue
		}
		idx[key] = append(idx[key], it)
	}
	out := make([]Match, 0, len(entries))
	for _, e := range entries {
		m := Match{Entry: e}
		cands := candidates(idx, e)
		switch {
		case len(cands) == 0:
			// unmatched
		case e.Year > 0:
			pick := cands[0]
			for i := range cands {
				if cands[i].Year == e.Year {
					pick, m.Exact = cands[i], true
					break
				}
				if abs(cands[i].Year-e.Year) < abs(pick.Year-e.Year) {
					pick = cands[i]
				}
			}
			it := pick
			m.Item, m.LibraryYear = &it, it.Year
		default:
			it := cands[0]
			m.Item, m.LibraryYear = &it, it.Year
		}
		out = append(out, m)
	}
	return out
}

// candidates returns the first non-empty candidate set found by normalizing the entry's
// title and then each of its aliases in turn, so an alias only kicks in when the primary
// title matches nothing.
func candidates(idx map[string][]LibraryItem, e Entry) []LibraryItem {
	if c := idx[normalize(e.Title)]; len(c) > 0 {
		return c
	}
	for _, alias := range e.Aliases {
		if c := idx[normalize(alias)]; len(c) > 0 {
			return c
		}
	}
	return nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// normalize reduces a title to lowercase alphanumerics so cosmetic differences
// (punctuation, spacing, "&" vs "and") do not block a match.
func normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "&", "and")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
