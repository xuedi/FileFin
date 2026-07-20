// Package recognize identifies a media file from its name: the title, the year, and
// (when present) a season/episode pair. It is a best-effort parser - the admin can
// correct title and year in the assessment table before importing.
//
// The parser works by cutting: it finds the first structural marker in a name - a year, a
// season/episode marker, a packaging token, a release-group credit - and keeps only what
// stands before it. Everything after a marker belongs to whoever released the file, not to
// the media, which is why an episode title never reaches the media title.
package recognize

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Parsed is the best-effort identification of a media file from its name. For a
// movie Season and Episode are 0 and IsShow is false. Part is non-zero only for a
// media split over several files ("CD1", "part2").
type Parsed struct {
	Title   string
	Year    int
	Season  int
	Episode int
	Part    int
	Ext     string
	IsShow  bool
}

var (
	// Season/episode schemes, most specific first. Every one of them sets IsShow.
	reEpSE     = regexp.MustCompile(`(?i)(?:^|` + sep + `|-)s(\d{1,2})` + sep + `?e(\d{1,3})(?:v\d+)?(?:-e?\d{1,3})?(?:` + bnd + `|$)`)
	reEpX      = regexp.MustCompile(`(?i)(?:^|` + bnd + `)(\d{1,2})x(\d{1,3})(?:` + bnd + `|$)`)
	reSeasonEp = regexp.MustCompile(`(?i)season` + sep + `*(\d{1,2})` + sep + `*episode` + sep + `*(\d{1,3})`)
	reSeasonNo = regexp.MustCompile(`(?i)(?:^|` + sep + `|-)(?:season|series|s)` + sep + `*(\d{1,2})(?:` + bnd + `|$)`)
	reEpWord   = regexp.MustCompile(`(?i)(?:^|` + bnd + `)(?:episode|ep)` + sep + `*\.?` + sep + `*(\d{1,3})(?:v\d+)?(?:` + bnd + `|$)`)
	reEpE      = regexp.MustCompile(`(?i)(?:^|` + sep + `|-)e(\d{1,3})(?:v\d+)?(?:-e?\d{1,3})?(?:` + bnd + `|$)`)
	// reEpDashTag matches the fansub scheme "Title - 01 [CRC]" / "Title_-_001_(DVD_480p)":
	// a bare number introduced by a dash and followed by a bracketed tag. The bracket is what
	// makes it unambiguous, so unlike a bare trailing number it needs no show context.
	reEpDashTag = regexp.MustCompile(`(?:^|` + sep + `)-` + sep + `*(\d{1,3})` + sep + `*[\[(]`)
	// reBareEp is the last resort: a trailing number on a name already known to be a show.
	reBareEp = regexp.MustCompile(`(?:^|[ _])-?\s*(\d{1,3})\s*$`)
	// reMovieOrdinal marks "Movie - 01", "Film 2": the number counts films in a series, not
	// episodes, and must not be read as one.
	reMovieOrdinal = regexp.MustCompile(`(?i)(?:^|` + bnd + `)(?:movie|film)s?(?:` + bnd + `)*(\d{1,3})(?:` + bnd + `|$)`)

	rePart = regexp.MustCompile(`(?i)(?:^|` + bnd + `)(?:cd|part|pt|disc|disk)` + sep + `*(\d{1,2})(?:` + bnd + `|$)`)

	reYear       = regexp.MustCompile(`(?:^|` + bnd + `)((?:19|20)\d{2})(?:` + bnd + `|$)`)
	reYearPrefix = regexp.MustCompile(`^\s*(?:\(((?:19|20)\d{2})\)|\[((?:19|20)\d{2})\]|((?:19|20)\d{2})(?:` + bnd + `))` + bnd + `*`)

	reSpaces     = regexp.MustCompile(`\s+`)
	reBracketTag = regexp.MustCompile(`[\[(]([^\[\]()]*)[\])]`)

	// reSeasonDir matches a season subfolder ("Season 1", "Series 02", "S03"); the
	// captured group is the season number. reSpecialsDir matches a specials folder,
	// which is season 0.
	reSeasonDir   = regexp.MustCompile(`(?i)^(?:season|series|s)\s*0*(\d{1,3})$`)
	reSpecialsDir = regexp.MustCompile(`(?i)^specials?$`)
)

