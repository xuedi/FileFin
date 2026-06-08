package transcode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func argIndex(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

func hasArg(args []string, want string) bool { return argIndex(args, want) >= 0 }

func TestOptimizeArgs(t *testing.T) {
	sw := OptimizeArgs(softwareEncoder, "in.avi", "out.mp4")
	if !hasArg(sw, "libx264") || !hasArg(sw, "+faststart") {
		t.Errorf("software optimize args missing libx264/faststart: %v", sw)
	}
	if hasArg(sw, "h264_vaapi") || hasArg(sw, "-force_key_frames") {
		t.Errorf("software optimize args leaked vaapi/hls flags: %v", sw)
	}
	if !hasArg(sw, "-f") || sw[len(sw)-1] != "out.mp4" {
		t.Errorf("output path must be last arg with explicit -f mp4: %v", sw)
	}

	vaapi := Encoder{Kind: "vaapi", Device: "/dev/dri/renderD128", Codec: "h264_vaapi"}
	hw := OptimizeArgs(vaapi, "in.avi", "out.mp4")
	if !hasArg(hw, "-vaapi_device") || !hasArg(hw, "h264_vaapi") || !hasArg(hw, "+faststart") {
		t.Errorf("vaapi optimize args missing expected flags: %v", hw)
	}
	if di, ii := argIndex(hw, "-vaapi_device"), argIndex(hw, "-i"); di < 0 || ii < 0 || di > ii {
		t.Errorf("-vaapi_device must come before -i: %v", hw)
	}
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

	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(src, later, later); err != nil {
		t.Fatal(err)
	}
	if _, fresh := OptimizedSibling(src); fresh {
		t.Error("source newer than optimized must be stale")
	}
}
