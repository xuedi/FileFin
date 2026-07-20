package recognize

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// Confidence says how much the admin should trust a recognised media. It is decided by
// which sanity checks passed, not by how much work recognition had to do: a title that was
// only found by falling back to the folder name, but then passes every check, is still
// trustworthy.
type Confidence string

const (
	High   Confidence = "high"
	Medium Confidence = "medium"
	Low    Confidence = "low"
)

// Check names one sanity rule. A failed check is reported to the admin verbatim, so the
// text is the explanation.
type Check string

const (
	CheckFilmFiles      Check = "a film should be one file, or numbered parts"
	CheckTitleAgreement Check = "the files disagree on the title"
	CheckEpisodeUnique  Check = "two files claim the same season and episode"
	CheckTitleJunk      Check = "the title still holds release junk"
	CheckTitleShape     Check = "the title does not read like a title"
	CheckYearRange      Check = "the year is out of range"
	CheckEpisodeRun     Check = "the episode numbers have gaps"
	CheckYearMissing    Check = "no year was found"
	CheckSeasonGuess    Check = "the seasons were guessed from the folder names"
)

// Source names where a candidate title came from. It only breaks ties between candidates
// that score the same.
type Source int

const (
	FromFolder Source = iota // the entry folder an admin named
	FromSubfolder
	FromFile
)

// Candidate is one possible identification of a media, from one source.
type Candidate struct {
	Source Source
	Parsed Parsed
}

// Result is the chosen identification plus the reasons to doubt it.
type Result struct {
	Parsed     Parsed
	Confidence Confidence
	Failed     []Check
}

var (
	reEmptyBrackets = regexp.MustCompile(`[\[(]\s*[\])]`)
	reAbbrevPair    = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)
	reWordLike      = regexp.MustCompile(`(?i)[a-z]*[aeiouy][a-z]*`)
)

// CheckTitle validates one candidate on its own: is this string plausibly a title, and is
// its year plausible. Hard failures make a candidate untrustworthy, soft ones only doubtful.
func CheckTitle(p Parsed) (hard, soft []Check) {
	t := strings.TrimSpace(p.Title)
	switch {
	case t == "":
		hard = append(hard, CheckTitleShape)
	case reAbbrevPair.MatchString(t): // "bifos-babyme", "abd-iyato"
		hard = append(hard, CheckTitleShape)
	case !hasTitleWord(t):
		hard = append(hard, CheckTitleShape)
	}
	if reJunk.MatchString(t) || reCRC.MatchString(t) || reEmptyBrackets.MatchString(t) {
		hard = append(hard, CheckTitleJunk)
	}
	if p.Year != 0 && (p.Year < 1900 || p.Year > time.Now().Year()+2) {
		hard = append(hard, CheckYearRange)
	}
	if p.Year == 0 {
		soft = append(soft, CheckYearMissing)
	}
	return hard, soft
}

// hasTitleWord reports whether a title holds at least one thing that reads like a word: a
// run of three letters with a vowel in it, or any CJK character (which carries a whole word
// per glyph).
func hasTitleWord(t string) bool {
	for _, w := range reWordLike.FindAllString(t, -1) {
		if len(w) >= 3 {
			return true
		}
	}
	for _, r := range t {
		if isCJKWord(string(r)) {
			return true
		}
	}
	return false
}

// CheckFiles validates the set of files that would become one media. It is the check that
// makes the show/film verdict self-validating: a film holding several files that are not
// numbered parts is either misgrouped or is really a show, and either way must not read as
// trustworthy.
func CheckFiles(files []Parsed, isShow bool) (hard, soft []Check) {
	if len(files) == 0 {
		return nil, nil
	}
	if !isShow {
		if len(files) > 1 && !contiguousParts(files) {
			hard = append(hard, CheckFilmFiles)
		}
		return hard, nil
	}
	seen := map[[2]int]bool{}
	perSeason := map[int][]int{}
	for _, f := range files {
		key := [2]int{f.Season, f.Episode}
		if seen[key] {
			hard = append(hard, CheckEpisodeUnique)
			break
		}
		seen[key] = true
	}
	for _, f := range files {
		perSeason[f.Season] = append(perSeason[f.Season], f.Episode)
	}
	for _, eps := range perSeason {
		if !contiguousFrom1(eps) {
			soft = append(soft, CheckEpisodeRun)
			break
		}
	}
	return hard, soft
}

