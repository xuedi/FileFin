package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"filefin/internal/db"
	"filefin/internal/importer"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// seedMedia creates a media folder on disk (with a video file, meta.json, and poster)
// and inserts matching cache rows, returning the media id and its folder path.
func seedMedia(t *testing.T, s *Server, dataDir, category string, catID int64, folder, fileName string, meta importer.Meta) (string, string) {
	t.Helper()
	dir := filepath.Join(dataDir, category, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("video-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "poster.jpg"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := importer.WriteMeta(dir, meta); err != nil {
		t.Fatal(err)
	}

	s.mu.RLock()
	pool := s.db
	s.mu.RUnlock()
	if pool == nil {
		t.Fatal("cache pool not opened (enter an admin page first)")
	}
	ctx := context.Background()
	id := mediaID(category, folder)
	if err := db.InsertMedia(ctx, pool, db.Media{
		ID: id, CategoryID: catID, Path: dir,
		Year: meta.Year, Title: meta.Title, Description: meta.Description, Plot: meta.Plot, Poster: "poster.jpg",
	}); err != nil {
		t.Fatal(err)
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	if err := db.InsertMediaFile(ctx, pool, db.MediaFile{
		MediaID: id, Idx: 0, Path: filepath.Join(dir, fileName), Name: fileName, Ext: ext,
	}); err != nil {
		t.Fatal(err)
	}
	return id, dir
}

// mediaTestServer builds an installed server, creates a category, and opens the cache.
func mediaTestServer(t *testing.T) (*Server, http.Handler, *http.Cookie, string, int64) {
	t.Helper()
	dataDir := t.TempDir()
	s, h, admin, _ := installedServer(t, dataDir)
	// Create the category (also opens + builds the cache pool).
	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Movies","alias":"Films"}`, admin); rr.Code != 200 {
		t.Fatalf("create category: %d %s", rr.Code, rr.Body.String())
	}
	catID := categoryRowID(t, s, "Movies")
	return s, h, admin, dataDir, catID
}

func TestCategoryMediaAndDetail(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	meta := importer.Meta{
		Title: "The Matrix", Year: 1999, Description: "A hacker learns the truth.",
		Plot:     "Neo follows the white rabbit.",
		Metadata: map[string]string{"runtime": "136", "directedBy": "The Wachowskis"},
		Ratings:  map[string]string{"imdb": "8.7"},
		Actors:   []string{"Keanu Reeves"},
		Tags:     []string{"action", "sci-fi"},
	}
	id, _ := seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mkv", meta)

	// Category listing.
	rr := do(t, h, "GET", "/api/category/"+itoa(catID)+"/media", "", admin)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"title":"The Matrix"`) {
		t.Fatalf("category media: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"hasPoster":true`) {
		t.Fatalf("expected poster flag: %s", rr.Body.String())
	}

	// Detail with rich meta.json fields + a transcoded mkv file.
	rr = do(t, h, "GET", "/api/media/"+id, "", admin)
	body := rr.Body.String()
	if rr.Code != 200 {
		t.Fatalf("detail: %d %s", rr.Code, body)
	}
	for _, want := range []string{
		`"title":"The Matrix"`, `"plot":"Neo follows the white rabbit."`,
		`"key":"Runtime"`, `"key":"Directed by"`, `"key":"IMDb"`,
		`"Keanu Reeves"`, `"action"`, `"transcode":true`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail missing %q:\n%s", want, body)
		}
	}
}

