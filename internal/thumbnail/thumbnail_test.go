package thumbnail

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNames(t *testing.T) {
	if got := DetailName(); got != "poster_280.webp" {
		t.Errorf("DetailName() = %q, want poster_280.webp", got)
	}
	if got := TileName(); got != "poster_180.webp" {
		t.Errorf("TileName() = %q, want poster_180.webp", got)
	}
}

// ffmpegWithWebP returns the ffmpeg path when it exists and can encode WebP, else "".
func ffmpegWithWebP(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	out, err := exec.Command(bin, "-hide_banner", "-encoders").Output()
	if err != nil || !strings.Contains(string(out), "libwebp") {
		return ""
	}
	return bin
}

func TestImagePipelines(t *testing.T) {
	ffmpeg := ffmpegWithWebP(t)
	if ffmpeg == "" {
		t.Skip("ffmpeg with libwebp not available")
	}
	ctx := context.Background()
	dir := t.TempDir()

	// A 400x600 base poster (2:3 already), plus a short test video.
	src := filepath.Join(dir, "poster.png")
	if err := exec.Command(ffmpeg, "-y", "-f", "lavfi", "-i", "color=c=red:s=400x600",
		"-frames:v", "1", src).Run(); err != nil {
		t.Fatalf("make source image: %v", err)
	}
	video := filepath.Join(dir, "clip.mp4")
	if err := exec.Command(ffmpeg, "-y", "-f", "lavfi", "-i", "testsrc=d=5:s=320x240",
		"-pix_fmt", "yuv420p", video).Run(); err != nil {
		t.Fatalf("make source video: %v", err)
	}

	detail := filepath.Join(dir, DetailName())
	if err := Detail(ctx, ffmpeg, src, detail); err != nil {
		t.Fatalf("Detail: %v", err)
	}
	tile := filepath.Join(dir, TileName())
	if err := Tile(ctx, ffmpeg, src, tile); err != nil {
		t.Fatalf("Tile: %v", err)
	}
	frame := filepath.Join(dir, "poster.webp")
	if err := FramePoster(ctx, ffmpeg, video, frame); err != nil {
		t.Fatalf("FramePoster: %v", err)
	}

	for _, p := range []string{detail, tile, frame} {
		fi, err := os.Stat(p)
		if err != nil || fi.Size() == 0 {
			t.Errorf("expected non-empty output at %s (err=%v)", p, err)
		}
		if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
			t.Errorf("temp file left behind for %s", p)
		}
	}
}
