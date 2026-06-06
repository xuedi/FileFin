// Package sysload reads coarse CPU load on Linux, so the optimizer can scale its
// CPU-worker pool up only while the box has headroom and back off when it does not.
// The signal is best-effort: a sampler that cannot read its source reports "unknown"
// rather than failing.
package sysload

import (
	"os"
	"strconv"
	"strings"
)

// CPUSampler computes overall CPU busy percentage from /proc/stat jiffy deltas between
// successive Percent calls. It is not safe for concurrent use.
type CPUSampler struct {
	procStat  string // overridable for tests; "" => /proc/stat
	lastTotal uint64
	lastIdle  uint64
	primed    bool
}

// NewCPUSampler returns a sampler reading the real /proc/stat.
func NewCPUSampler() *CPUSampler { return &CPUSampler{} }

// Percent returns the CPU busy percentage (0-100) over the interval since the previous
// call, and whether a reading is available. The first call after construction primes
// the baseline and returns ok=false; call it once up front, then on each tick.
func (s *CPUSampler) Percent() (pct int, ok bool) {
	total, idle, err := s.read()
	if err != nil {
		return 0, false
	}
	if !s.primed {
		s.lastTotal, s.lastIdle, s.primed = total, idle, true
		return 0, false
	}
	dTotal := total - s.lastTotal
	dIdle := idle - s.lastIdle
	s.lastTotal, s.lastIdle = total, idle
	if dTotal == 0 {
		return 0, false
	}
	busy := float64(dTotal-dIdle) / float64(dTotal) * 100
	return clampPct(busy), true
}

func (s *CPUSampler) read() (total, idle uint64, err error) {
	path := s.procStat
	if path == "" {
		path = "/proc/stat"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	return parseProcStat(string(data))
}

// parseProcStat sums the aggregate "cpu" line fields into total and idle jiffies. The
// idle component is fields idle (index 3) + iowait (index 4), matching common CPU-usage
// math.
func parseProcStat(s string) (total, idle uint64, err error) {
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:] // drop the "cpu" label
		for i, f := range fields {
			v, e := strconv.ParseUint(f, 10, 64)
			if e != nil {
				return 0, 0, e
			}
			total += v
			if i == 3 || i == 4 { // idle, iowait
				idle += v
			}
		}
		return total, idle, nil
	}
	return 0, 0, os.ErrNotExist
}

func clampPct(v float64) int {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return int(v + 0.5)
	}
}