func TestFavoriteAndProgressAndHome(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	id, dir := seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mp4",
		importer.Meta{Title: "The Matrix", Year: 1999})

	// Favorite it.
	if rr := do(t, h, "POST", "/api/media/"+id+"/favorite", `{"favorite":true}`, admin); rr.Code != 204 {
		t.Fatalf("favorite: %d %s", rr.Code, rr.Body.String())
	}
	// State lands in meta.json (one unified file), stamped with an update time.
	if m, err := importer.ReadMeta(dir); err != nil || !m.State["admin"].Favorite || m.State["admin"].Updated == 0 {
		t.Fatalf("favorite not recorded in meta.json: %+v err=%v", m.State, err)
	}

	// Progress past 90% -> watched.
	if rr := do(t, h, "POST", "/api/media/"+id+"/progress", `{"file":0,"position":950,"duration":1000}`, admin); rr.Code != 204 {
		t.Fatalf("progress: %d %s", rr.Code, rr.Body.String())
	}

	// Home: appears under favorites and completed (watched), not under continue.
	rr := do(t, h, "GET", "/api/home", "", admin)
	body := rr.Body.String()
	if rr.Code != 200 {
		t.Fatalf("home: %d %s", rr.Code, body)
	}
	// Quick structural checks.
	if !strings.Contains(body, `"favorites":[`) || !strings.Contains(body, `"completed":[`) {
		t.Fatalf("home shape: %s", body)
	}
	if strings.Count(body, `"id":"`+id+`"`) < 2 {
		t.Fatalf("expected item in favorites and completed:\n%s", body)
	}

	// Detail reflects watched + favorite.
	rr = do(t, h, "GET", "/api/media/"+id, "", admin)
	if !strings.Contains(rr.Body.String(), `"watched":true`) || !strings.Contains(rr.Body.String(), `"favorite":true`) {
		t.Fatalf("detail watch state: %s", rr.Body.String())
	}

	// Clear watched -> returns to unwatched, drops from completed.
	if rr := do(t, h, "DELETE", "/api/media/"+id+"/watched", "", admin); rr.Code != 204 {
		t.Fatalf("clear watched: %d", rr.Code)
	}
	rr = do(t, h, "GET", "/api/media/"+id, "", admin)
	if strings.Contains(rr.Body.String(), `"watched":true`) {
		t.Fatalf("still watched after clear: %s", rr.Body.String())
	}
}

func TestPosterAndStream(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	id, _ := seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mp4",
		importer.Meta{Title: "The Matrix", Year: 1999})

	// Poster.
	if rr := do(t, h, "GET", "/api/media/"+id+"/poster", "", admin); rr.Code != 200 || rr.Body.String() != "img" {
		t.Fatalf("poster: %d %q", rr.Code, rr.Body.String())
	}

	// Direct-play range request on the mp4.
	req := httptest.NewRequest("GET", "/api/media/"+id+"/file/0", nil)
	req.AddCookie(admin)
	req.Header.Set("Range", "bytes=0-3")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("range request: %d, want 206\n%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "vide" {
		t.Fatalf("range body: %q", rr.Body.String())
	}
}

func TestStreamTranscodeDisabled(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	id, _ := seedMedia(t, s, dataDir, "Movies", catID, "(2002) Show", "(2002) Show.mkv",
		importer.Meta{Title: "Show", Year: 2002})

	// With transcoding on (default), an mkv 307-redirects to the HLS playlist.
	req := httptest.NewRequest("GET", "/api/media/"+id+"/file/0", nil)
	req.AddCookie(admin)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("mkv stream: %d, want 307", rr.Code)
	}

	// Disable transcoding; the mkv now returns 415.
	if rr := do(t, h, "POST", "/api/admin/settings/transcoding",
		`{"ffmpegPath":"ffmpeg","ffprobePath":"ffprobe","enabled":false}`, admin); rr.Code != 200 {
		t.Fatalf("disable transcoding: %d %s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/media/"+id+"/file/0", nil)
	req.AddCookie(admin)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("disabled transcode: %d, want 415", rr.Code)
	}
}

func TestSubtitleVTT(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	id, dir := seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mp4",
		importer.Meta{Title: "The Matrix", Year: 1999})
	// Drop a sidecar .srt matching the media file's base name.
	srt := "1\n00:00:01,000 --> 00:00:02,000\nHi\n"
	if err := os.WriteFile(filepath.Join(dir, "(1999) The Matrix.en.srt"), []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}

	// Detail surfaces the subtitle track.
	rr := do(t, h, "GET", "/api/media/"+id, "", admin)
	if !strings.Contains(rr.Body.String(), `"lang":"en"`) {
		t.Fatalf("detail subtitle: %s", rr.Body.String())
	}

	// Subtitle endpoint converts to WebVTT.
	rr = do(t, h, "GET", "/api/media/"+id+"/file/0/sub/0", "", admin)
	if rr.Code != 200 || !strings.HasPrefix(rr.Body.String(), "WEBVTT") {
		t.Fatalf("subtitle vtt: %d %q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("subtitle not converted: %q", rr.Body.String())
	}
}