// IsSeasonDir reports whether a directory name is a season or specials folder
// ("Season 1", "Series 02", "S03", "Specials"). It is used to detect a TV-show layout.
func IsSeasonDir(name string) bool {
	return reSeasonDir.MatchString(name) || reSpecialsDir.MatchString(name)
}

// FromPath identifies a media file from its path relative to the import root,
// folding in directory context: a "Season N"/"S0N"/"Specials" ancestor folder sets
// the season and marks the item as a show (so a bare trailing episode number is
// honoured), and a non-season ancestor supplies the title/year when the file name
// itself is just an episode number. An explicit season/episode marker in the file
// name always wins over the folder. relPath uses the OS path separator.
func FromPath(relPath string) Parsed {
	comps := splitDirs(relPath)
	file := filepath.Base(relPath)

	seasonDir, seasonIdx, haveSeasonDir := 0, -1, false
	for i, c := range comps {
		if m := reSeasonDir.FindStringSubmatch(c); m != nil {
			seasonDir, _ = strconv.Atoi(m[1])
			seasonIdx, haveSeasonDir = i, true
		} else if reSpecialsDir.MatchString(c) {
			seasonDir, seasonIdx, haveSeasonDir = 0, i, true
		}
	}

	p := ParseName(file, haveSeasonDir)
	if p.Episode > 0 || haveSeasonDir {
		p.IsShow = true
	}

	// A season folder overrides the season only when the name did not state one
	// explicitly: episode-only schemes imply season 1, which "Season 2/05.mkv"
	// should correct, but an explicit "S02E05" in the name is authoritative.
	base := file[:len(file)-len(filepath.Ext(file))]
	if haveSeasonDir && !HasExplicitSeason(base) {
		p.Season = seasonDir
	}

	// When the file name carries no usable title (a bare episode number, now
	// trimmed to empty), borrow it from the nearest non-season ancestor folder.
	if strings.TrimSpace(p.Title) == "" {
		for i := len(comps) - 1; i >= 0; i-- {
			if i == seasonIdx || reSeasonDir.MatchString(comps[i]) || reSpecialsDir.MatchString(comps[i]) {
				continue
			}
			fp := ParseFolder(comps[i])
			if fp.Title != "" {
				p.Title = fp.Title
				if p.Year == 0 {
					p.Year = fp.Year
				}
				break
			}
		}
	}
	return p
}

// splitDirs returns the directory components of a relative path (the file name
// dropped), skipping empties and "." segments, separator-agnostic.
func splitDirs(relPath string) []string {
	dir := filepath.ToSlash(filepath.Dir(relPath))
	var out []string
	for _, c := range strings.Split(dir, "/") {
		if c != "" && c != "." {
			out = append(out, c)
		}
	}
	return out
}

// ParseFolder identifies a media from a directory name. A folder name has no extension,
// so nothing may be stripped from its end - "beijing.rocks" is a title, not a name with a
// ".rocks" extension.
func ParseFolder(name string) Parsed { return parse(name, "", false) }

// ParseName extracts title, year, season/episode and part from a file name.
// The title is whatever stands before the first structural marker; isShow enables the one
// ambiguous scheme (a bare trailing number, "Beck 04") that would otherwise swallow the
// number of a movie title like "Blade 2". Every other scheme is unambiguous enough to be
// recognised without context.
func ParseName(name string, isShow bool) Parsed {
	ext := filepath.Ext(name)
	return parse(name[:len(name)-len(ext)], ext, isShow)
}

