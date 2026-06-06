package transcode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeedsTranscode(t *testing.T) {
	cases := map[string]bool{
		".mp4":  false,
		".MP4":  false,
		"mp4":   false,
		".webm": false,
		".m4v":  false,
		".avi":  true,
		".AVI":  true,
		"avi":   true,
		".mkv":  true,
		".mov":  true,
		".ts":   true,
		".wmv":  true,
		".xyz":  true,
		"":      true,
	}
	for ext, want := range cases {
		if got := NeedsTranscode(ext); got != want {
			t.Errorf("NeedsTranscode(%q) = %v, want %v", ext, got, want)
		}
	}
}

func TestRemuxEligible(t *testing.T) {
	cases := []struct {
		video, audio string
		want         bool
	}{
		{"h264", "aac", true},
		{"h264", "mp3", true},
		{"h264", "", true},
		{"h264", "ac3", false},
		{"hevc", "aac", false},
		{"mpeg4", "mp3", false},
		{"", "aac", false},
	}
	for _, c := range cases {
		got := RemuxEligible(Streams{VideoCodec: c.video, AudioCodec: c.audio})
		if got != c.want {
			t.Errorf("RemuxEligible(%q,%q) = %v, want %v", c.video, c.audio, got, c.want)
		}
	}
}

func TestBuildPlaylist(t *testing.T) {
	pl := buildPlaylist(13) // 13s -> 3 segments (6, 6, 1)
	for _, want := range []string{"#EXTM3U", "#EXT-X-ENDLIST", "seg0.ts", "seg1.ts", "seg2.ts"} {
		if !strings.Contains(pl, want) {
			t.Errorf("playlist missing %q:\n%s", want, pl)
		}
	}
	if strings.Contains(pl, "seg3.ts") {
		t.Errorf("playlist has an extra segment:\n%s", pl)
	}
}

func TestHLSEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH")
	}

	dir := t.TempDir()
	in := filepath.Join(dir, "in.avi")
	gen := exec.Command("ffmpeg", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-shortest", in,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate test input: %v\n%s", err, out)
	}

	m := NewManager(Options{})
	defer m.Close()

	pl, err := m.Playlist("k", in)
	if err != nil {
		t.Fatalf("Playlist: %v", err)
	}
	if !strings.Contains(string(pl), "#EXTM3U") || !strings.Contains(string(pl), "#EXT-X-ENDLIST") {
		t.Fatalf("unexpected playlist:\n%s", pl)
	}

	seg, err := m.Segment("k", "seg0.ts")
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	fi, err := os.Stat(seg)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("segment missing or empty: %v", err)
	}

	if _, err := m.Segment("k", "../etc/passwd"); err == nil {
		t.Error("expected error for invalid segment name")
	}
}
