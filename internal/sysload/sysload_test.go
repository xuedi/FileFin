package sysload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProcStat(t *testing.T) {
	// cpu  user nice system idle iowait irq softirq steal ...
	total, idle, err := parseProcStat("cpu  100 0 50 800 50 0 0 0 0 0\ncpu0 1 2 3 4\n")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1000 { // 100+0+50+800+50
		t.Errorf("total = %d, want 1000", total)
	}
	if idle != 850 { // idle 800 + iowait 50
		t.Errorf("idle = %d, want 850", idle)
	}
}

func TestCPUSamplerDelta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat")
	s := &CPUSampler{procStat: path}

	if err := os.WriteFile(path, []byte("cpu  0 0 0 100 0 0 0 0 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Percent(); ok {
		t.Fatal("first call should prime the baseline and report ok=false")
	}
	// +50 busy, +50 idle over the interval => 50% busy.
	if err := os.WriteFile(path, []byte("cpu  50 0 0 150 0 0 0 0 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pct, ok := s.Percent()
	if !ok || pct != 50 {
		t.Fatalf("Percent = %d (ok=%v), want 50", pct, ok)
	}
}

func TestCPUSamplerMissingFile(t *testing.T) {
	s := &CPUSampler{procStat: filepath.Join(t.TempDir(), "nope")}
	if _, ok := s.Percent(); ok {
		t.Fatal("unreadable /proc/stat should report ok=false")
	}
}