// contiguousParts reports whether every file carries a part number and together they run
// 1..N without a gap ("CD1" + "CD2").
func contiguousParts(files []Parsed) bool {
	parts := make([]int, 0, len(files))
	for _, f := range files {
		if f.Part == 0 {
			return false
		}
		parts = append(parts, f.Part)
	}
	return contiguousFrom1(parts)
}

func contiguousFrom1(nums []int) bool {
	if len(nums) == 0 {
		return true
	}
	sorted := append([]int(nil), nums...)
	sort.Ints(sorted)
	for i, n := range sorted {
		if n != i+1 {
			return false
		}
	}
	return true
}

// TitlesAgree reports whether the files of one media tell the same story about the title.
// Empty titles abstain - a file named after nothing but its episode number says nothing -
// and a title that merely extends another counts as agreement, because that is how a later
// season names itself ("InuYasha", then "InuYasha Kanketsu-hen").
func TitlesAgree(files []Parsed) bool {
	var titles [][]string
	for _, f := range files {
		if t := strings.Fields(strings.ToLower(f.Title)); len(t) > 0 {
			titles = append(titles, t)
		}
	}
	if len(titles) == 0 {
		return true
	}
	for _, t := range titles[1:] {
		if !sharePrefix(titles[0], t) {
			return false
		}
	}
	return true
}

// sharePrefix reports whether the shorter of two word lists is a prefix of the longer.
func sharePrefix(a, b []string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	for i, w := range a {
		if w != b[i] {
			return false
		}
	}
	return true
}

// Best picks the identification to show the admin. Every candidate is scored by the same
// checks and the least-doubtful one wins; equal scores are broken by the order the caller
// passed them, so cands[0] is the source the caller expected to win. The confidence reports
// what the winner failed, plus whether that expected source lost.
func Best(cands []Candidate, files []Parsed, isShow bool) Result {
	if len(cands) == 0 {
		return Result{Confidence: Low, Failed: []Check{CheckTitleShape}}
	}
	fileHard, fileSoft := CheckFiles(files, isShow)
	if !TitlesAgree(files) {
		fileHard = append(fileHard, CheckTitleAgreement)
	}

	type scored struct {
		Candidate
		at         int
		hard, soft []Check
	}
	ranked := make([]scored, 0, len(cands))
	for i, c := range cands {
		h, s := CheckTitle(c.Parsed)
		ranked = append(ranked, scored{c, i, h, s})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if len(ranked[i].hard) != len(ranked[j].hard) {
			return len(ranked[i].hard) < len(ranked[j].hard)
		}
		return len(ranked[i].soft) < len(ranked[j].soft)
	})
	win := ranked[0]

	// Two sources naming the same title in different case are not a disagreement; take the
	// one that capitalises it, whichever source it came from.
	for _, c := range ranked[1:] {
		if strings.EqualFold(c.Parsed.Title, win.Parsed.Title) && capitals(c.Parsed.Title) > capitals(win.Parsed.Title) {
			win.Parsed.Title = c.Parsed.Title
		}
	}
	// A candidate that knows the year completes one that does not.
	if win.Parsed.Year == 0 {
		for _, c := range ranked[1:] {
			if c.Parsed.Year != 0 {
				win.Parsed.Year = c.Parsed.Year
				win.soft = dropCheck(win.soft, CheckYearMissing)
				break
			}
		}
	}

	res := Result{Parsed: win.Parsed}
	res.Failed = append(append([]Check(nil), fileHard...), win.hard...)
	soft := append(append([]Check(nil), fileSoft...), win.soft...)
	switch {
	case len(res.Failed) > 0:
		res.Confidence = Low
	case len(soft) > 0 || win.at != 0:
		res.Confidence = Medium
	default:
		res.Confidence = High
	}
	res.Failed = append(res.Failed, soft...)
	return res
}

func dropCheck(list []Check, drop Check) []Check {
	out := list[:0]
	for _, c := range list {
		if c != drop {
			out = append(out, c)
		}
	}
	return out
}

func capitals(s string) int {
	n := 0
	for _, w := range strings.Fields(s) {
		if r := []rune(w); len(r) > 0 && r[0] >= 'A' && r[0] <= 'Z' {
			n++
		}
	}
	return n
}
