// Package watchlist matches a user's external watch-history entries (from MyDramaList,
// MyAnimeList, ...) against the local library. The match is deliberately approximate:
// source titles and on-disk titles rarely agree on punctuation or romanization, so the
// caller is expected to surface the result for review rather than apply it blindly. The
// year is part of the decision so a shared normalized title (e.g. an anime "Kingdom" and
// a Korean drama "Kingdom") never auto-selects the wrong release.
package watchlist

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// yearTolerance is how far a unique title's year may drift and still be trusted: a unique
// title with a year within +/-1 (or no year at all) is confident, but a wildly different
// year is left for review.
const yearTolerance = 1

// fuzzyThreshold is the minimum normalized similarity a fuzzy fallback must clear, and
// fuzzySeparation is how far the best key must beat the runner-up before it is offered, so
// a near-tie never produces a silent guess.
const (
	fuzzyThreshold  = 0.9
	fuzzySeparation = 0.05
)

// Confidence grades how strongly an entry is paired to a library item. Only exact and
// confident rows are pre-selected in the UI; approximate rows are surfaced for a deliberate
// tick and never auto-applied.
type Confidence string

const (
	ConfidenceExact       Confidence = "exact"       // normalized title and year agree
	ConfidenceConfident   Confidence = "confident"   // unique title, year absent or within tolerance
	ConfidenceApproximate Confidence = "approximate" // offered for review, never pre-selected
)

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
	Confidence  Confidence   // grade of the pairing; empty when Item is nil
	Reason      string       // short hint for a non-year approximate match (e.g. "similar title")
	LibraryYear int          // the matched item's year, for the UI to render a mismatch
}

// MatchLibrary pairs each entry to a library item. A title unique in the library is trusted
// even with a missing or slightly-off year (confident); a colliding title stays year-strict
// so the wrong release never auto-selects. A title that keys to nothing falls back to a
// bounded fuzzy pass that only ever offers an approximate, reviewable proposal.
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
		out = append(out, matchOne(idx, e))
	}
	return out
}

// matchOne resolves a single entry: an exact-key hit decides by uniqueness and year, and a
// miss drops to the fuzzy fallback.
func matchOne(idx map[string][]LibraryItem, e Entry) Match {
	cands := candidates(idx, e)
	switch {
	case len(cands) == 0:
		return fuzzyMatch(idx, e)
	case len(cands) == 1:
		return decideUnique(e, cands[0])
	default:
		return decideCollision(e, cands)
	}
}

// decideUnique grades a title that is unique in the library. The year only sharpens the
// grade here; it never rejects the sole candidate unless both years are known and far apart.
func decideUnique(e Entry, it LibraryItem) Match {
	m := Match{Entry: e, Item: &it, LibraryYear: it.Year}
	switch {
	case e.Year > 0 && it.Year > 0 && it.Year == e.Year:
		m.Confidence = ConfidenceExact
	case e.Year == 0 || it.Year == 0 || abs(it.Year-e.Year) <= yearTolerance:
		m.Confidence = ConfidenceConfident
	default:
		m.Confidence = ConfidenceApproximate
	}
	return m
}

// decideCollision grades a title shared by several library items, where the year is the only
// discriminator: an exact-year hit wins; failing that, a single candidate within tolerance is
// trusted; otherwise the closest year is offered for review.
func decideCollision(e Entry, cands []LibraryItem) Match {
	m := Match{Entry: e}
	if e.Year > 0 {
		for _, c := range cands {
			if c.Year == e.Year {
				m.Item, m.LibraryYear, m.Confidence = itemPtr(c), c.Year, ConfidenceExact
				return m
			}
		}
		var within []LibraryItem
		for _, c := range cands {
			if c.Year > 0 && abs(c.Year-e.Year) <= yearTolerance {
				within = append(within, c)
			}
		}
		if len(within) == 1 {
			m.Item, m.LibraryYear, m.Confidence = itemPtr(within[0]), within[0].Year, ConfidenceConfident
			return m
		}
	}
	pick := closestYear(cands, e.Year)
	m.Item, m.LibraryYear, m.Confidence = itemPtr(pick), pick.Year, ConfidenceApproximate
	return m
}

// fuzzyMatch is the last resort when no key matches: it scores the entry's normalized title
// against every library key by edit-distance ratio and offers the best only when it clears
// the threshold and clearly beats the runner-up. The result is always approximate.
func fuzzyMatch(idx map[string][]LibraryItem, e Entry) Match {
	m := Match{Entry: e}
	q := normalize(e.Title)
	if q == "" {
		return m
	}
	bestKey := ""
	best, second := 0.0, 0.0
	for key := range idx {
		if tooDifferentLength(len(q), len(key)) {
			continue
		}
		s := similar(q, key)
		if s > best {
			bestKey, best, second = key, s, best
		} else if s > second {
			second = s
		}
	}
	if bestKey == "" || best < fuzzyThreshold || best-second < fuzzySeparation {
		return m
	}
	pick := closestYear(idx[bestKey], e.Year)
	m.Item, m.LibraryYear = itemPtr(pick), pick.Year
	m.Confidence, m.Reason = ConfidenceApproximate, "similar title"
	return m
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

// closestYear returns the candidate whose year is nearest year (the first on a tie).
func closestYear(cands []LibraryItem, year int) LibraryItem {
	pick := cands[0]
	for _, c := range cands {
		if abs(c.Year-year) < abs(pick.Year-year) {
			pick = c
		}
	}
	return pick
}

// itemPtr returns a pointer to a copy of it, so the returned Match never aliases the caller's
// candidate slice.
func itemPtr(it LibraryItem) *LibraryItem { return &it }

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Normalize exposes the matcher's title normalization so sources can drop aliases that
// collapse to the same key as the primary title.
func Normalize(s string) string { return normalize(s) }

// normalize reduces a title to lowercase alphanumerics so cosmetic differences (punctuation,
// spacing, "&" vs "and", diacritics, a leading article) do not block a match.
func normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "&", "and")
	s = foldDiacritics(s)
	s = stripLeadingArticle(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// foldDiacritics decomposes s (NFKD) and drops combining marks, so a macron or accent folds
// to its base letter and romanization differences stop blocking a match.
func foldDiacritics(s string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(s) {
		if unicode.Is(unicode.Mn, r) { // Mn: non-spacing combining marks
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// stripLeadingArticle drops a single leading English article so "The Goblin" and "Goblin"
// share a key. Only a leading article is stripped; interior words are untouched.
func stripLeadingArticle(s string) string {
	s = strings.TrimLeft(s, " ")
	for _, art := range []string{"the ", "an ", "a "} {
		if strings.HasPrefix(s, art) {
			return s[len(art):]
		}
	}
	return s
}

// similar is the normalized Levenshtein similarity of two keys, 1 for equal and approaching
// 0 as they diverge.
func similar(a, b string) float64 {
	if a == b {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	longest := len(a)
	if len(b) > longest {
		longest = len(b)
	}
	return 1 - float64(levenshtein(a, b))/float64(longest)
}

// tooDifferentLength reports when two keys are too far apart in length to possibly clear the
// fuzzy threshold, since the edit distance is at least their length gap. It bounds the fuzzy
// pass without changing its result.
func tooDifferentLength(la, lb int) bool {
	longest := la
	if lb > longest {
		longest = lb
	}
	return float64(abs(la-lb)) > (1-fuzzyThreshold)*float64(longest)
}

// levenshtein is the edit distance between two ASCII keys (normalize leaves only [a-z0-9],
// so byte and rune indexing coincide).
func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
