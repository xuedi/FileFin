package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPlaybackTarget(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "(1966) Django.avi")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No sibling: a non-native source needs transcode, a native one does not.
	if serve, need := playbackTarget(src, ".avi"); serve != src || !need {
		t.Errorf("no sibling avi: serve=%q need=%v", serve, need)
	}
	mp4 := filepath.Join(dir, "clip.mp4")
	if serve, need := playbackTarget(mp4, ".mp4"); serve != mp4 || need {
		t.Errorf("native mp4: serve=%q need=%v", serve, need)
	}

	// A fresh optimized sibling is served directly without transcode.
	opt := filepath.Join(dir, "(1966) Django.optimized.mp4")
	if err := os.WriteFile(opt, []byte("optimized"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(opt, future, future); err != nil {
		t.Fatal(err)
	}
	if serve, need := playbackTarget(src, ".avi"); serve != opt || need {
		t.Errorf("fresh sibling: serve=%q need=%v, want %q false", serve, need, opt)
	}
}

func TestIsOptimizedSibling(t *testing.T) {
	cases := map[string]bool{
		"film.optimized.mp4":     true,
		"film.optimized.mp4.tmp": true,
		"FILM.OPTIMIZED.MP4":     true,
		"film.mp4":               false,
		"film.mkv":               false,
		"optimized.mp4":          false,
	}
	for name, want := range cases {
		if got := isOptimizedSibling(name); got != want {
			t.Errorf("isOptimizedSibling(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestScanFolderFilesIgnoresOptimized(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"movie.mkv", "movie.optimized.mp4", "movie.optimized.mp4.tmp", "poster.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	videos, poster := scanFolderFiles(dir)
	if len(videos) != 1 || filepath.Base(videos[0]) != "movie.mkv" {
		t.Errorf("videos = %v, want only movie.mkv", videos)
	}
	if poster != "poster.jpg" {
		t.Errorf("poster = %q, want poster.jpg", poster)
	}
}
