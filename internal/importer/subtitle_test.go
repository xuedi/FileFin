package importer

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// writeFiles creates each named empty file in dir.
func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindSidecarSubtitles(t *testing.T) {
	cases := []struct {
		name  string
		files []string // sidecars present beside Movie.mkv
		want  []string // "lang|ext" pairs, sorted
	}{
		{"plain", []string{"Movie.srt"}, []string{"|.srt"}},
		{"two languages", []string{"Movie.en.srt", "Movie.de.srt"}, []string{"de|.srt", "en|.srt"}},
		{"alias and qualifier", []string{"Movie.eng.srt", "Movie.en.forced.srt"}, []string{"eng|.srt", "en|.srt"}},
		{"convert format", []string{"Movie.ass"}, []string{"|.ass"}},
		{"vobsub pair once", []string{"Movie.sub", "Movie.idx"}, []string{"|.sub"}},
		{"pgs bitmap", []string{"Movie.sup"}, []string{"|.sup"}},
		{"ignore foreign and non-language infix", []string{"Other.srt", "Movie.foobar.srt"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			video := filepath.Join(dir, "Movie.mkv")
			writeFiles(t, dir, "Movie.mkv")
			writeFiles(t, dir, c.files...)

			var got []string
			for _, s := range FindSidecarSubtitles(video) {
				got = append(got, s.Language+"|"+s.Ext)
			}
			sort.Strings(got)
			if len(got) != len(c.want) {
				t.Fatalf("FindSidecarSubtitles = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("FindSidecarSubtitles = %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestSubtitleTargetName(t *testing.T) {
	got := SubtitleTargetName("(2008) Breaking Bad - 2x5.mkv", "en", ".srt")
	if want := "(2008) Breaking Bad - 2x5.en.srt"; got != want {
		t.Errorf("SubtitleTargetName = %q, want %q", got, want)
	}
}

// TestPlaceSubtitles checks naming, the default-language fallback, same-language
// deduplication, and the convert-failure fallback (no ffmpeg -> keep the original).
func TestPlaceSubtitles(t *testing.T) {
	src := t.TempDir()
	writeFiles(t, src, "a.srt", "b.srt", "c.de.srt", "d.ass")
	subs := []Subtitle{
		{Path: filepath.Join(src, "a.srt"), Language: "", Ext: ".srt"},
		{Path: filepath.Join(src, "b.srt"), Language: "", Ext: ".srt"},
		{Path: filepath.Join(src, "c.de.srt"), Language: "de", Ext: ".srt"},
		{Path: filepath.Join(src, "d.ass"), Language: "fr", Ext: ".ass"},
	}
	dst := t.TempDir()
	videoTarget := filepath.Join(dst, "Movie.mkv")
	// ffmpegPath "" forces the conversion fallback for the .ass file.
	PlaceSubtitles(context.Background(), videoTarget, "en", "", subs)

	want := []string{
		"Movie.en.2.srt", // second untagged subtitle, deduped
		"Movie.en.srt",   // first untagged subtitle, default language applied
		"Movie.de.srt",
		"Movie.fr.ass", // conversion fell back to verbatim copy
	}
	for _, n := range want {
		if _, err := os.Stat(filepath.Join(dst, n)); err != nil {
			t.Errorf("expected placed subtitle %q: %v", n, err)
		}
	}
}