func parse(base, ext string, isShow bool) Parsed {
	p := Parsed{Ext: strings.ToLower(ext)}

	// A leading year is a prefix style ("(1962) Lawrence of Arabia", "[1983]project.a"):
	// the title follows it rather than preceding it.
	if loc := reYearPrefix.FindStringSubmatchIndex(base); loc != nil && loc[1] < len(base) {
		p.Year = firstGroupInt(base, loc)
		base = base[loc[1]:]
	}

	// A bracketed tag at the head is the releaser's name, never the title, and may itself
	// hold a packaging token ("(Hi10)"). Dropping it first keeps that token from cutting
	// the title away to nothing.
	base = dropHeadTags(base)

	p.Season, p.Episode, p.IsShow = parseSeasonEpisode(base, isShow)
	if m := rePart.FindStringSubmatch(base); m != nil {
		p.Part, _ = strconv.Atoi(m[1])
	}
	if p.Year == 0 {
		if m := reYear.FindStringSubmatch(base); m != nil {
			p.Year, _ = strconv.Atoi(m[1])
		}
	}
	p.Title = cleanTitle(base[:cutAt(base, isShow)])
	return p
}

// cutAt returns the offset of the first structural marker in a raw name - the point where
// the title ends. It is len(base) when the name is all title.
func cutAt(base string, isShow bool) int {
	cut := len(base)
	at := func(re *regexp.Regexp) {
		if loc := re.FindStringIndex(base); loc != nil && loc[0] < cut {
			cut = loc[0]
		}
	}
	at(reYear)
	at(reEpSE)
	at(reEpX)
	at(reSeasonEp)
	at(reSeasonNo)
	at(reEpWord)
	at(reEpE)
	at(reEpDashTag)
	at(rePart)
	at(reJunk)
	at(reCRC)
	if isShow {
		at(reBareEp)
	}
	// The match may start on its own boundary character; drop any separator, dash or
	// opening bracket the title would otherwise end on.
	for cut > 0 && strings.ContainsRune(" ._-[(", rune(base[cut-1])) {
		cut--
	}
	return cut
}

// parseSeasonEpisode reads a season/episode pair from a raw name, trying unambiguous
// schemes first and the bare trailing number only for names already known to be a show.
// An episode-only scheme implies season 1. The third result reports whether any marker
// matched at all - that is the show/film discriminator.
func parseSeasonEpisode(base string, isShow bool) (int, int, bool) {
	if m := reEpSE.FindStringSubmatch(base); m != nil {
		return atoi(m[1]), atoi(m[2]), true
	}
	if m := reEpX.FindStringSubmatch(base); m != nil {
		return atoi(m[1]), atoi(m[2]), true
	}
	if m := reSeasonEp.FindStringSubmatch(base); m != nil {
		return atoi(m[1]), atoi(m[2]), true
	}
	// "Movie - 01" counts films in a series; the number is an ordinal, never an episode.
	movieOrdinal := reMovieOrdinal.MatchString(base)
	if m := reEpWord.FindStringSubmatch(base); m != nil && !movieOrdinal {
		return 1, atoi(m[1]), true
	}
	if m := reEpE.FindStringSubmatch(base); m != nil && !movieOrdinal {
		return 1, atoi(m[1]), true
	}
	if m := reEpDashTag.FindStringSubmatch(base); m != nil && !movieOrdinal {
		return 1, atoi(m[1]), true
	}
	// A season with no episode ("Deaths.Game.S01.1080p") still marks a show.
	if m := reSeasonNo.FindStringSubmatch(base); m != nil {
		return atoi(m[1]), 0, true
	}
	if isShow && !movieOrdinal {
		if m := reBareEp.FindStringSubmatch(base); m != nil {
			if e := atoi(m[1]); e > 0 {
				return 1, e, true
			}
		}
	}
	return 0, 0, false
}

// HasExplicitSeason reports whether a name states its season itself, in which case neither a
// season folder nor a guess from sibling folders may override it.
func HasExplicitSeason(name string) bool {
	base := name[:len(name)-len(filepath.Ext(name))]
	return reEpSE.MatchString(base) || reEpX.MatchString(base) ||
		reSeasonEp.MatchString(base) || reSeasonNo.MatchString(base)
}

// IsMovieOrdinal reports whether a name counts films in a series ("InuYasha Movie - 02",
// "InuYasha Movies 1-4"). Such a number is an ordinal, never an episode.
func IsMovieOrdinal(name string) bool {
	return reMovieOrdinal.MatchString(name)
}

