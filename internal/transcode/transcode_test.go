package transcode

import "testing"

func TestNeedsTranscode(t *testing.T) {
	cases := map[string]bool{
		".mp4": false, ".webm": false, ".m4v": false,
		"mp4":  false, // accepts a dotless ext
		".MP4": false, // case-insensitive
		".mkv": true, ".avi": true, ".mov": true, ".ts": true,
	}
	for ext, want := range cases {
		if got := NeedsTranscode(ext); got != want {
			t.Errorf("NeedsTranscode(%q) = %v, want %v", ext, got, want)
		}
	}
}

func TestRemuxEligible(t *testing.T) {
	cases := []struct {
		s    Streams
		want bool
	}{
		{Streams{VideoCodec: "h264", AudioCodec: "aac"}, true},
		{Streams{VideoCodec: "h264", AudioCodec: "mp3"}, true},
		{Streams{VideoCodec: "h264", AudioCodec: ""}, true},
		{Streams{VideoCodec: "h264", AudioCodec: "ac3"}, false},
		{Streams{VideoCodec: "hevc", AudioCodec: "aac"}, false},
	}
	for _, c := range cases {
		if got := RemuxEligible(c.s); got != c.want {
			t.Errorf("RemuxEligible(%+v) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestBuildPlaylist(t *testing.T) {
	pl := buildPlaylist(13) // 6s segments -> 3 segments (6, 6, 1)
	if want := "seg0.ts"; !contains(pl, want) {
		t.Errorf("playlist missing %q:\n%s", want, pl)
	}
	if want := "seg2.ts"; !contains(pl, want) {
		t.Errorf("playlist missing %q:\n%s", want, pl)
	}
	if !contains(pl, "#EXT-X-ENDLIST") {
		t.Errorf("playlist missing endlist:\n%s", pl)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
