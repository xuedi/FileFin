package optimize

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filefin/internal/logging"
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

func TestSweepStaleLocks(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "cat", "(1966) Django")
	if err := os.MkdirAll(media, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := []string{
		filepath.Join(media, "(1966) Django.avi"),
		filepath.Join(media, "(1966) Django.optimized.mp4"),
		filepath.Join(media, "notes.txt"),
	}
	locks := []string{
		filepath.Join(media, "(1966) Django.optimized.mp4.tmp"),
		filepath.Join(dir, "cat", "(1970) X.optimized.mp4.tmp"),
	}
	for _, p := range append(append([]string{}, keep...), locks...) {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	n, err := SweepStaleLocks(dir)
	if err != nil || n != 2 {
		t.Fatalf("SweepStaleLocks = %d (err=%v), want 2", n, err)
	}
	for _, p := range keep {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("swept a non-lock file: %s", p)
		}
	}
	for _, p := range locks {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("lock not removed: %s", p)
		}
	}
}

// TestProcessOneSkipsClaimed verifies the temp-as-lock claim: a candidate whose lock
// already exists is left untouched (no encode, no final copy), so two workers/processes
// never do the same work.
func TestProcessOneSkipsClaimed(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "(1966) Django.avi")
	if err := os.WriteFile(src, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	opt, _ := transcode.OptimizedSibling(src)
	lock := opt + ".tmp"
	if err := os.WriteFile(lock, []byte("claimed"), 0o644); err != nil {
		t.Fatal(err)
	}

	// FFmpeg/FFprobe would error if invoked; the claim must short-circuit before then.
	w := NewWorker(Options{DataDir: dir, FFmpeg: "false", FFprobe: "false"})
	if err := w.processOne(context.Background(), Candidate{Source: src, Optimized: opt}, transcode.SoftwareEncoder()); err != nil {
		t.Fatalf("processOne on a claimed item: %v", err)
	}
	if b, _ := os.ReadFile(lock); string(b) != "claimed" {
		t.Errorf("another worker's lock was modified: %q", b)
	}
	if _, err := os.Stat(opt); err == nil {
		t.Error("optimized copy should not be produced for a claimed item")
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

	var logbuf bytes.Buffer
	w := NewWorker(Options{DataDir: dir, Logger: logging.New(logging.Info, &logbuf)})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	opt, fresh := transcode.OptimizedSibling(src)
	if !fresh {
		t.Fatalf("optimized copy missing or stale: %s", opt)
	}
	if !strings.Contains(logbuf.String(), "optimizer: new optimized version of") {
		t.Errorf("missing optimizer event: %q", logbuf.String())
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

// TestWorkerParallel is gated on ffmpeg: it runs several inputs through the adaptive pool
// with room for multiple workers and asserts every direct-play copy is produced and no
// stale lock files remain.
func TestWorkerParallel(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH")
	}

	dir := filepath.Join(t.TempDir(), "data")
	var srcs []string
	for i := 0; i < 4; i++ {
		media := filepath.Join(dir, "cat", "(196"+string(rune('0'+i))+") Film")
		if err := os.MkdirAll(media, 0o755); err != nil {
			t.Fatal(err)
		}
		src := filepath.Join(media, filepath.Base(media)+".avi")
		gen := exec.Command("ffmpeg", "-nostdin", "-y",
			"-f", "lavfi", "-i", "testsrc=duration=1:size=160x120:rate=15",
			"-c:v", "mpeg4", src,
		)
		if out, err := gen.CombinedOutput(); err != nil {
			t.Fatalf("generate input %d: %v\n%s", i, err, out)
		}
		srcs = append(srcs, src)
	}

	w := NewWorker(Options{DataDir: dir, MaxWorkers: 4, Logger: logging.New(logging.Info, new(bytes.Buffer))})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	for _, src := range srcs {
		if _, fresh := transcode.OptimizedSibling(src); !fresh {
			t.Errorf("missing/stale optimized copy for %s", src)
		}
	}
	if n, _ := SweepStaleLocks(dir); n != 0 {
		t.Errorf("%d stale lock(s) left after a clean run", n)
	}
}
