package optimize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filefin/internal/db"
)

func TestCandidates(t *testing.T) {
	dir := t.TempDir()
	avi := filepath.Join(dir, "django.avi")
	mp4 := filepath.Join(dir, "matrix.mp4")
	mkvFresh := filepath.Join(dir, "firefly.mkv")
	for _, p := range []string{avi, mp4, mkvFresh} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// firefly already has a fresh optimized sibling.
	opt := filepath.Join(dir, "firefly.optimized.mp4")
	if err := os.WriteFile(opt, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(opt, future, future); err != nil {
		t.Fatal(err)
	}

	files := []db.MediaFile{
		{MediaID: "m", Idx: 0, Path: avi, Ext: ".avi"},
		{MediaID: "m", Idx: 1, Path: mp4, Ext: ".mp4"},      // native, skipped
		{MediaID: "m", Idx: 2, Path: mkvFresh, Ext: ".mkv"}, // fresh sibling, skipped
	}
	got := Candidates(files)
	if len(got) != 1 || got[0].Source != avi || got[0].FileIdx != 0 {
		t.Fatalf("Candidates = %+v, want only the avi", got)
	}
	if got[0].Optimized != filepath.Join(dir, "django.optimized.mp4") {
		t.Errorf("optimized path = %q", got[0].Optimized)
	}
}

func TestScanProgress(t *testing.T) {
	block := strings.Join([]string{
		"frame=10",
		"out_time_ms=5000000", // 5s of a 10s file => 50%
		"progress=continue",
		"out_time_ms=10000000", // 10s => 100%
		"progress=end",
	}, "\n")
	var pcts []int
	scanProgress(strings.NewReader(block), 10, func(p int) { pcts = append(pcts, p) })
	if len(pcts) == 0 || pcts[len(pcts)-1] != 100 {
		t.Fatalf("progress percents = %v, want last 100", pcts)
	}
	saw50 := false
	for _, p := range pcts {
		if p == 50 {
			saw50 = true
		}
	}
	if !saw50 {
		t.Errorf("expected a 50%% reading, got %v", pcts)
	}
}

func TestScanProgressNoDuration(t *testing.T) {
	block := "out_time_ms=5000000\nprogress=end\n"
	var pcts []int
	scanProgress(strings.NewReader(block), 0, func(p int) { pcts = append(pcts, p) })
	// No duration: no percentage readings, but the end still reports 100.
	if len(pcts) != 1 || pcts[0] != 100 {
		t.Fatalf("percents = %v, want [100]", pcts)
	}
}

func TestSweepStaleLocks(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "Movies", "(1966) Django")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(sub, "(1966) Django.optimized.mp4.tmp")
	keep := filepath.Join(sub, "(1966) Django.optimized.mp4")
	src := filepath.Join(sub, "(1966) Django.avi")
	for _, p := range []string{lock, keep, src} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	n, err := SweepStaleLocks(dir)
	if err != nil || n != 1 {
		t.Fatalf("SweepStaleLocks = %d (%v), want 1", n, err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Error("the .tmp lock should have been removed")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("the .optimized.mp4 must be left alone")
	}
}