// BracketTags returns the bracketed decorations of a name ("[LostYears]", "(Hi10)"), which
// is how a release names the group that made it.
func BracketTags(name string) []string {
	var out []string
	for _, m := range reBracketTag.FindAllStringSubmatch(name, -1) {
		if t := strings.TrimSpace(m[1]); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// dropHeadTags removes bracketed decorations from the front of a name, as long as
// something is left over - a name that is nothing but a tag keeps it.
func dropHeadTags(s string) string {
	for {
		rest := reEdgeTagHead.ReplaceAllString(s, "")
		if rest == s || strings.TrimSpace(rest) == "" {
			return s
		}
		s = rest
	}
}

// cleanTitle turns the raw head of a name into a title: bracketed decorations dropped,
// separators normalised to spaces, a release-group credit and a CJK title prefix removed.
func cleanTitle(s string) string {
	s = reCRC.ReplaceAllString(s, "")
	for {
		trimmed := reEdgeTagHead.ReplaceAllString(s, "")
		trimmed = reEdgeTagTail.ReplaceAllString(trimmed, "")
		if trimmed == s {
			break
		}
		s = trimmed
	}
	s = dropUnbalancedTail(s)
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	s = reSpaces.ReplaceAllString(s, " ")
	s = strings.Trim(s, " -")
	s = dropGroupSuffix(s)
	s = dropCJKPrefix(s)
	return strings.Trim(s, " -")
}

// dropUnbalancedTail cuts a title at a bracket that was opened but never closed, which is
// what a cut inside a bracketed tag leaves behind ("InuYasha Movie - 01 Title (BD").
func dropUnbalancedTail(s string) string {
	depth, open := 0, -1
	for i, r := range s {
		switch r {
		case '[', '(':
			if depth == 0 {
				open = i
			}
			depth++
		case ']', ')':
			if depth > 0 {
				depth--
			}
		}
	}
	if depth > 0 && open >= 0 {
		return s[:open]
	}
	return s
}

// dropGroupSuffix removes a release-group credit left at the end of a title
// ("Jirisan -AppleTor"). A hyphenated word is only a credit when a space precedes the
// hyphen, so "Three-Body" and "Otome-domo yo" survive.
func dropGroupSuffix(s string) string {
	if i := strings.LastIndex(s, " -"); i > 0 {
		if tail := s[i+1:]; reGroupSuffix.MatchString(tail) && !strings.Contains(tail, " ") {
			return strings.TrimRight(s[:i], " ")
		}
	}
	if i := strings.LastIndex(s, "@"); i > 0 && !strings.Contains(s[i:], " ") {
		return strings.TrimRight(s[:i], " -")
	}
	return s
}

// dropCJKPrefix removes the original-language title many Asian releases put in front of
// the romanised one ("狂飙.The.Knockout" -> "The Knockout"), so the metadata lookup has a
// title it can match. The full name stays visible in the import table.
func dropCJKPrefix(s string) string {
	words := strings.Split(s, " ")
	if len(words) < 2 || !isCJKWord(words[0]) {
		return s
	}
	for _, w := range words[1:] {
		if hasLatinLetter(w) {
			return strings.Join(words[1:], " ")
		}
	}
	return s
}

func isCJKWord(w string) bool {
	cjk := false
	for _, r := range w {
		switch {
		case unicode.Is(unicode.Han, r), unicode.Is(unicode.Hiragana, r),
			unicode.Is(unicode.Katakana, r), unicode.Is(unicode.Hangul, r):
			cjk = true
		case unicode.IsDigit(r):
		default:
			return false
		}
	}
	return cjk
}

func hasLatinLetter(w string) bool {
	for _, r := range w {
		if r < unicode.MaxASCII && unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// firstGroupInt reads the first non-empty capture of a submatch index set.
func firstGroupInt(s string, loc []int) int {
	for i := 2; i+1 < len(loc); i += 2 {
		if loc[i] >= 0 {
			return atoi(s[loc[i]:loc[i+1]])
		}
	}
	return 0
}
