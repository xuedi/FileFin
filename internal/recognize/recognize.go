// Package recognize identifies a media file from its name: the title, the year, and
// (when present) a season/episode pair. It is a best-effort parser - the admin can
// correct title and year in the assessment table before importing.
package recognize

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Parsed is the best-effort identification of a media file from its name. For a
// movie Season and Episode are 0 and IsShow is false.
type Parsed struct {
	Title   string
	Year    int
	Season  int
	Episode int
	Ext     string
	IsShow  bool
}

var (
	reYearParen = regexp.MustCompile(`\((19\d{2}|20\d{2})\)`)
	reYearBare  = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	reEpX       = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
	reEpSE      = regexp.MustCompile(`(?i)\bS(\d{1,2})E(\d{1,3})\b`)
	reSeasonEp  = regexp.MustCompile(`(?i)\bseason\s*(\d{1,2})\s*episode\s*(\d{1,3})\b`)
	reEpWord    = regexp.MustCompile(`(?i)\bepisode\s*(\d{1,3})\b`)
	reEpE       = regexp.MustCompile(`(?i)(?:^|[ ._-])E(\d{1,3})(?:[ ._-]|$)`)
	reBareEp    = regexp.MustCompile(`(?:^|[ _])-?\s*(\d{1,3})\s*$`)
	reJunk      = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|4k|x264|x265|h\.?264|h\.?265|hevc|bluray|blu-ray|brrip|bdrip|web-?dl|webrip|hdtv|dvdrip|aac|ac3|dts|hdr|remux|proper|repack|extended|unrated|imax)\b`)
	reSpaces    = regexp.MustCompile(`\s+`)
	// reSeasonDir matches a season subfolder ("Season 1", "Series 02", "S03"); the
	// captured group is the season number. reSpecialsDir matches a specials folder,
	// which is season 0.
	reSeasonDir   = regexp.MustCompile(`(?i)^(?:season|series|s)\s*0*(\d{1,3})$`)
	reSpecialsDir = regexp.MustCompile(`(?i)^specials?$`)
)

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
	nameHasExplicitSeason := reEpSE.MatchString(base) || reEpX.MatchString(base) || reSeasonEp.MatchString(base)
	if haveSeasonDir && !nameHasExplicitSeason {
		p.Season = seasonDir
	}

	// When the file name carries no usable title (a bare episode number, now
	// trimmed to empty), borrow it from the nearest non-season ancestor folder.
	if strings.TrimSpace(p.Title) == "" {
		for i := len(comps) - 1; i >= 0; i-- {
			if i == seasonIdx || reSeasonDir.MatchString(comps[i]) || reSpecialsDir.MatchString(comps[i]) {
				continue
			}
			fp := ParseName(comps[i], false)
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

// ParseName extracts title, year, and (if present) season/episode from a file name.
// It handles prefix-year names ("(1962) Lawrence of Arabia.avi") and suffix-year
// release names ("The.Matrix.1999.1080p.mkv"). isShow enables the looser, ambiguous
// episode schemes (bare trailing numbers like "Beck 04") that would otherwise be
// mistaken for part of a movie title; explicit markers (SxE, NxNN, "Season N
// Episode M", E-markers) are always recognised. For v1 movies callers pass false.
func ParseName(name string, isShow bool) Parsed {
	base := name[:len(name)-len(filepath.Ext(name))]
	p := Parsed{Ext: strings.ToLower(filepath.Ext(name))}
	p.Season, p.Episode = parseSeasonEpisode(base, isShow)

	var titlePart string
	switch {
	case reYearParen.FindStringSubmatchIndex(base) != nil:
		loc := reYearParen.FindStringSubmatchIndex(base)
		p.Year, _ = strconv.Atoi(base[loc[2]:loc[3]])
		if loc[0] <= 2 { // year at the front: prefix style, title follows
			titlePart = base[loc[1]:]
		} else {
			titlePart = base[:loc[0]]
		}
	case reYearBare.FindStringIndex(base) != nil:
		loc := reYearBare.FindStringIndex(base)
		p.Year, _ = strconv.Atoi(base[loc[0]:loc[1]])
		if loc[0] == 0 {
			titlePart = base[loc[1]:]
		} else {
			titlePart = base[:loc[0]]
		}
	default:
		titlePart = base
	}

	p.Title = cleanTitle(titlePart)
	if isShow {
		// Drop a trailing bare episode number ("Reply 1988 - 17" -> "Reply 1988")
		// only for shows, where it was just parsed as the episode.
		p.Title = strings.Trim(reBareEp.ReplaceAllString(p.Title, ""), " -")
	}
	return p
}

// parseSeasonEpisode reads a season/episode pair from a name's base (no extension),
// trying explicit schemes first and falling back to a bare trailing number only for
// shows. An episode-only scheme yields season 1.
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
	if m := reEpWord.FindStringSubmatch(base); m != nil {
		e, _ := strconv.Atoi(m[1])
		return 1, e
	}
	if m := reEpE.FindStringSubmatch(base); m != nil {
		e, _ := strconv.Atoi(m[1])
		return 1, e
	}
	if isShow {
		if m := reBareEp.FindStringSubmatch(base); m != nil {
			if e, _ := strconv.Atoi(m[1]); e > 0 {
				return 1, e
			}
		}
	}
	return 0, 0
}

func cleanTitle(s string) string {
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	s = reEpX.ReplaceAllString(s, " ")
	s = reEpSE.ReplaceAllString(s, " ")
	s = reSeasonEp.ReplaceAllString(s, " ")
	s = reEpWord.ReplaceAllString(s, " ")
	s = reEpE.ReplaceAllString(s, " ")
	s = reJunk.ReplaceAllString(s, " ")
	s = reSpaces.ReplaceAllString(s, " ")
	return strings.Trim(s, " -")
}
