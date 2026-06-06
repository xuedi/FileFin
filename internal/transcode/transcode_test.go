package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestSegIndex(t *testing.T) {
	cases := map[string]int{"seg0.ts": 0, "seg7.ts": 7, "seg123.ts": 123}
	for name, want := range cases {
		if got := segIndex(name); got != want {
			t.Errorf("segIndex(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestRepositionTarget(t *testing.T) {
	// repositionLead is 3.
	cases := []struct {
		name                        string
		startSeg, produced, request int
		wantTarget                  int
		wantReposition              bool
	}{
		{"cold start, seg0", 0, -1, 0, 0, false},
		{"normal prebuffer within lead", 0, 5, 8, 0, false},
		{"at the lead boundary", 0, 5, 8 + 0, 0, false}, // 5+3 = 8, still in reach
		{"one past the lead boundary", 0, 5, 9, 9, true},
		{"far-forward jump", 0, 5, 200, 200, true},
		{"seek back behind start", 100, 105, 10, 10, true},
		{"at start with nothing produced", 100, 99, 100, 0, false},
		{"playing forward at the head", 100, 102, 103, 0, false},
	}
	for _, c := range cases {
		gotTarget, gotReposition := repositionTarget(c.startSeg, c.produced, c.request)
		if gotReposition != c.wantReposition {
			t.Errorf("%s: reposition = %v, want %v", c.name, gotReposition, c.wantReposition)
		}
		if gotReposition && gotTarget != c.wantTarget {
			t.Errorf("%s: target = %d, want %d", c.name, gotTarget, c.wantTarget)
		}
	}
}

func TestHighestSeg(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"seg0.ts", "seg1.ts", "seg4.ts", "index.m3u8", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := highestSeg(dir, 0); got != 4 {
		t.Errorf("highestSeg(startSeg=0) = %d, want 4", got)
	}
	// Segments below startSeg are ignored (they belong to an earlier run).
	if got := highestSeg(dir, 2); got != 4 {
		t.Errorf("highestSeg(startSeg=2) = %d, want 4", got)
	}
	if got := highestSeg(t.TempDir(), 10); got != 9 {
		t.Errorf("highestSeg(empty, startSeg=10) = %d, want 9", got)
	}
}

func TestVideoEncodeArgs(t *testing.T) {
	contains := func(args []string, want string) bool {
		for _, a := range args {
			if a == want {
				return true
			}
		}
		return false
	}

	// Software path: libx264, no VAAPI flags, keyframe expr present.
	pre, codec := videoEncodeArgs(softwareEncoder, 0)
	if len(pre) != 0 {
		t.Errorf("software preInput = %v, want empty", pre)
	}
	if !contains(codec, "libx264") {
		t.Errorf("software codec missing libx264: %v", codec)
	}
	if contains(codec, "h264_vaapi") || contains(codec, "hwupload") {
		t.Errorf("software codec leaked vaapi flags: %v", codec)
	}
	if !contains(codec, "expr:gte(t,(n_forced+0)*6)") {
		t.Errorf("software codec missing keyframe expr: %v", codec)
	}

	// VAAPI path: -vaapi_device before input, hwupload + h264_vaapi, offset keyframes.
	vaapi := Encoder{Kind: "vaapi", Device: "/dev/dri/renderD128", Codec: "h264_vaapi"}
	pre, codec = videoEncodeArgs(vaapi, 5)
	if len(pre) != 2 || pre[0] != "-vaapi_device" || pre[1] != "/dev/dri/renderD128" {
		t.Errorf("vaapi preInput = %v, want -vaapi_device /dev/dri/renderD128", pre)
	}
	if !contains(codec, "h264_vaapi") || !contains(codec, "format=nv12,hwupload") {
		t.Errorf("vaapi codec missing hw flags: %v", codec)
	}
	if contains(codec, "libx264") {
		t.Errorf("vaapi codec leaked libx264: %v", codec)
	}
	if !contains(codec, "expr:gte(t,(n_forced+5)*6)") {
		t.Errorf("vaapi codec missing offset keyframe expr: %v", codec)
	}
}

func TestOptimizeArgs(t *testing.T) {
	contains := func(args []string, want string) bool {
		for _, a := range args {
			if a == want {
				return true
			}
		}
		return false
	}

	sw := OptimizeArgs(softwareEncoder, "in.avi", "out.mp4")
	if !contains(sw, "libx264") || !contains(sw, "+faststart") {
		t.Errorf("software optimize args missing libx264/faststart: %v", sw)
	}
	if contains(sw, "h264_vaapi") || contains(sw, "-force_key_frames") {
		t.Errorf("software optimize args leaked vaapi/hls flags: %v", sw)
	}
	if sw[len(sw)-1] != "out.mp4" {
		t.Errorf("output path must be last arg: %v", sw)
	}

	vaapi := Encoder{Kind: "vaapi", Device: "/dev/dri/renderD128", Codec: "h264_vaapi"}
	hw := OptimizeArgs(vaapi, "in.avi", "out.mp4")
	if !contains(hw, "-vaapi_device") || !contains(hw, "h264_vaapi") || !contains(hw, "+faststart") {
		t.Errorf("vaapi optimize args missing expected flags: %v", hw)
	}
	// -vaapi_device must precede -i (global option).
	di, ii := indexOf(hw, "-vaapi_device"), indexOf(hw, "-i")
	if di < 0 || ii < 0 || di > ii {
		t.Errorf("-vaapi_device must come before -i: %v", hw)
	}
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

func TestOptimizedSibling(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "(1966) Django.avi")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}

	opt, fresh := OptimizedSibling(src)
	if opt != filepath.Join(dir, "(1966) Django.optimized.mp4") {
		t.Errorf("sibling path = %q", opt)
	}
	if fresh {
		t.Error("no optimized file yet, must not be fresh")
	}

	// Write an optimized copy newer than the source -> fresh.
	if err := os.WriteFile(opt, []byte("optimized"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(opt, future, future); err != nil {
		t.Fatal(err)
	}
	if _, fresh := OptimizedSibling(src); !fresh {
		t.Error("optimized newer than source must be fresh")
	}

	// Make the source newer (re-import) -> stale.
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(src, later, later); err != nil {
		t.Fatal(err)
	}
	if _, fresh := OptimizedSibling(src); fresh {
		t.Error("source newer than optimized must be stale")
	}
}

func TestRenderNodes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"renderD128", "renderD129", "card0", "card1", "by-path"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := renderNodes(dir)
	want := []string{filepath.Join(dir, "renderD128"), filepath.Join(dir, "renderD129")}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("renderNodes = %v, want %v", got, want)
	}
	if nodes := renderNodes(filepath.Join(dir, "does-not-exist")); nodes != nil {
		t.Errorf("renderNodes(missing) = %v, want nil", nodes)
	}
}

func TestEncoderListed(t *testing.T) {
	out := " V....D h264_vaapi           H.264/AVC (VAAPI) (codec h264)\n" +
		" V..... libx264              libx264 H.264 / AVC\n"
	if !encoderListed(out, "h264_vaapi") {
		t.Error("h264_vaapi should be listed")
	}
	if !encoderListed(out, "libx264") {
		t.Error("libx264 should be listed")
	}
	if encoderListed(out, "h264_nvenc") {
		t.Error("h264_nvenc should not be listed")
	}
}

func TestDetectEncoderOff(t *testing.T) {
	got := DetectEncoder(context.Background(), Options{HWAccel: "off"})
	if got.Kind != "software" {
		t.Errorf("hwaccel=off -> %+v, want software (no probing)", got)
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

// TestHLSStartSegment proves the seek-aware launch path: an encoder started at a
// non-zero segment seeks the input there (-ss) and numbers its output from that
// segment (-start_number), without emitting earlier segments. This is the mechanic a
// far-forward seek relies on, exercised deterministically (no dependence on relative
// encode timing).
func TestHLSStartSegment(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}

	dir := t.TempDir()
	in := filepath.Join(dir, "in.avi")
	gen := exec.Command("ffmpeg", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=30:size=160x120:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30",
		"-shortest", in,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate test input: %v\n%s", err, out)
	}

	m := NewManager(Options{})
	defer m.Close()

	runDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.startFFmpeg(ctx, runDir, in, false, 4); err != nil {
		t.Fatalf("startFFmpeg(startSeg=4): %v", err)
	}

	seg4 := filepath.Join(runDir, "seg4.ts")
	deadline := time.Now().Add(20 * time.Second)
	for {
		if fi, err := os.Stat(seg4); err == nil && fi.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("seg4.ts not produced from a startSeg=4 encoder")
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := os.Stat(filepath.Join(runDir, "seg0.ts")); err == nil {
		t.Error("start_number=4 ignored: the run produced seg0.ts")
	}
}

// TestVAAPIEncode is gated on an actually-working VAAPI device: it skips unless
// DetectEncoder finds one. When it does, it confirms a Manager built with that
// hardware encoder produces a valid H.264 segment end to end.
func TestVAAPIEncode(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	enc := DetectEncoder(context.Background(), Options{HWAccel: "auto"})
	if enc.Kind != "vaapi" {
		t.Skip("no working VAAPI device detected")
	}
	t.Logf("detected %s on %s", enc.Codec, enc.Device)

	dir := t.TempDir()
	in := filepath.Join(dir, "in.avi")
	gen := exec.Command("ffmpeg", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-shortest", in,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate test input: %v\n%s", err, out)
	}

	m := NewManager(Options{Encoder: enc})
	defer m.Close()
	if _, err := m.Playlist("k", in); err != nil {
		t.Fatalf("Playlist: %v", err)
	}
	seg, err := m.Segment("k", "seg0.ts")
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if fi, err := os.Stat(seg); err != nil || fi.Size() == 0 {
		t.Fatalf("segment missing or empty: %v", err)
	}
	codec, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "stream=codec_name", "-of", "default=nw=1:nk=1", seg).Output()
	if err != nil {
		t.Fatalf("ffprobe segment: %v", err)
	}
	if !strings.Contains(string(codec), "h264") {
		t.Errorf("hardware segment codec = %q, want h264", strings.TrimSpace(string(codec)))
	}
}
