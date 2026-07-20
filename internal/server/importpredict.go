package server

import (
	"fmt"
	"strings"

	"filefin/internal/library"
)

// The evidence a marker must carry before it is allowed to preselect a category. One
// import button queues every row, so a confident wrong guess misfiles silently - below
// these thresholds the row reports no guess instead, and the dropdown keeps its plain
// default.
const (
	minSightings = 2   // a marker seen once is an accident, not a pattern
	minPurity    = 0.8 // and it must nearly always land in the same category
)

// How much a vote is worth, by where it came from. A declared marker is the admin stating
// the rule outright, so it counts as evidence at the same bar a learned marker must clear;
// a shipped seed is a guess from a list that rots, so anything learned outvotes it.
const (
	declaredWeight = minSightings
	seedWeight     = 1
)

// prediction is the category an import row is preselected into, with the evidence in words.
// A zero CategoryID means nothing earned the guess.
type prediction struct {
	CategoryID int64
	Reason     string
}

// predictor scores import rows against the categories' markers. It is built once per scan
// and asked about each row independently: a row's guess never depends on the rows around it.
//
// Following the previous row was measured on the labelled corpus and dropped. It preselected
// three extra rows and got all three wrong, because a dump is not reliably one region at a
// time - and one wrong preselect costs more than a missing one, since a single Import button
// queues them all.
type predictor struct {
	cats   []library.Category
	counts map[string]map[int64]int // learned marker -> category -> times seen
	totals map[string]int           // learned marker -> times seen anywhere
}

func newPredictor(cats []library.Category) *predictor {
	p := &predictor{cats: cats, counts: map[string]map[int64]int{}, totals: map[string]int{}}
	for _, c := range cats {
		for marker, n := range c.Markers.Learned {
			if p.counts[marker] == nil {
				p.counts[marker] = map[int64]int{}
			}
			p.counts[marker][c.ID] += n
			p.totals[marker] += n
		}
	}
	return p
}

// vote is one category's running case, keeping the strongest single reason rather than a
// list: the admin wants to know why a row was preselected, not every hint that agreed.
type vote struct {
	score  int
	weight int // the strongest single signal behind this vote
	reason string
}

// predict picks the category an item most likely belongs to. The signals are tried in order
// of strength: the kind verdict rules categories out entirely, then learned markers, then
// what the admin declared, then the seeded vocabulary. Nothing at all is a legitimate
// answer - below the evidence threshold the row reports no guess and the dropdown keeps its
// plain default.
func (p *predictor) predict(it importItem) prediction {
	eligible := make([]library.Category, 0, len(p.cats))
	for _, c := range p.cats {
		if c.Markers.Accepts(it.IsShow) {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 {
		return prediction{}
	}
	votes := map[int64]*vote{}
	cast := func(id int64, weight int, reason string) {
		v := votes[id]
		if v == nil {
			v = &vote{}
			votes[id] = v
		}
		v.score += weight
		if weight > v.weight {
			v.weight, v.reason = weight, reason
		}
	}
	byID := map[int64]library.Category{}
	for _, c := range eligible {
		byID[c.ID] = c
	}

	for _, marker := range itemMarkers(it) {
		if id, count, ok := p.learnedVote(marker); ok {
			if c, live := byID[id]; live {
				cast(id, count, fmt.Sprintf("%s was imported into %s %s",
					asWritten(it.Entry, markerValue(marker)), c.Alias, times(count)))
			}
		}
	}
	names := strings.ToLower(it.Entry + " " + it.Title)
	for _, c := range eligible {
		for _, kw := range c.Markers.Keywords {
			if containsWord(names, kw) {
				cast(c.ID, declaredWeight, fmt.Sprintf("the name mentions %q, a keyword of %s", kw, c.Alias))
			}
		}
		for _, lang := range c.Markers.Languages {
			if containsWord(names, lang) {
				cast(c.ID, declaredWeight, fmt.Sprintf("the name mentions %q, a language of %s", lang, c.Alias))
			}
		}
	}
	p.seedVotes(it, eligible, cast)

	if best := pickVote(votes); best != 0 {
		return prediction{CategoryID: best, Reason: votes[best].reason}
	}
	// A tree with a single category for this kind needs no evidence: there is nowhere else
	// the row could go.
	if len(eligible) == 1 {
		return prediction{CategoryID: eligible[0].ID,
			Reason: "the only category that takes " + kindWord(it.IsShow)}
	}
	return prediction{}
}

// learnedVote reports the category a learned marker points at, and how often it landed
// there. A marker seen once, or spread across categories, points nowhere: that is what keeps
// a pan-Asian uploader silent while a region-pure fansub group speaks.
func (p *predictor) learnedVote(marker string) (int64, int, bool) {
	total := p.totals[marker]
	if total < minSightings {
		return 0, 0, false
	}
	var bestID int64
	best := 0
	for id, n := range p.counts[marker] {
		if n > best || (n == best && id < bestID) {
			bestID, best = id, n
		}
	}
	if float64(best)/float64(total) < minPurity {
		return 0, 0, false
	}
	return bestID, best, true
}

// pickVote returns the winning category, or 0 when nothing voted. A tie goes to the lowest
// id so the same scan always preselects the same way.
func pickVote(votes map[int64]*vote) int64 {
	var bestID int64
	best := 0
	for id, v := range votes {
		if v.score > best || (v.score == best && bestID != 0 && id < bestID) {
			bestID, best = id, v.score
		}
	}
	return bestID
}

// containsWord reports whether a lower-cased name mentions a declared word. The match is on
// whole words, so "kr" does not fire on "workroom".
func containsWord(name, word string) bool {
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(name[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		if !isNameLetter(name, start-1) && !isNameLetter(name, end) {
			return true
		}
		i = start + 1
		if i >= len(name) {
			return false
		}
	}
}

// isNameLetter reports whether the byte at i is a letter or digit, i.e. whether a match
// there would run into a longer word.
func isNameLetter(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := s[i]
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

// markerValue drops the namespace for display: the admin reads "JKCT", not "grp:jkct".
func markerValue(marker string) string {
	if i := strings.IndexByte(marker, ':'); i >= 0 {
		return marker[i+1:]
	}
	return marker
}

// asWritten spells a marker the way the source name does. Markers are stored lower-cased so
// one group is one signal, but the reason reads better as "JKCT" than "jkct".
func asWritten(name, value string) string {
	if i := strings.Index(strings.ToLower(name), value); i >= 0 {
		return name[i : i+len(value)]
	}
	return value
}

func times(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}

func kindWord(isShow bool) string {
	if isShow {
		return "shows"
	}
	return "films"
}

// predictCategories annotates every row of a scan with the category its markers point at,
// in scan order so the sticky rule sees the row before.
func (s *Server) predictCategories(items []importItem) {
	cats, err := library.List(s.dataDir())
	if err != nil || len(cats) == 0 {
		return
	}
	p := newPredictor(cats)
	for i := range items {
		guess := p.predict(items[i])
		items[i].CategoryID, items[i].CategoryReason = guess.CategoryID, guess.Reason
	}
}
