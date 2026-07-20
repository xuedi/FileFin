package recognize

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Marker namespaces. One map holds every learned signal, so a new kind of signal needs no
// schema change - only a new prefix here.
const (
	MarkerGroup    = "grp"    // the release group credited at the end of a name
	MarkerTag      = "tag"    // a bracketed decoration, which is how fansubs sign their work
	MarkerPlatform = "plat"   // the streaming service a rip came from
	MarkerScript   = "script" // the writing system the name uses
)

// platforms are the services a rip can name itself after. Only the region-locked ones are
// worth learning - "WEB-DL" and the global services appear everywhere - but the extraction
// records what it sees and lets the evidence threshold decide.
var platforms = regexp.MustCompile(`(?i)(?:^|` + bnd + `)(iqiyi|youku|mgtv|tencent|wetv|bilibili|` +
	`tving|viu|wavve|kocowa|coupang|netflix|nf|amzn|dsnp|hulu|max|appletv|atvp|viki|crunchyroll|` +
	`funimation|abema|u-next|hotstar)(?:` + bnd + `|$)`)

// reGroupCredit matches the release group at the end of a name, with or without the tracker
// it was uploaded to ("-AppleTor", "-unco@AvistaZ").
var reGroupCredit = regexp.MustCompile(`[-@]([A-Za-z0-9]{2,})(?:@([A-Za-z0-9]+))?$`)

// Markers extracts the learnable signals from a raw source name: who released it, which
// tags it wears, which platform it came from, and what script it is written in. The name is
// the file or folder as it arrived, before the importer renames it - which is why learning
// can only happen at import time.
//
// Every marker is returned namespaced ("grp:JKCT") and lower-cased after the prefix, so the
// same group written in two cases is one signal. The result carries no duplicates.
func Markers(name string) []string {
	base := trimExt(name)
	seen := map[string]bool{}
	var out []string
	add := func(kind, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		m := kind + ":" + strings.ToLower(value)
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}

	for _, tag := range BracketTags(base) {
		if isJunkTag(tag) {
			continue
		}
		add(MarkerTag, tag)
	}
	// The group credit sits at the end of what is left once the bracketed tags are gone, so a
	// trailing "[720p]" cannot be mistaken for one.
	stripped := strings.TrimSpace(reBracketTag.ReplaceAllString(base, " "))
	if m := reGroupCredit.FindStringSubmatch(stripped); m != nil && !isJunkTag(m[1]) {
		add(MarkerGroup, m[1])
	}
	for _, m := range platforms.FindAllStringSubmatch(base, -1) {
		add(MarkerPlatform, m[1])
	}
	if s := scriptOf(base); s != "" {
		add(MarkerScript, s)
	}
	return out
}

// trimExt drops a file extension, but only something that looks like one. Entries arrive as
// folders too, and the last dotted piece of "The.Knockout.2023.1080p-JKCT" is the release
// group - the strongest marker a name carries - not an extension to throw away.
func trimExt(name string) string {
	ext := filepath.Ext(name)
	if ext == "" || len(ext) > 5 {
		return name
	}
	for _, r := range ext[1:] {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return name
		}
	}
	return name[:len(name)-len(ext)]
}

// isJunkTag reports whether a tag is packaging rather than a name: "1080p", "BluRay", a CRC
// hash. Such a tag says nothing about who released the file, so it is never learned.
func isJunkTag(tag string) bool {
	tag = strings.TrimSpace(tag)
	if tag == "" || reJunk.MatchString(tag) || reCRC.MatchString("["+tag+"]") {
		return true
	}
	// A tag that is only digits and separators ("01", "1-4") counts nothing either.
	for _, r := range tag {
		if unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// scriptOf names the writing system a name is decorated with. Han script means the releaser
// is Chinese-speaking, which is a weaker claim than the media being Chinese - the evidence
// threshold is what decides whether that is worth anything. Hangul and kana are recorded the
// same way, though Korean and Japanese releases are almost always romanised.
func scriptOf(name string) string {
	hangul, kana, han := false, false, false
	for _, r := range name {
		switch {
		case unicode.Is(unicode.Hangul, r):
			hangul = true
		case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			kana = true
		case unicode.Is(unicode.Han, r):
			han = true
		}
	}
	// Japanese mixes kanji with kana, so kana outranks the Han characters beside it.
	switch {
	case hangul:
		return "hangul"
	case kana:
		return "kana"
	case han:
		return "han"
	}
	return ""
}
