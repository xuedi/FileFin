package importer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSubtitleTargetName(t *testing.T) {
	cases := []struct {
		video, lang, sext, want string
	}{
		{"(1967) The Assassin.avi", "en", ".srt", "(1967) The Assassin.en.srt"},
		{"(2002) Firefly - 1x1.mkv", "en", ".srt", "(2002) Firefly - 1x1.en.srt"},
		{"(2010) Two Discs - part1.avi", "zh", ".sub", "(2010) Two Discs - part1.zh.sub"},
	}
	for _, c := range cases {
		if got := SubtitleTargetName(c.video, c.lang, c.sext); got != c.want {
			t.Errorf("SubtitleTargetName(%q,%q,%q) = %q, want %q", c.video, c.lang, c.sext, got, c.want)
		}
	}
}

func TestFindSidecarSubtitles(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	video := "(1972) The 14 Amazons.avi"
	mk(video)
	mk("(1972) The 14 Amazons.srt")        // no infix -> default language
	mk("(1972) The 14 Amazons.eng.srt")    // language token
	mk("(1972) The 14 Amazons.zh.ass")     // language token, ass
	mk("(1972) The 14 Amazons.zh.sub")     // VobSub pair...
	mk("(1972) The 14 Amazons.zh.idx")     // ...attached once
	mk("Other Movie.srt")                  // not a match
	mk("(1972) The 14 Amazons.poster.jpg") // not a subtitle ext

	subs := FindSidecarSubtitles(filepath.Join(dir, video))
	// Index by language for assertions; the no-infix one has empty language.
	byLang := map[string][]Subtitle{}
	for _, s := range subs {
		byLang[s.Language] = append(byLang[s.Language], s)
	}
	if len(byLang[""]) != 1 || strings.ToLower(filepath.Ext(byLang[""][0].Path)) != ".srt" {
		t.Errorf("expected one untagged .srt, got %+v", byLang[""])
	}
	if len(byLang["eng"]) != 1 {
		t.Errorf("expected one .eng.srt, got %+v", byLang["eng"])
	}
	if len(byLang["zh"]) != 2 {
		// zh has the .ass and the VobSub pair (one Subtitle, Ext .sub)
		t.Errorf("expected .zh.ass and the VobSub pair, got %+v", byLang["zh"])
	}
	// The VobSub pair must be attached exactly once, with the canonical .sub ext.
	vob := 0
	for _, s := range subs {
		if s.Ext == ".sub" {
			vob++
		}
	}
	if vob != 1 {
		t.Errorf("VobSub pair attached %d times, want 1", vob)
	}
	// "Other Movie.srt" must not have been picked up.
	for _, s := range subs {
		if strings.Contains(s.Path, "Other Movie") {
			t.Errorf("matched a foreign subtitle: %q", s.Path)
		}
	}
}

func TestPlaceSubtitlesCopyAndCollision(t *testing.T) {
	dir := t.TempDir()
	src := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		return p
	}
	a := src("a.srt", "AAA")
	b := src("b.srt", "BBBB")
	idx := src("v.idx", "IDX")
	sub := src("v.sub", "SUBDATA")

	target := filepath.Join(dir, "(2020) Movie.mkv")
	os.WriteFile(target, []byte("video"), 0o644)

	placeSubtitles(target, "en", "", []Subtitle{
		{Path: a, Language: "en", Ext: ".srt"},
		{Path: b, Language: "en", Ext: ".srt"}, // same language -> suffixed
		{Path: sub, Language: "zh", Ext: ".sub"},
	}, false)

	must := func(name, content string) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(b) != content {
			t.Errorf("%s: got %q err=%v, want %q", name, b, err, content)
		}
	}
	must("(2020) Movie.en.srt", "AAA")
	must("(2020) Movie.en.2.srt", "BBBB")
	must("(2020) Movie.zh.sub", "SUBDATA")
	must("(2020) Movie.zh.idx", "IDX")
	_ = idx
}

func TestPlaceSubtitlesAssConversion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is POSIX")
	}
	dir := t.TempDir()
	// A fake ffmpeg that writes "CONVERTED" to its last argument (the output path).
	stub := filepath.Join(dir, "fakeffmpeg")
	script := "#!/bin/sh\nout=\"\"\nfor a in \"$@\"; do out=\"$a\"; done\nprintf 'CONVERTED' > \"$out\"\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	assSrc := filepath.Join(dir, "in.ass")
	os.WriteFile(assSrc, []byte("[Script Info]"), 0o644)
	target := filepath.Join(dir, "(2020) Movie.mkv")
	os.WriteFile(target, []byte("video"), 0o644)

	// With ffmpeg present, .ass is converted to .srt.
	placeSubtitles(target, "en", stub, []Subtitle{{Path: assSrc, Language: "en", Ext: ".ass"}}, false)
	if b, err := os.ReadFile(filepath.Join(dir, "(2020) Movie.en.srt")); err != nil || string(b) != "CONVERTED" {
		t.Fatalf("ass->srt conversion: got %q err=%v", b, err)
	}

	// With ffmpeg absent, .ass falls back to a verbatim copy under its own ext.
	dir2 := t.TempDir()
	assSrc2 := filepath.Join(dir2, "in.ass")
	os.WriteFile(assSrc2, []byte("STYLED"), 0o644)
	target2 := filepath.Join(dir2, "(2021) Other.mkv")
	os.WriteFile(target2, []byte("video"), 0o644)
	placeSubtitles(target2, "en", "", []Subtitle{{Path: assSrc2, Language: "en", Ext: ".ass"}}, false)
	if b, err := os.ReadFile(filepath.Join(dir2, "(2021) Other.en.ass")); err != nil || string(b) != "STYLED" {
		t.Fatalf("ass verbatim fallback: got %q err=%v", b, err)
	}
}

func TestExecutePlacesSubtitles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "movie.avi")
	os.WriteFile(src, []byte("VIDEO"), 0o644)
	sub := filepath.Join(dir, "movie.srt")
	os.WriteFile(sub, []byte("SUBS"), 0o644)
	data := filepath.Join(dir, "data")

	res, err := Execute(Request{
		SourcePath: src, DataDir: data, Category: "C", Title: "Movie", Year: 2020,
		SubtitleLanguage: "en",
		Subtitles:        []Subtitle{{Path: sub, Language: "", Ext: ".srt"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(res.Folder, "(2020) Movie.en.srt")
	if b, err := os.ReadFile(want); err != nil || string(b) != "SUBS" {
		t.Fatalf("placed subtitle: got %q err=%v, want SUBS at %s", b, err, want)
	}
}
