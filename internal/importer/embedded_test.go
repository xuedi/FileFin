package importer

import (
	"testing"

	"filefin/internal/ffprobe"
)

// TestChooseEmbeddedSubtitles covers the selection rules: text-only (bitmap skipped),
// real-language-only (empty/und skipped), alias normalisation, dedup against existing
// sidecars, and first-of-a-language wins.
func TestChooseEmbeddedSubtitles(t *testing.T) {
	streams := []ffprobe.SubtitleStream{
		{Index: 0, Codec: "subrip", Language: "eng"},            // -> en, picked
		{Index: 1, Codec: "hdmv_pgs_subtitle", Language: "fre"}, // bitmap, skipped
		{Index: 2, Codec: "ass", Language: "und"},               // undetermined, skipped
		{Index: 3, Codec: "subrip", Language: ""},               // no tag, skipped
		{Index: 4, Codec: "subrip", Language: "ger"},            // -> de, but de already present
		{Index: 5, Codec: "mov_text", Language: "spa"},          // -> es, picked
		{Index: 6, Codec: "subrip", Language: "eng"},            // en again, first one wins
	}
	present := map[string]bool{"de": true}

	got := chooseEmbeddedSubtitles(streams, present)
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
	// Only bitmap and untagged tracks: nothing to extract.
	streams := []ffprobe.SubtitleStream{
		{Index: 0, Codec: "dvd_subtitle", Language: "eng"},
		{Index: 1, Codec: "subrip", Language: ""},
	}
	if got := chooseEmbeddedSubtitles(streams, nil); len(got) != 0 {
		t.Fatalf("want no picks, got %+v", got)
	}
}
