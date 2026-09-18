// Package subtitle holds subtitle helpers: sidecar recognition for the media folder
// scan, language labelling, and SRT->WebVTT conversion for the server. It depends on
// nothing else in the project.
package subtitle

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// KnownLang reports whether raw names a language FileFin recognises - a two-letter tag
// it has a label for, or an alias it can normalise. Used to decide whether a free-text
// tag (an embedded track's title, say) really names a language rather than a variant
// like "Full" or "SDH".
func KnownLang(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return false
	}
	if _, ok := langAliases[s]; ok {
		return true
	}
	_, ok := langLabels[s]
	return ok
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

// sniffLen is how much of a subtitle file is inspected to tell its real format.
const sniffLen = 4096

// Qualifiers returns the qualifier infixes of a subtitle file name ("forced", "sdh", ...),
// in file-name order, e.g. "Movie.en.forced.srt" -> ["forced"].
func Qualifiers(name string) []string {
	name = strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	var out []string
	for _, tok := range strings.Split(name, ".")[1:] {
		if subQualifiers[strings.ToLower(tok)] {
			out = append(out, strings.ToLower(tok))
		}
	}
	return out
}

// Format names the real format of the subtitle file at path, "ASS" or "SRT", judged by
// content like the renderer does; "" when the file cannot be read.
func Format(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, sniffLen)
	n, _ := io.ReadFull(f, head)
	if IsASS(head[:n]) {
		return "ASS"
	}
	return "SRT"
}

