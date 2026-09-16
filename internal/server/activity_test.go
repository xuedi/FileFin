package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDrainTracker: a queue has no denominator of its own, so the tracker gives it one -
// the run starts when the queue fills, fills up as it drains, grows rather than rewinds when
// a refill adds more, and ends when the queue empties.
func TestDrainTracker(t *testing.T) {
	d := newDrainTracker()

	if _, _, ok := d.observe("optimize", 0); ok {
		t.Fatal("an empty queue is not a run")
	}

	done, total, ok := d.observe("optimize", 10)
	if !ok || done != 0 || total != 10 {
		t.Fatalf("observe(10) = %d/%d ok=%v, want 0/10 running", done, total, ok)
	}
	if done, total, _ = d.observe("optimize", 4); done != 6 || total != 10 {
		t.Fatalf("observe(4) = %d/%d, want 6/10", done, total)
	}
	// A refill mid-drain raises the total instead of sending the bar backwards.
	if done, total, _ = d.observe("optimize", 15); done != 0 || total != 15 {
		t.Fatalf("observe(15) = %d/%d, want 0/15", done, total)
	}
	if _, _, ok = d.observe("optimize", 0); ok {
		t.Fatal("a drained queue ends the run")
	}
	// ...and the next non-empty queue is a fresh run, not a continuation.
	if done, total, _ = d.observe("optimize", 3); done != 0 || total != 3 {
		t.Fatalf("observe(3) = %d/%d, want a fresh 0/3", done, total)
	}
}

// TestDrainTrackerPerKind: the queues are tracked independently.
func TestDrainTrackerPerKind(t *testing.T) {
	d := newDrainTracker()
	d.observe("optimize", 10)
	d.observe("probe", 2)
	if done, total, _ := d.observe("optimize", 9); done != 1 || total != 10 {
		t.Fatalf("optimize = %d/%d, want 1/10", done, total)
	}
	if done, total, _ := d.observe("probe", 1); done != 1 || total != 2 {
		t.Fatalf("probe = %d/%d, want 1/2", done, total)
	}
}

// TestBytePercent guards the empty-total and over-100 cases of a copy's progress.
func TestBytePercent(t *testing.T) {
	cases := []struct {
		copied, total int64
		want          int
	}{{0, 0, 0}, {5, 0, 0}, {0, 10, 0}, {5, 10, 50}, {10, 10, 100}, {11, 10, 100}}
	for _, c := range cases {
		if got := bytePercent(c.copied, c.total); got != c.want {
			t.Errorf("bytePercent(%d, %d) = %d, want %d", c.copied, c.total, got, c.want)
		}
	}
}

// TestActivityEndpoint drives the Progress page's one endpoint end to end: a discovery tick
// over a fresh media folder queues work, and the snapshot reports it as a run with a real
// denominator while nothing is yet being worked on.
func TestActivityEndpoint(t *testing.T) {
	dataDir := t.TempDir()
	s, h, admin, _ := installedServer(t, dataDir)
	ctx := context.Background()

	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Movies","alias":"Films"}`, admin); rr.Code != 200 {
		t.Fatalf("create category: %d %s", rr.Code, rr.Body.String())
	}
	dir := filepath.Join(dataDir, "Movies", "(1966) Django")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "(1966) Django.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"title":"Django","year":1966}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s.discoveryTick(ctx) // discovers the folder and refills the queues

	rr := do(t, h, "GET", "/api/admin/activity", "", admin)
	if rr.Code != 200 {
		t.Fatalf("activity: %d %s", rr.Code, rr.Body.String())
	}
	var snap activitySnapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode activity: %v (%s)", err, rr.Body.String())
	}
	// Both lists must marshal as arrays, never null, so the page can iterate them blindly.
	if !strings.Contains(rr.Body.String(), `"activity":[`) || !strings.Contains(rr.Body.String(), `"runs":[`) {
		t.Fatalf("activity/runs must be arrays: %s", rr.Body.String())
	}
	if len(snap.Activity) != 0 {
		t.Fatalf("nothing is being worked on yet, got %+v", snap.Activity)
	}
	// The probe queue is filled for any new media (its format is unknown), so its drain is
	// a run with a real denominator.
	var found bool
	for _, r := range snap.Runs {
		if r.Kind == runProbe {
			found = true
			if r.Total == 0 {
				t.Fatalf("a queued run needs a denominator: %+v", r)
			}
		}
	}
	if !found {
		t.Fatalf("want a probe drain among the runs, got %+v", snap.Runs)
	}
}
