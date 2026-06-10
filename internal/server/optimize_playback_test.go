package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"filefin/internal/db"
)

func TestPlaybackTarget(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "(1966) Django.avi")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No sibling, no probed format: fall back to the extension - a non-native source
	// needs transcode, a native one does not.
	if serve, need := playbackTarget(db.MediaFile{Path: src, Ext: ".avi"}); serve != src || !need {
		t.Errorf("no sibling avi: serve=%q need=%v", serve, need)
	}
	mp4 := filepath.Join(dir, "clip.mp4")
	if serve, need := playbackTarget(db.MediaFile{Path: mp4, Ext: ".mp4"}); serve != mp4 || need {
		t.Errorf("native mp4: serve=%q need=%v", serve, need)
	}

	// An `.avi`-named file probed as H.264/MP4 direct-plays despite the extension.
	probed := db.MediaFile{Path: src, Ext: ".avi", Container: "mov,mp4,m4a,3gp,3g2,mj2", VideoCodec: "h264", AudioCodec: "aac"}
	if serve, need := playbackTarget(probed); serve != src || need {
		t.Errorf("probed h264 avi: serve=%q need=%v, want %q false", serve, need, src)
	}
	// An `.mp4`-named file probed as HEVC must transcode despite the native extension.
	hevc := db.MediaFile{Path: mp4, Ext: ".mp4", Container: "mov,mp4,m4a,3gp,3g2,mj2", VideoCodec: "hevc", AudioCodec: "aac"}
	if _, need := playbackTarget(hevc); !need {
		t.Errorf("probed hevc mp4: need=%v, want true", need)
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
	if serve, need := playbackTarget(db.MediaFile{Path: src, Ext: ".avi"}); serve != opt || need {
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
