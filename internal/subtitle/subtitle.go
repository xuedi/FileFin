// Package subtitle holds subtitle helpers: sidecar recognition for the media folder
// scan, language labelling, and SRT->WebVTT conversion for the server. It depends on
// nothing else in the project.
package subtitle

import (
	"bufio"
	"io"
	"path/filepath"
	"strings"
	"unicode"
)

// langLabels are display names for the two-letter tags FileFin stores. Unknown tags
// fall back to the tag itself (see Label).
var langLabels = map[string]string{
	"en": "English", "de": "German", "fr": "French", "es": "Spanish",
	"it": "Italian", "ja": "Japanese", "zh": "Chinese", "ko": "Korean",
	"ru": "Russian", "pt": "Portuguese", "nl": "Dutch",
}

// subQualifiers are trailing infix segments that follow the language tag rather
// than being one ("Movie.en.forced.srt" -> language "en").
var subQualifiers = map[string]bool{
	"forced": true, "sdh": true, "cc": true, "hi": true, "default": true,
}

// langAliases maps three-letter and full-word language tags to the two-letter tags
// FileFin stores, so a sidecar's "eng"/"english" infix normalises to "en".
var langAliases = map[string]string{
	"eng": "en", "english": "en",
	"ger": "de", "deu": "de", "german": "de",
	"fre": "fr", "fra": "fr", "french": "fr",
	"spa": "es", "esp": "es", "spanish": "es",
	"ita": "it", "italian": "it",
	"jpn": "ja", "jap": "ja", "japanese": "ja",
	"chi": "zh", "zho": "zh", "chinese": "zh",
	"kor": "ko", "korean": "ko",
	"rus": "ru", "russian": "ru",
	"por": "pt", "portuguese": "pt",
	"dut": "nl", "nld": "nl", "dutch": "nl",
}

// NormalizeLang lowercases and trims raw, maps a known alias to its two-letter
// form, and falls back to def when raw is empty.
func NormalizeLang(raw, def string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return def
	}
	if m, ok := langAliases[s]; ok {
		return m
	}
	return s
}

// Label returns a human display name for a language tag, falling back to the tag
// itself (uppercased) when it is unknown.
func Label(lang string) string {
	if l, ok := langLabels[strings.ToLower(lang)]; ok {
		return l
	}
	return strings.ToUpper(lang)
}

// LangFromName extracts a language tag from a subtitle file name of the form
// "<base>.<lang>[.<qualifier>...].<ext>" (e.g. "Movie.en.forced.srt" -> "en").
// It returns "" when there is no infix or the infix is not a plausible tag.
func LangFromName(name string) string {
	name = filepath.Base(name)
	name = strings.TrimSuffix(name, filepath.Ext(name)) // drop the subtitle extension
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return ""
	}
	for i := len(parts) - 1; i >= 1; i-- {
		tok := strings.ToLower(parts[i])
		if subQualifiers[tok] {
			continue
		}
		if isLangToken(tok) {
			return parts[i]
		}
		return ""
	}
	return ""
}

func isLangToken(s string) bool {
	if l := len(s); l < 2 || l > 3 {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// Match reports whether subName is an ".srt" sidecar belonging to the video whose
// base name (extension stripped) is videoBase, returning the raw language tag ("" when
// the file carries no language infix). Only ".srt" is recognised for playback.
func Match(videoBase, subName string) (lang string, ok bool) {
	if strings.ToLower(filepath.Ext(subName)) != ".srt" {
		return "", false
	}
	stem := strings.TrimSuffix(subName, filepath.Ext(subName))
	switch {
	case stem == videoBase:
		return "", true
	case strings.HasPrefix(stem, videoBase+"."):
		if l := LangFromName(subName); l != "" {
			return l, true
		}
	}
	return "", false
}

// ToVTT streams a WebVTT rendering of the SRT read from r to w. SRT and VTT differ
// only in the header, the timestamp decimal separator, and the optional numeric cue
// index, so the transform is textual: write the header, drop a leading UTF-8 BOM and
// the numeric cue-index lines, and rewrite "HH:MM:SS,mmm" to "HH:MM:SS.mmm" on cue
// timing lines. Unrecognised lines pass through unchanged, so a malformed file still
// produces output rather than failing.
func ToVTT(w io.Writer, r io.Reader) error {
	bw := bufio.NewWriter(w)
	if _, err := bw.WriteString("WEBVTT\n\n"); err != nil {
		return err
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // tolerate long cue lines
	first := true
	pendingNum := ""    // a numeric line held back until we know if it is a cue index
	hasPending := false // ...it is dropped only when the next line is a cue timing line
	for sc.Scan() {
		line := sc.Text()
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		timing := strings.Contains(line, "-->")
		if hasPending {
			if !timing { // the held number was real text, not an index: emit it
				if _, err := bw.WriteString(pendingNum + "\n"); err != nil {
					return err
				}
			}
			hasPending = false
		}
		if isNumericLine(line) {
			pendingNum, hasPending = line, true
			continue
		}
		if timing {
			line = strings.ReplaceAll(line, ",", ".")
		}
		if _, err := bw.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	if hasPending { // trailing numeric line with nothing after it
		if _, err := bw.WriteString(pendingNum + "\n"); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return bw.Flush()
}

func isNumericLine(line string) bool {
	s := strings.TrimSpace(line)
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// Sidecars scans a media folder for ".srt" files belonging to the video whose base
// name (extension stripped) is videoBase, returning them sorted by file name. Each
// result carries its detected language tag ("" when none) and a display label. Used to
// surface external subtitle tracks for playback; best-effort, so a read error yields
// no sidecars.
func Sidecars(entries []string, videoBase string) []Sidecar {
	var out []Sidecar
	for _, name := range entries {
		if lang, ok := Match(videoBase, name); ok {
			out = append(out, Sidecar{Name: name, Lang: lang, Label: Label(lang)})
		}
	}
	return out
}

// Sidecar is one discovered external subtitle file for a media file.
type Sidecar struct {
	Name  string // file name within the media folder
	Lang  string // two-letter language tag, "" when none
	Label string // display label
}
