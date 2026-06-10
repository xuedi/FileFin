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

func TestDirectPlayable(t *testing.T) {
	cases := []struct {
		name                      string
		container, vCodec, aCodec string
		want                      bool
	}{
		{"mp4 h264/aac", "mov,mp4,m4a,3gp,3g2,mj2", "h264", "aac", true},
		{"mp4 h264/mp3", "mp4", "h264", "mp3", true},
		{"mp4 h264 no audio", "mp4", "h264", "", true},
		{"mp4 hevc", "mov,mp4,m4a,3gp,3g2,mj2", "hevc", "aac", false},
		{"mp4 h264/ac3", "mp4", "h264", "ac3", false},
		{"avi-named but really mp4 h264/aac", "mov,mp4,m4a,3gp,3g2,mj2", "h264", "aac", true},
		{"webm vp9/opus", "matroska,webm", "vp9", "opus", true},
		{"webm vp8/vorbis", "matroska,webm", "vp8", "vorbis", true},
		{"webm av1 no audio", "webm", "av1", "", true},
		{"matroska h264/aac (not webm-native)", "matroska,webm", "h264", "aac", false},
		{"unknown container", "avi", "h264", "aac", false},
		{"unknown codec", "mp4", "theora", "aac", false},
		{"empty container (never probed)", "", "h264", "aac", false},
	}
	for _, c := range cases {
		if got := DirectPlayable(c.container, c.vCodec, c.aCodec); got != c.want {
			t.Errorf("%s: DirectPlayable(%q,%q,%q) = %v, want %v",
				c.name, c.container, c.vCodec, c.aCodec, got, c.want)
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
