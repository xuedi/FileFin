package importer

import (
	"testing"

	"filefin/internal/ffprobe"
)

// TestChooseEmbeddedSubtitles covers the selection rules: text-only (bitmap skipped),
// alias normalisation, dedup against existing sidecars, and first-of-a-language wins.
func TestChooseEmbeddedSubtitles(t *testing.T) {
	streams := []ffprobe.SubtitleStream{
		{Index: 0, Codec: "subrip", Language: "eng"},            // -> en, picked
		{Index: 1, Codec: "hdmv_pgs_subtitle", Language: "fre"}, // bitmap, skipped
		{Index: 2, Codec: "ass", Language: "und"},               // -> fallback en, already claimed
		{Index: 3, Codec: "subrip", Language: ""},               // -> fallback en, already claimed
		{Index: 4, Codec: "subrip", Language: "ger"},            // -> de, but de already present
		{Index: 5, Codec: "mov_text", Language: "spa"},          // -> es, picked
		{Index: 6, Codec: "subrip", Language: "eng"},            // en again, first one wins
	}
	present := map[string]bool{"de": true}

	got := chooseEmbeddedSubtitles(streams, present, "en")
	want := []embeddedPick{{Index: 0, Lang: "en"}, {Index: 5, Lang: "es"}}
	if len(got) != len(want) {
		t.Fatalf("picked %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("picked %+v, want %+v", got, want)
		}
	}
}

func TestChooseEmbeddedSubtitlesNone(t *testing.T) {
	// Only bitmap tracks: nothing a sidecar could be made of.
	streams := []ffprobe.SubtitleStream{
		{Index: 0, Codec: "dvd_subtitle", Language: "eng"},
		{Index: 1, Codec: "hdmv_pgs_subtitle", Language: ""},
	}
	if got := chooseEmbeddedSubtitles(streams, nil, "en"); len(got) != 0 {
		t.Fatalf("want no picks, got %+v", got)
	}
}

// TestChooseEmbeddedSubtitlesUntagged pins the fix for releases shipping one unlabelled
// text track (common for IQIYI/WeTV WEB-DLs): it is extracted under the configured
// subtitle language instead of being dropped, and a second untagged track is dedup'd away.
func TestChooseEmbeddedSubtitlesUntagged(t *testing.T) {
	streams := []ffprobe.SubtitleStream{
		{Index: 0, Codec: "subrip", Language: ""},
		{Index: 1, Codec: "subrip", Language: "und"},
	}
	got := chooseEmbeddedSubtitles(streams, nil, "en")
	if len(got) != 1 || got[0] != (embeddedPick{Index: 0, Lang: "en"}) {
		t.Fatalf("picked %+v, want one en pick of stream 0", got)
	}
}

// TestChooseEmbeddedSubtitlesUntaggedCovered: an untagged track resolves to the fallback,
// so an existing sidecar in that language already covers it.
func TestChooseEmbeddedSubtitlesUntaggedCovered(t *testing.T) {
	streams := []ffprobe.SubtitleStream{{Index: 0, Codec: "subrip", Language: ""}}
	if got := chooseEmbeddedSubtitles(streams, map[string]bool{"en": true}, "en"); len(got) != 0 {
		t.Fatalf("want no picks, got %+v", got)
	}
}

// TestTrackLang covers the evidence order: language tag, then a title that names a
// language, then the fallback.
func TestTrackLang(t *testing.T) {
	cases := []struct {
		name     string
		st       ffprobe.SubtitleStream
		fallback string
		want     string
	}{
		{"language tag wins", ffprobe.SubtitleStream{Language: "ger", Title: "English"}, "en", "de"},
		{"title names a language", ffprobe.SubtitleStream{Title: "Chinese"}, "en", "zh"},
		{"title alias", ffprobe.SubtitleStream{Title: "jpn"}, "en", "ja"},
		{"title is a variant, not a language", ffprobe.SubtitleStream{Title: "Full"}, "en", "en"},
		{"sdh title falls back", ffprobe.SubtitleStream{Language: "und", Title: "SDH"}, "de", "de"},
		{"nothing at all", ffprobe.SubtitleStream{}, "zh", "zh"},
		{"no evidence and no fallback", ffprobe.SubtitleStream{}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := trackLang(c.st, c.fallback); got != c.want {
				t.Fatalf("trackLang = %q, want %q", got, c.want)
			}
		})
	}
}
