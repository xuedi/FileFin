package transcode

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestInspect(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH")
	}

	dir := t.TempDir()
	in := filepath.Join(dir, "in.mkv")
	gen := exec.Command("ffmpeg", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-shortest", in,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate test input: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mi, err := Inspect(ctx, "ffprobe", in)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if mi.VideoCodec == "" {
		t.Errorf("VideoCodec empty: %+v", mi)
	}
	if mi.AudioCodec == "" {
		t.Errorf("AudioCodec empty: %+v", mi)
	}
	if mi.Width != 160 || mi.Height != 120 {
		t.Errorf("resolution = %dx%d, want 160x120", mi.Width, mi.Height)
	}
	if mi.Size <= 0 {
		t.Errorf("Size = %d, want > 0", mi.Size)
	}
	if mi.Container == "" {
		t.Errorf("Container empty: %+v", mi)
	}
}
