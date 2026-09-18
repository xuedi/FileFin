package sources

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"filefin/internal/importer"
	"filefin/internal/people"
)

// Conflict classes. A field conflict is a disagreement about one fact; an identity conflict
// means the sources probably describe different works, which no rule may paper over.
const (
	ClassField    = "field"
	ClassIdentity = "identity"
)

// fold lowercases a value, strips accents, and collapses its spacing, for comparing values
// that differ only in presentation.
func fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(s) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// sameValues reports whether two values are the same after folding; a list compares as a set.
func sameValues(a, b []string, list bool) bool {
	if !list {
		return fold(strings.Join(a, " ")) == fold(strings.Join(b, " "))
	}
	return sameSet(foldAll(a, fold), foldAll(b, fold))
}

func foldAll(vals []string, key func(string) string) map[string]bool {
	out := map[string]bool{}
	for _, v := range vals {
		if k := key(v); k != "" {
			out[k] = true
		}
	}
	return out
}

func sameSet(a, b map[string]bool) bool {
	return len(a) == len(b) && subset(a, b)
}

func subset(a, b map[string]bool) bool {
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func intersects(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

// parenthetical drops credit notes such as "(screenplay)" or "(based on the novel by)".
var parenthetical = regexp.MustCompile(`\([^)]*\)`)

func personKey(name string) string {
	return people.NameKey(parenthetical.ReplaceAllString(name, ""))
}

// titleKeys is every title a snapshot answers to, folded for comparison.
func titleKeys(s *importer.Snapshot) map[string]bool {
	keys := foldAll(s.AltTitles, people.NameKey)
	if k := people.NameKey(s.Title); k != "" {
		keys[k] = true
	}
	if k := people.NameKey(s.Metadata["originalTitle"]); k != "" {
		keys[k] = true
	}
	return keys
}

// compare classifies two sources' values of one field: "" when they agree, else a conflict
// class. Fields whose policy never conflicts always agree.
func compare(f Field, a, b []string, sa, sb *importer.Snapshot) string {
	switch f.Policy {
	case PolicyTitle:
		if people.NameKey(strings.Join(a, " ")) == people.NameKey(strings.Join(b, " ")) ||
			titleKeys(sb)[people.NameKey(strings.Join(a, " "))] || titleKeys(sa)[people.NameKey(strings.Join(b, " "))] {
			return ""
		}
		return ClassField
	case PolicyYear:
		ya, yb := firstInt(a), firstInt(b)
		switch d := abs(ya - yb); {
		case d == 0:
			return ""
		case d == 1:
			return ClassField
		}
		return ClassIdentity
	case PolicyKind, PolicyID:
		if sameValues(a, b, false) {
			return ""
		}
		return ClassIdentity
	case PolicyRuntime:
		ra, rb := firstInt(a), firstInt(b)
		if ra == 0 || rb == 0 {
			return exact(a, b)
		}
		tolerance := max(ra, rb) / 10
		if tolerance < 3 {
			tolerance = 3
		}
		if abs(ra-rb) <= tolerance {
			return ""
		}
		return ClassField
	case PolicyRelease:
		ya, yb := yearIn(strings.Join(a, " ")), yearIn(strings.Join(b, " "))
		if ya == 0 || yb == 0 {
			return exact(a, b)
		}
		if ya == yb {
			return ""
		}
		return ClassField
	case PolicyDirectors:
		ka, kb := foldAll(a, personKey), foldAll(b, personKey)
		if subset(ka, kb) || subset(kb, ka) {
			return ""
		}
		return ClassField
	case PolicyWriters:
		if intersects(foldAll(a, personKey), foldAll(b, personKey)) {
			return ""
		}
		return ClassField
	case PolicyExact:
		return exact(a, b)
	}
	return ""
}

func exact(a, b []string) string {
	if sameValues(a, b, false) {
		return ""
	}
	return ClassField
}

// firstInt reads the leading number of a value ("136", "136 min"), 0 when there is none.
func firstInt(v []string) int {
	s := strings.TrimSpace(strings.Join(v, " "))
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

var yearPattern = regexp.MustCompile(`\b(1[89]\d\d|20\d\d)\b`)

// yearIn finds the year in a date written either way ("1999-03-31", "31 Mar 1999").
func yearIn(s string) int {
	m := yearPattern.FindString(s)
	n, _ := strconv.Atoi(m)
	return n
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
