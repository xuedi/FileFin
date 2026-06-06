package optimize

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"filefin/internal/model"
	"filefin/internal/transcode"
)

// scanOf builds a one-category, one-media Scan over the given file paths.
func scanOf(paths ...string) *model.Scan {
	var files []model.MediaFile
	for _, p := range paths {
		files = append(files, model.MediaFile{Path: p, Name: filepath.Base(p), Ext: filepath.Ext(p)})
	}
	return &model.Scan{Categories: []model.Category{{
		Name:  "cat",
		Media: []model.Media{{Files: files}},
	}}}
}

func TestWorkList(t *testing.T) {
	dir := t.TempDir()
	native := filepath.Join(dir, "a.mp4") // browser-native: never optimized
	needs := filepath.Join(dir, "b.avi")  // non-native, no optimized copy: included
	done := filepath.Join(dir, "c.avi")   // non-native, fresh optimized copy: excluded
	for _, p := range []string{native, needs, done} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Give c.avi a fresh optimized sibling.
	cOpt, _ := transcode.OptimizedSibling(done)
	if err := os.WriteFile(cOpt, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(cOpt, future, future); err != nil {
		t.Fatal(err)
	}

	work := WorkList(scanOf(native, needs, done))
	if len(work) != 1 || work[0].Source != needs {
		t.Fatalf("WorkList = %+v, want only %s", work, needs)
	}
	wantOpt, _ := transcode.OptimizedSibling(needs)
	if work[0].Optimized != wantOpt {
		t.Errorf("Optimized target = %q, want %q", work[0].Optimized, wantOpt)
	}
}

// TestWorkerEndToEnd is gated on ffmpeg: it optimizes a generated non-native clip and
// asserts a fresh, browser-direct-play H.264 copy is produced.
func TestWorkerEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH")
	}

	dir := t.TempDir()
	media := filepath.Join(dir, "cat", "(1966) Django")
	if err := os.MkdirAll(media, 0o755); err != nil {
		t.Fatal(err)
	}
	// mpeg4 .avi -> genuinely needs re-encoding (not remux-eligible).
	src := filepath.Join(media, "(1966) Django.avi")
	gen := exec.Command("ffmpeg", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-shortest", "-c:v", "mpeg4", src,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate input: %v\n%s", err, out)
	}

	w := NewWorker(Options{DataDir: dir})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	opt, fresh := transcode.OptimizedSibling(src)
	if !fresh {
		t.Fatalf("optimized copy missing or stale: %s", opt)
	}
	codec, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "stream=codec_name", "-of", "default=nw=1:nk=1", opt).Output()
	if err != nil || len(codec) == 0 {
		t.Fatalf("ffprobe optimized: %v", err)
	}
	if got := string(codec); got[:4] != "h264" {
		t.Errorf("optimized video codec = %q, want h264", got)
	}
}
