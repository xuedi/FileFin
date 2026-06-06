package importer

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Episode-recognition patterns, in the priority order parseSeasonEpisode applies
// them. Explicit markers are parsed unconditionally; the bare trailing-number form
// (reBareEp) is gated on isShow because it would otherwise eat a movie's title
// number ("Blade 2").
var (
	reSeasonEp = regexp.MustCompile(`(?i)\bseason\s*(\d{1,2})\s*episode\s*(\d{1,3})\b`)
	reEpWord   = regexp.MustCompile(`(?i)\bepisode\s*(\d{1,3})\b`)
	reEpE      = regexp.MustCompile(`(?i)(?:^|[ ._-])E(\d{1,3})(?:[ ._-]|$)`)
	reBareEp   = regexp.MustCompile(`(?:^|[ _])-?\s*(\d{1,3})\s*$`)
)

// parseSeasonEpisode reads a season/episode pair from a name's base (no extension),
// trying the explicit schemes first and falling back to a bare trailing number only
// for shows. A returned season of 0 with a non-zero episode means "season 1 implied"
// is the caller's job; here an episode-only scheme yields season 1 directly.
func parseSeasonEpisode(base string, isShow bool) (int, int) {
	if m := reEpSE.FindStringSubmatch(base); m != nil {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e
	}
	if m := reEpX.FindStringSubmatch(base); m != nil {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e
	}
	if m := reSeasonEp.FindStringSubmatch(base); m != nil {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e
	}
	// "Episode 01 & 02" / "Episode 5": take the first number, season 1.
	if m := reEpWord.FindStringSubmatch(base); m != nil {
		e, _ := strconv.Atoi(m[1])
		return 1, e
	}
	// "E16" / "- E50" / "E01-E02" (first number). A trailing date like
	// "...E16.121004" is not eaten because the date is not preceded by an E.
	if m := reEpE.FindStringSubmatch(base); m != nil {
		e, _ := strconv.Atoi(m[1])
		return 1, e
	}
	if isShow {
		if e, ok := bareEpisode(base); ok {
			return 1, e
		}
	}
	return 0, 0
}

// bareEpisode reads a trailing absolute episode number ("Beck 04", "Amachan - 090")
// when the name carries no explicit marker. It refuses part markers (CD1, Disc 2)
// and four-digit years (reBareEp caps at three digits) so those are not mistaken
// for episodes.
func bareEpisode(base string) (int, bool) {
	if _, _, ok := detectPart(base); ok {
		return 0, false
	}
	m := reBareEp.FindStringSubmatch(base)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	if n == 0 {
		return 0, false
	}
	return n, true
}

// rePartMarker matches a trailing multi-part marker: CD/DVD/Disc/Disk/Part/pt
// followed by a number (any of " ", ".", "_", "-" between or none), or a
// parenthesised/bracketed number, optionally "(1 of 2)" / "(1/2)".
var rePartMarker = regexp.MustCompile(`(?i)[ ._-]*(?:(?:cd|dvd|disc|disk|part|pt)[ ._-]*(\d{1,3})|\((\d{1,3})(?:\s*(?:of|/)\s*\d{1,3})?\)|\[(\d{1,3})\])\s*$`)

// detectPart finds a trailing multi-part marker in a media file's base name (no
// extension). It returns the base with the marker stripped (the key files of one
// item share), the part number, and whether a marker was found. Bare trailing
// numbers are deliberately not markers here: distinguishing "Movie 2" (part) from
// "Blade 2" (title) needs sibling context the caller holds, not a single name.
func detectPart(base string) (string, int, bool) {
	loc := rePartMarker.FindStringSubmatchIndex(base)
	if loc == nil {
		return base, 0, false
	}
	num := 0
	for _, g := range [][2]int{{2, 3}, {4, 5}, {6, 7}} {
		if loc[g[0]] >= 0 {
			num, _ = strconv.Atoi(base[loc[g[0]]:loc[g[1]]])
			break
		}
	}
	stripped := strings.Trim(base[:loc[0]], " ._-")
	return stripped, num, true
}

// DetectPart reports a trailing multi-part marker in a base name (no extension):
// the base with the marker stripped (the grouping key files of one item share),
// the part number, and whether a marker was found. It is the exported form of the
// detection the importer uses internally, for callers that group loose files.
func DetectPart(base string) (string, int, bool) { return detectPart(base) }

// sortFiles orders a media item's files so multi-part numbering is stable: by
// season, then episode, then the part marker's number (markers ahead of unmarked
// files), then lexically by path. It sorts in place.
func sortFiles(files []SourceFile) {
	sort.SliceStable(files, func(i, j int) bool {
		a, b := files[i], files[j]
		if a.Season != b.Season {
			return a.Season < b.Season
		}
		if a.Episode != b.Episode {
			return a.Episode < b.Episode
		}
		an, aok := partNum(a.Path)
		bn, bok := partNum(b.Path)
		if aok && bok {
			if an != bn {
				return an < bn
			}
			return a.Path < b.Path
		}
		if aok != bok {
			return aok
		}
		return a.Path < b.Path
	})
}

func partNum(path string) (int, bool) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	_, n, ok := detectPart(base)
	return n, ok
}
