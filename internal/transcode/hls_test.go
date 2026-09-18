package transcode

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRemuxSegments(t *testing.T) {
	got := remuxSegments([]float64{0, 10, 20, 30}, 35)
	want := []float64{10, 10, 10, 5}
	if !floatsEqual(got, want) {
		t.Errorf("10s keyframes: got %v, want %v", got, want)
	}
	// Dense keyframes collapse onto the 6s grid.
	got = remuxSegments([]float64{0, 2, 6, 8, 12, 18}, 20)
	want = []float64{6, 6, 6, 2}
	if !floatsEqual(got, want) {
		t.Errorf("dense keyframes: got %v, want %v", got, want)
	}
}

func TestBuildPlaylistSegments(t *testing.T) {
	pl := buildPlaylist(25, []float64{10, 10, 5})
	for _, want := range []string{"#EXT-X-TARGETDURATION:10", "#EXTINF:10.000,\nseg1.ts", "#EXTINF:5.000,\nseg2.ts"} {
		if !contains(pl, want) {
			t.Errorf("playlist missing %q:\n%s", want, pl)
		}
	}
	if contains(pl, "seg3.ts") {
		t.Errorf("playlist lists a segment ffmpeg will not write:\n%s", pl)
	}
}

// TestRemuxPlanMatchesFFmpeg stream-copies a source with irregular keyframes through the
// same HLS muxer settings the Manager uses and checks the up-front plan against the
// segments ffmpeg actually wrote.
func TestRemuxPlanMatchesFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=25",
		"-f", "lavfi", "-i", "sine", "-t", "60", "-c:v", "libx264", "-preset", "ultrafast",
		"-x264-params", "keyint=1000:min-keyint=1000:scenecut=0",
		"-force_key_frames", "0,4,7,13,14,25,31,40,52", "-c:a", "aac", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("cannot build test source (libx264?): %v %s", err, out)
	}

	m := NewManager(Options{})
	defer m.Close()
	streams, err := Probe(context.Background(), "ffprobe", src)
	if err != nil {
		t.Fatal(err)
	}
	plan := m.remuxPlan(src, streams.Duration)
	if plan == nil {
		t.Fatal("no remux plan")
	}

	out := filepath.Join(dir, "hls")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	run, err := m.startFFmpeg(context.Background(), out, src, "test", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	<-run.done
	if run.failed() {
		t.Fatal(run.err)
	}
	actual := playlistDurations(t, filepath.Join(out, "index.m3u8"))
	if len(plan) != len(actual) {
		t.Fatalf("plan has %d segments %v, ffmpeg wrote %d %v", len(plan), plan, len(actual), actual)
	}
	for i := range plan {
		if math.Abs(plan[i]-actual[i]) > 0.1 {
			t.Errorf("segment %d: plan %.3f, ffmpeg %.3f", i, plan[i], actual[i])
		}
	}
}

func playlistDurations(t *testing.T, path string) []float64 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d []float64
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
			f, err := strconv.ParseFloat(strings.TrimSuffix(v, ","), 64)
			if err != nil {
				t.Fatal(err)
			}
			d = append(d, f)
		}
	}
	return d
}

func floatsEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-9 {
			return false
		}
	}
	return true
}
