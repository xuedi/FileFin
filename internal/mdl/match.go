package mdl

import "strings"

// LibraryItem is the minimal library entry the matcher pairs MDL entries against.
type LibraryItem struct {
	ID    string
	Title string
	Year  int
}

// Match is one MDL entry paired (or not) to a library item.
type Match struct {
	Entry Entry
	Item  *LibraryItem // nil when no library title matched
	Exact bool         // true when matched on normalized title and year
}

// MatchLibrary pairs each scraped entry to a library item by normalized title, preferring
// an exact year match when both sides carry a year. Matching is deliberately approximate:
// MyDramaList titles and on-disk titles rarely agree on punctuation or romanization, so
// the caller is expected to surface the result for review rather than apply it blindly.
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
		cands := idx[normalize(e.Title)]
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
			}
			it := pick
			m.Item = &it
		default:
			it := cands[0]
			m.Item = &it
		}
		out = append(out, m)
	}
	return out
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
