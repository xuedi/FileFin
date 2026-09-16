package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"filefin/internal/config"
	"filefin/internal/db"
)

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestHasSRTSidecar pins the gate that keeps the sweep cheap: a video with an SRT beside
// it is never probed again, while other sidecar formats do not count because the player
// cannot render them.
func TestHasSRTSidecar(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Show - S1E1.mkv")
	write(t, video)
	if hasSRTSidecar(video) {
		t.Fatal("a bare video should have no sidecar")
	}

	sup := filepath.Join(dir, "Show - S1E1.en.sup") // bitmap: not renderable
	write(t, sup)
	if hasSRTSidecar(video) {
		t.Fatal("a bitmap sidecar should not count as coverage")
	}

	write(t, filepath.Join(dir, "Show - S1E1.en.srt"))
	if !hasSRTSidecar(video) {
		t.Fatal("an SRT sidecar should count as coverage")
	}
}

// TestHasSRTSidecarOtherVideo: a sidecar belonging to a different episode in the same
// folder must not be mistaken for this one's.
func TestHasSRTSidecarOtherVideo(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Show - S1E1.mkv")
	write(t, video)
	write(t, filepath.Join(dir, "Show - S1E2.en.srt"))
	if hasSRTSidecar(video) {
		t.Fatal("another episode's sidecar should not count")
	}
}

// TestRepairSubtitlesNoConfig: a sweep that somehow runs before the config is loaded does
// nothing rather than panicking.
func TestRepairSubtitlesNoConfig(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Show - S1E1.mkv")
	write(t, video)

	s := New()
	files := []db.MediaFile{{Path: video}}
	if n := s.repairSubtitles(context.Background(), files); n != 0 {
		t.Fatalf("want no extraction without config, got %d", n)
	}
}

// TestRepairSubtitlesNoTracks: a file with nothing embedded yields no sidecar, and a
// failing extract is swallowed rather than breaking the sweep.
func TestRepairSubtitlesNoTracks(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Show - S1E1.mkv")
	write(t, video) // not a real container: ffprobe finds no streams

	s := New()
	s.cfg = &config.Config{SubtitleLanguage: "en"}
	files := []db.MediaFile{{Path: video}}
	if n := s.repairSubtitles(context.Background(), files); n != 0 {
		t.Fatalf("want no extraction from a file with no tracks, got %d", n)
	}
	if hasSRTSidecar(video) {
		t.Fatal("a failed extract must not leave a sidecar behind")
	}
}

// TestRepairSubtitlesSkipsCovered: a file that already has an SRT is not even probed, so
// a fake ffmpeg path never gets invoked.
func TestRepairSubtitlesSkipsCovered(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Show - S1E1.mkv")
	write(t, video)
	write(t, filepath.Join(dir, "Show - S1E1.en.srt"))

	s := New()
	s.cfg = &config.Config{FFmpegPath: "/nonexistent/ffmpeg", SubtitleLanguage: "en"}
	files := []db.MediaFile{{Path: video}}
	if n := s.repairSubtitles(context.Background(), files); n != 0 {
		t.Fatalf("want no extraction for a covered file, got %d", n)
	}
}

// TestSweepTrackerGuard: one tracker guards the timer and the button alike, so a rolling
// tick in flight refuses a forced sweep (and says which kind is running) rather than both
// walking the library at once.
func TestSweepTrackerGuard(t *testing.T) {
	var tr sweepTracker
	if !tr.begin(sweepRolling, 64) {
		t.Fatal("first claim should win")
	}
	if tr.begin(sweepFull, 400) {
		t.Fatal("a second claim must be refused while one is in flight")
	}
	if got := tr.snapshot(); got.Scope != sweepRolling || !got.Running || got.Total != 64 {
		t.Fatalf("snapshot %+v does not describe the running rolling sweep", got)
	}

	tr.setTotal(50)
	tr.advance(3)
	if got := tr.snapshot(); got.Total != 50 || got.Done != 1 || got.Subtitles != 3 {
		t.Fatalf("snapshot %+v, want total 50, done 1, subtitles 3", got)
	}

	tr.done()
	if got := tr.snapshot(); got.Running || !got.Finished {
		t.Fatalf("snapshot %+v, want a finished, not-running sweep", got)
	}
	if !tr.begin(sweepFull, 400) {
		t.Fatal("a finished sweep must release the guard")
	}
	if got := tr.snapshot(); got.Scope != sweepFull || got.Done != 0 || got.Finished {
		t.Fatalf("snapshot %+v, want a fresh full sweep", got)
	}
}
