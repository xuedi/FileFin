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
