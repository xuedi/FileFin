package progress

import (
	"strings"
	"testing"
)

func count(s string, r rune) int {
	n := 0
	for _, c := range s {
		if c == r {
			n++
		}
	}
	return n
}

func TestBar(t *testing.T) {
	// pct, expected full cells, expected empty cells
	cases := []struct {
		pct         float64
		full, empty int
	}{
		{-5, 0, Width},
		{150, Width, 0},
		{0, 0, Width},
		{100, Width, 0},
		{5, 1, 19},
		{25, 5, 15},
		{50, 10, 10},
		{75, 15, 5},
		{0.1, 0, 20},
		{99.9, 19, 0}, // 19 full + 1 partial
		{12.5, 2, 17}, // 2 full + 1 partial + 17 empty
	}
	for _, c := range cases {
		bar := Bar(c.pct)
		if n := len([]rune(bar)); n != Width {
			t.Errorf("Bar(%v) width = %d, want %d", c.pct, n, Width)
		}
		if got := count(bar, steps[8]); got != c.full {
			t.Errorf("Bar(%v) full cells = %d, want %d", c.pct, got, c.full)
		}
		if got := count(bar, steps[0]); got != c.empty {
			t.Errorf("Bar(%v) empty cells = %d, want %d", c.pct, got, c.empty)
		}
	}
}

func TestReporterFinalizesWithNewline(t *testing.T) {
	var sb strings.Builder
	r := NewReporter(&sb)
	r.Track("file.avi", 50, 100)
	r.Track("file.avi", 100, 100)
	out := sb.String()
	if !strings.Contains(out, "file.avi") {
		t.Errorf("missing name in output: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("completed copy should end with newline: %q", out)
	}
	if !strings.Contains(out, "100%") {
		t.Errorf("missing 100%% in output: %q", out)
	}
}
