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

func TestDetectEncoders(t *testing.T) {
	yes := func(string) bool { return true }
	no := func(string) bool { return false }
	onlyNode := func(want string) func(string) bool {
		return func(node string) bool { return node == want }
	}
	nodes := []string{"/dev/dri/renderD128", "/dev/dri/renderD129"}

	t.Run("hwaccel off -> software", func(t *testing.T) {
		got := detectEncoders(Options{HWAccel: hwAccelOff}, yes, yes, nodes)
		if len(got) != 1 || got[0].Kind != KindSoftware {
			t.Fatalf("got %+v, want single software encoder", got)
		}
	})

	t.Run("no h264_vaapi -> software", func(t *testing.T) {
		got := detectEncoders(Options{}, no, yes, nodes)
		if len(got) != 1 || got[0].Kind != KindSoftware {
			t.Fatalf("got %+v, want single software encoder", got)
		}
	})

	t.Run("no node encodes -> software", func(t *testing.T) {
		got := detectEncoders(Options{}, yes, no, nodes)
		if len(got) != 1 || got[0].Kind != KindSoftware {
			t.Fatalf("got %+v, want single software encoder", got)
		}
	})

	t.Run("all nodes encode -> one vaapi encoder each", func(t *testing.T) {
		got := detectEncoders(Options{}, yes, yes, nodes)
		if len(got) != len(nodes) {
			t.Fatalf("got %d encoders, want %d: %+v", len(got), len(nodes), got)
		}
		for i, e := range got {
			if e.Kind != KindVAAPI || e.Codec != "h264_vaapi" || e.Device != nodes[i] {
				t.Errorf("encoder %d = %+v, want vaapi on %q", i, e, nodes[i])
			}
		}
	})

	t.Run("only second node encodes", func(t *testing.T) {
		got := detectEncoders(Options{}, yes, onlyNode(nodes[1]), nodes)
		if len(got) != 1 || got[0].Device != nodes[1] {
			t.Fatalf("got %+v, want single vaapi on %q", got, nodes[1])
		}
	})

	t.Run("device override probes only that node", func(t *testing.T) {
		probed := []string{}
		probe := func(node string) bool { probed = append(probed, node); return true }
		got := detectEncoders(Options{HWAccelDevice: nodes[1]}, yes, probe, nodes)
		if len(probed) != 1 || probed[0] != nodes[1] {
			t.Fatalf("probed %v, want only %q", probed, nodes[1])
		}
		if len(got) != 1 || got[0].Device != nodes[1] {
			t.Fatalf("got %+v, want single vaapi on %q", got, nodes[1])
		}
	})
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
