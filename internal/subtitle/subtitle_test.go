package subtitle

import (
	"strings"
	"testing"
)

func TestNormalizeLang(t *testing.T) {
	cases := map[string]string{
		"":        "en", // empty -> default
		"EN":      "en",
		" eng ":   "en",
		"english": "en",
		"zho":     "zh",
		"ja":      "ja",
		"klingon": "klingon", // unknown kept as-is, lowercased
	}
	for in, want := range cases {
		if got := NormalizeLang(in, "en"); got != want {
			t.Errorf("NormalizeLang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLangFromName(t *testing.T) {
	cases := map[string]string{
		"Movie.srt":                  "",
		"Movie.en.srt":               "en",
		"Movie.eng.srt":              "eng",
		"Movie.en.forced.srt":        "en",
		"Movie.zh.ass":               "zh",
		"Sparrow - 42.srt":           "", // a trailing number is not a language infix
		"Some.Release.Name.x264.srt": "", // junk infix rejected
	}
	for in, want := range cases {
		if got := LangFromName(in); got != want {
			t.Errorf("LangFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatch(t *testing.T) {
	base := "(1967) The Assassin"
	cases := []struct {
		name     string
		wantLang string
		wantOK   bool
	}{
		{"(1967) The Assassin.srt", "", true},             // no infix
		{"(1967) The Assassin.en.srt", "en", true},        // language infix
		{"(1967) The Assassin.zh.srt", "zh", true},        // another language
		{"(1967) The Assassin.en.forced.srt", "en", true}, // qualifier after lang
		{"(1967) The Assassin.x264.srt", "", false},       // non-language infix
		{"(1967) The Assassin.en.ass", "", false},         // not .srt
		{"Other Movie.srt", "", false},                    // different base
		{"(1967) The Assassin.en.vtt", "", false},         // not .srt
	}
	for _, c := range cases {
		lang, ok := Match(base, c.name)
		if ok != c.wantOK || lang != c.wantLang {
			t.Errorf("Match(%q, %q) = (%q, %v), want (%q, %v)", base, c.name, lang, ok, c.wantLang, c.wantOK)
		}
	}
}

func TestToVTT(t *testing.T) {
	srt := "\ufeff1\r\n00:00:01,000 --> 00:00:04,000\r\nLine one.\r\n\r\n" +
		"2\r\n00:00:05,500 --> 00:00:09,250\r\nLine two.\r\nSecond row 1999.\r\n"
	var b strings.Builder
	if err := ToVTT(&b, strings.NewReader(srt)); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.HasPrefix(out, "WEBVTT\n\n") {
		t.Fatalf("missing WEBVTT header: %q", out)
	}
	if strings.Contains(out, "\ufeff") {
		t.Errorf("BOM not stripped: %q", out)
	}
	if !strings.Contains(out, "00:00:01.000 --> 00:00:04.000") || !strings.Contains(out, "00:00:05.500 --> 00:00:09.250") {
		t.Errorf("timestamps not rewritten: %q", out)
	}
	if strings.Contains(out, ",000") || strings.Contains(out, ",250") {
		t.Errorf("comma separator left in timing: %q", out)
	}
	// Cue-index lines dropped, but a numeric line that is real cue text is kept.
	for _, line := range strings.Split(out, "\n") {
		if line == "1" || line == "2" {
			t.Errorf("cue index not dropped: %q", out)
		}
	}
	if !strings.Contains(out, "Second row 1999.") {
		t.Errorf("cue text lost: %q", out)
	}
}

func TestToVTTTrailingNumber(t *testing.T) {
	// A numeric final line with no following timing line is real text, not an index.
	var b strings.Builder
	if err := ToVTT(&b, strings.NewReader("00:00:01,000 --> 00:00:02,000\n42\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "\n42\n") {
		t.Errorf("trailing numeric text dropped: %q", b.String())
	}
}
