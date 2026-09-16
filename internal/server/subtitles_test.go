package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// TestRepairSubtitlesEndpoint: the per-media action reports what it wrote and refuses an id
// that is not in the library.
func TestRepairSubtitlesEndpoint(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, bob := installedServer(t, dataDir)
	ctx := context.Background()

	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Movies","alias":"Films"}`, admin); rr.Code != 200 {
		t.Fatalf("create category: %d %s", rr.Code, rr.Body.String())
	}
	dir := filepath.Join(dataDir, "Movies", "(1966) Django")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "(1966) Django.mkv")) // not a real container: nothing to extract
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"title":"Django","year":1966}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s.discoveryTick(ctx)

	pool, _ := s.ensureDB(ctx)
	media, _ := db.AllMedia(ctx, pool)
	if len(media) != 1 {
		t.Fatalf("want the discovered media, got %+v", media)
	}
	id := media[0].ID

	rr := do(t, h, "POST", "/api/admin/media/"+id+"/subtitles", "", admin)
	if rr.Code != 200 {
		t.Fatalf("repair: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"written":0`) {
		t.Fatalf("want nothing written for a file with no tracks, got %s", rr.Body.String())
	}

	if rr := do(t, h, "POST", "/api/admin/media/nosuchid/subtitles", "", admin); rr.Code != 404 {
		t.Fatalf("unknown media: want 404, got %d", rr.Code)
	}
	if rr := do(t, h, "POST", "/api/admin/media/"+id+"/subtitles", "", bob); rr.Code == 200 {
		t.Fatal("a non-admin must not be able to run the repair")
	}
}
