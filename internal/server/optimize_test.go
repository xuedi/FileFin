package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"filefin/internal/config"
	"filefin/internal/db"
)

// seedOptimizeMedia creates a media folder with one transcode-needing source and one
// browser-native file, registers both in the cache, and returns the avi source path.
func seedOptimizeMedia(t *testing.T, s *Server) (ctx context.Context, avi string) {
	t.Helper()
	c := context.Background()
	p, err := s.ensureDB(c)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.cfg.DataDir, "Movies", "(1966) Django")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	avi = filepath.Join(dir, "(1966) Django.avi")
	mp4 := filepath.Join(dir, "clip.mp4")
	for _, f := range []string{avi, mp4} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InsertMedia(c, p, db.Media{ID: "m1", Path: dir, Title: "Django"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertMediaFile(c, p, db.MediaFile{MediaID: "m1", Idx: 0, Path: avi, Name: "(1966) Django.avi", Ext: ".avi"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertMediaFile(c, p, db.MediaFile{MediaID: "m1", Idx: 1, Path: mp4, Name: "clip.mp4", Ext: ".mp4"}); err != nil {
		t.Fatal(err)
	}
	return c, avi
}

func TestOptimizeScanEndpoint(t *testing.T) {
	s, h, admin, bob := installedServer(t, t.TempDir())
	ctx, _ := seedOptimizeMedia(t, s)
	pool, _ := s.ensureDB(ctx)

	if rr := do(t, h, "POST", "/api/admin/optimize/scan", "", bob); rr.Code != 403 {
		t.Fatalf("non-admin scan: %d, want 403", rr.Code)
	}
	rr := do(t, h, "POST", "/api/admin/optimize/scan", "", admin)
	if rr.Code != 200 {
		t.Fatalf("scan: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Candidates int `json:"candidates"`
		Pending    int `json:"pending"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Candidates != 1 || resp.Pending != 1 {
		t.Fatalf("scan result = %+v, want 1 candidate / 1 pending", resp)
	}
	if n, _ := db.CountPending(ctx, pool); n != 1 {
		t.Fatalf("pending after scan = %d, want 1", n)
	}
}

func TestOptimizeRefill(t *testing.T) {
	s, _, _, _ := installedServer(t, t.TempDir())
	ctx, avi := seedOptimizeMedia(t, s)
	pool, _ := s.ensureDB(ctx)

	// First refill: only the non-native source without a sibling becomes a pending task.
	s.optimizeRefill(ctx)
	if n, _ := db.CountPending(ctx, pool); n != 1 {
		t.Fatalf("pending after first refill = %d, want 1", n)
	}
	// Idempotent: a second refill does not duplicate.
	s.optimizeRefill(ctx)
	if n, _ := db.CountPending(ctx, pool); n != 1 {
		t.Fatalf("pending after second refill = %d, want 1", n)
	}

	// Once a fresh optimized sibling exists, the planner prunes the task.
	opt := filepath.Join(filepath.Dir(avi), "(1966) Django.optimized.mp4")
	if err := os.WriteFile(opt, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(opt, future, future); err != nil {
		t.Fatal(err)
	}
	s.optimizeRefill(ctx)
	if n, _ := db.CountPending(ctx, pool); n != 0 {
		t.Fatalf("pending after sibling appears = %d, want 0", n)
	}
}

func TestOptimizeModeNoneCreatesNoTasks(t *testing.T) {
	s, _, _, _ := installedServer(t, t.TempDir())
	ctx, _ := seedOptimizeMedia(t, s)
	pool, _ := s.ensureDB(ctx)

	s.cfg.OptimizeMode = config.OptimizeNone
	wg := &sync.WaitGroup{}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startOptimizeRun(runCtx, wg)
	// No planner is launched in none mode, so nothing is ever queued.
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()
	if n, _ := db.CountPending(ctx, pool); n != 0 {
		t.Fatalf("none mode queued %d tasks, want 0", n)
	}
}

func TestSetOptimizerEndpoint(t *testing.T) {
	s, h, admin, bob := installedServer(t, t.TempDir())

	if rr := do(t, h, "POST", "/api/admin/settings/optimizer", `{"mode":"all"}`, bob); rr.Code != 403 {
		t.Fatalf("non-admin set optimizer: %d, want 403", rr.Code)
	}
	rr := do(t, h, "POST", "/api/admin/settings/optimizer", `{"mode":"all"}`, admin)
	if rr.Code != 200 {
		t.Fatalf("set optimizer all: %d %s", rr.Code, rr.Body.String())
	}
	if s.cfg.OptimizeMode != "all" {
		t.Fatalf("optimize mode not persisted: %q", s.cfg.OptimizeMode)
	}
	got, _ := config.Load()
	if got.OptimizeMode != "all" {
		t.Fatalf("optimize mode not saved to disk: %q", got.OptimizeMode)
	}
	if rr := do(t, h, "POST", "/api/admin/settings/optimizer", `{"mode":"bogus"}`, admin); rr.Code != 400 {
		t.Fatalf("bad mode: %d, want 400", rr.Code)
	}
}

func TestActiveOptimizeEndpoint(t *testing.T) {
	s, h, admin, bob := installedServer(t, t.TempDir())
	ctx, _ := seedOptimizeMedia(t, s)
	pool, _ := s.ensureDB(ctx)
	if err := db.UpsertPendingTask(ctx, pool, "m1", 0, "/src/(1966) Django.avi", "/src/(1966) Django.optimized.mp4"); err != nil {
		t.Fatal(err)
	}
	tk, _, err := db.ClaimNextTask(ctx, pool, "GPU")
	if err != nil {
		t.Fatal(err)
	}
	s.setOptPercent(tk.ID, 77) // live overlay should win over the DB mirror

	if rr := do(t, h, "GET", "/api/admin/optimize/active", "", bob); rr.Code != 403 {
		t.Fatalf("non-admin active: %d, want 403", rr.Code)
	}
	rr := do(t, h, "GET", "/api/admin/optimize/active", "", admin)
	if rr.Code != 200 {
		t.Fatalf("active: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Active []struct {
			ID      int64  `json:"id"`
			Title   string `json:"title"`
			File    string `json:"file"`
			Agent   string `json:"agent"`
			Percent int    `json:"percent"`
		} `json:"active"`
		Pending int `json:"pending"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Active) != 1 {
		t.Fatalf("active = %+v, want 1", resp.Active)
	}
	a := resp.Active[0]
	if a.Title != "Django" || a.File != "(1966) Django.avi" || a.Agent != "GPU" || a.Percent != 77 {
		t.Fatalf("active task = %+v", a)
	}
	if resp.Pending != 0 {
		t.Fatalf("pending = %d, want 0", resp.Pending)
	}
}
