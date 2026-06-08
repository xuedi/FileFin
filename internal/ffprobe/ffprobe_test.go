package ffprobe

import "testing"

// TestParseSubtitleStreams checks that only subtitle streams are returned, numbered by
// their position among subtitle streams (the 0:s:N index), with codec and language.
func TestParseSubtitleStreams(t *testing.T) {
	data := []byte(`{
		"format": {"duration": "120.0", "format_name": "matroska"},
		"streams": [
			{"codec_type": "video", "codec_name": "h264", "width": 1920, "height": 1080},
			{"codec_type": "audio", "codec_name": "aac"},
			{"codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng"}},
			{"codec_type": "subtitle", "codec_name": "hdmv_pgs_subtitle", "tags": {"language": "ger"}},
			{"codec_type": "subtitle", "codec_name": "ass"}
		]
	}`)
	subs := parseSubtitleStreams(data)
	if len(subs) != 3 {
		t.Fatalf("got %d subtitle streams, want 3: %+v", len(subs), subs)
	}
	want := []SubtitleStream{
		{Index: 0, Codec: "subrip", Language: "eng"},
		{Index: 1, Codec: "hdmv_pgs_subtitle", Language: "ger"},
		{Index: 2, Codec: "ass", Language: ""},
	}
	for i, w := range want {
		if subs[i] != w {
			t.Errorf("stream %d = %+v, want %+v", i, subs[i], w)
		}
	}
}

func TestParseSubtitleStreamsNone(t *testing.T) {
	data := []byte(`{"streams": [{"codec_type": "video", "codec_name": "h264"}]}`)
	if subs := parseSubtitleStreams(data); len(subs) != 0 {
		t.Fatalf("want no subtitle streams, got %+v", subs)
	}
	if subs := parseSubtitleStreams([]byte("not json")); subs != nil {
		t.Fatalf("malformed input should yield nil, got %+v", subs)
	}
}