// IsASS reports whether head (the opening bytes of a subtitle file) is an ASS/SSA script
// rather than SRT. Files holding ASS content under an ".srt" name are common in the wild,
// so the format is decided by content and never by the extension.
func IsASS(head []byte) bool {
	s := strings.ToLower(string(bytes.TrimPrefix(head, []byte("\ufeff"))))
	for _, marker := range []string{"[script info]", "[v4 styles]", "[v4+ styles]", "[events]", "\ndialogue:"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return strings.HasPrefix(s, "dialogue:")
}

// ToVTT streams a WebVTT rendering of the subtitle read from r to w. The source format is
// sniffed from its opening bytes, so an ASS/SSA script is rendered as cues even when it
// arrived under an ".srt" name; anything else is treated as SRT.
func ToVTT(w io.Writer, r io.Reader) error {
	br := bufio.NewReaderSize(r, 2*sniffLen)
	head, _ := br.Peek(sniffLen) // a short file yields a short head, which sniffs fine
	if IsASS(head) {
		return assToVTT(w, br)
	}
	return srtToVTT(w, br)
}

// srtToVTT streams a WebVTT rendering of SRT. SRT and VTT differ only in the header, the
// timestamp decimal separator, and the optional numeric cue index, so the transform is
// textual: write the header, drop a leading UTF-8 BOM and the numeric cue-index lines, and
// rewrite "HH:MM:SS,mmm" to "HH:MM:SS.mmm" on cue timing lines. Unrecognised lines pass
// through unchanged, so a malformed file still produces output rather than failing.
func srtToVTT(w io.Writer, r io.Reader) error {
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

// assCue is one parsed "[Events]" Dialogue line. Cues are collected rather than streamed
// because WebVTT requires them in start order and ASS does not guarantee it.
type assCue struct {
	start, end int // centiseconds
	text       string
}

// assOverride matches an ASS "{\...}" style/positioning override block. WebVTT has no
// equivalent, so the blocks are dropped and the text around them kept.
var assOverride = regexp.MustCompile(`\{[^}]*\}`)

// assToVTT renders an ASS/SSA script as WebVTT. Only the "[Events]" section carries timed
// text: its "Format:" line names the field order, and each "Dialogue:" line is one cue.
// Everything else (script info, styles, fonts, "Comment:" lines) has no WebVTT counterpart
// and is skipped. A line that cannot be parsed is dropped rather than failing the render,
// so a partly malformed script still shows the cues it does have.
func assToVTT(w io.Writer, r io.Reader) error {
	bw := bufio.NewWriter(w)
	if _, err := bw.WriteString("WEBVTT\n\n"); err != nil {
		return err
	}
	startIdx, endIdx, textIdx, fields := assDefaultFormat()
	var cues []assCue
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // tolerate long cue lines
	first, inEvents := true, false
	for sc.Scan() {
		line := sc.Text()
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inEvents = strings.EqualFold(line, "[events]")
			continue
		}
		if !inEvents {
			continue
		}
		if rest, ok := cutPrefixFold(line, "format:"); ok {
			startIdx, endIdx, textIdx, fields = assFormat(rest)
			continue
		}
		rest, ok := cutPrefixFold(line, "dialogue:")
		if !ok {
			continue
		}
		parts := strings.SplitN(rest, ",", fields)
		if len(parts) < fields || textIdx >= len(parts) {
			continue
		}
		start, okStart := assTime(parts[startIdx])
		end, okEnd := assTime(parts[endIdx])
		if !okStart || !okEnd || end <= start {
			continue
		}
		if text := assText(parts[textIdx]); text != "" {
			cues = append(cues, assCue{start: start, end: end, text: text})
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].start < cues[j].start })
	for _, c := range cues {
		if _, err := bw.WriteString(vttTime(c.start) + " --> " + vttTime(c.end) + "\n" + c.text + "\n\n"); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// assDefaultFormat is the near-universal ASS field order, used until an "[Events]"
// "Format:" line says otherwise: Layer, Start, End, Style, Name, MarginL, MarginR,
// MarginV, Effect, Text.
func assDefaultFormat() (start, end, text, fields int) { return 1, 2, 9, 10 }

// assFormat reads an "[Events]" Format field list and returns the positions of Start, End
// and Text plus the field count. Text is always the last field and may itself contain
// commas, so the count doubles as the SplitN limit that keeps it whole. A list missing
// Start or End is unusable and falls back to the default order.
func assFormat(list string) (start, end, text, fields int) {
	names := strings.Split(list, ",")
	start, end, text, fields = -1, -1, len(names)-1, len(names)
	for i, n := range names {
		switch strings.ToLower(strings.TrimSpace(n)) {
		case "start":
			start = i
		case "end":
			end = i
		case "text":
			text = i
		}
	}
	if start < 0 || end < 0 || fields < 2 {
		return assDefaultFormat()
	}
	return start, end, text, fields
}

// assTime parses an ASS "H:MM:SS.cc" timestamp into centiseconds.
func assTime(s string) (int, bool) {
	var h, m, sec, cs int
	if n, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d:%d.%d", &h, &m, &sec, &cs); n != 4 || err != nil {
		return 0, false
	}
	if h < 0 || m < 0 || sec < 0 || cs < 0 {
		return 0, false
	}
	return ((h*60+m)*60+sec)*100 + cs, true
}

// vttTime renders centiseconds as a WebVTT "HH:MM:SS.mmm" timestamp.
func vttTime(cs int) string {
	ms := cs * 10
	h, ms := ms/3600000, ms%3600000
	m, ms := ms/60000, ms%60000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, ms/1000, ms%1000)
}

// assText renders one ASS dialogue field as WebVTT cue text: override blocks dropped, the
// ASS line-break and hard-space escapes expanded, and "&"/"<"/">" escaped so cue text is
// never parsed as WebVTT markup. Blank lines would end the cue early, so they are removed;
// a field holding nothing but styling collapses to "" and its cue is skipped.
func assText(s string) string {
	s = assOverride.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, `\h`, " ")
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `\N`, "\n")
	s = strings.ReplaceAll(s, `\n`, "\n")
	var kept []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// cutPrefixFold splits off a case-insensitive prefix, returning the trimmed remainder.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(s[len(prefix):]), true
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
