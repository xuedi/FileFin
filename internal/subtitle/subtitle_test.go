package subtitle

import (
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		base, name string
		wantLang   string
		wantOK     bool
	}{
		{"Movie", "Movie.srt", "", true},
		{"Movie", "Movie.en.srt", "en", true},
		{"Movie", "Movie.en.forced.srt", "en", true},
		{"Movie", "Other.srt", "", false},
		{"Movie", "Movie.txt", "", false}, // only .srt
	}
	for _, c := range cases {
		lang, ok := Match(c.base, c.name)
		if ok != c.wantOK || lang != c.wantLang {
			t.Errorf("Match(%q,%q) = (%q,%v), want (%q,%v)", c.base, c.name, lang, ok, c.wantLang, c.wantOK)
		}
	}
}

func TestSidecars(t *testing.T) {
	names := []string{"Movie.mkv", "Movie.srt", "Movie.de.srt", "Unrelated.srt"}
	subs := Sidecars(names, "Movie")
	if len(subs) != 2 {
		t.Fatalf("got %d sidecars, want 2: %+v", len(subs), subs)
	}
	if subs[0].Lang != "" || subs[1].Lang != "de" || subs[1].Label != "German" {
		t.Fatalf("unexpected sidecars: %+v", subs)
	}
}

func TestToVTT(t *testing.T) {
	srt := "1\n00:00:01,000 --> 00:00:04,000\nHello\n\n2\n00:00:05,500 --> 00:00:06,000\nWorld\n"
	var b strings.Builder
	if err := ToVTT(&b, strings.NewReader(srt)); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.HasPrefix(out, "WEBVTT\n\n") {
		t.Errorf("missing WEBVTT header:\n%s", out)
	}
	if strings.Contains(out, ",000") || strings.Contains(out, ",500") {
		t.Errorf("comma separators not rewritten:\n%s", out)
	}
	if !strings.Contains(out, "00:00:01.000 --> 00:00:04.000") {
		t.Errorf("timing line not converted:\n%s", out)
	}
	// The numeric cue index lines are dropped.
	if strings.Contains(out, "\n1\n") || strings.Contains(out, "\n2\n") {
		t.Errorf("cue index lines not dropped:\n%s", out)
	}
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "World") {
		t.Errorf("cue text lost:\n%s", out)
	}
}

func TestLabel(t *testing.T) {
	if Label("en") != "English" {
		t.Errorf("Label(en) = %q", Label("en"))
	}
	if Label("xx") != "XX" { // unknown falls back to uppercased tag
		t.Errorf("Label(xx) = %q", Label("xx"))
	}
}
