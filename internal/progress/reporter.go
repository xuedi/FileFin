package progress

import (
	"fmt"
	"io"
)

const nameWidth = 32

// Reporter renders a single in-place progress line per file copy, Docker-style:
// the line is rewritten with \r as bytes arrive and finalized with a newline when
// the copy completes. Copies are sequential, so one Reporter drives them all.
type Reporter struct {
	w       io.Writer
	name    string
	lastPct int
}

// NewReporter returns a Reporter that draws to w (typically os.Stderr).
func NewReporter(w io.Writer) *Reporter {
	return &Reporter{w: w, lastPct: -1}
}

// Track is the copy callback: name identifies the file, copied/total are bytes so
// far and the file size (total 0 = unknown). It redraws only when the whole-percent
// changes, so it is cheap to call on every write.
func (r *Reporter) Track(name string, copied, total int64) {
	if name != r.name {
		r.name = name
		r.lastPct = -1
	}
	pct := 0.0
	if total > 0 {
		pct = float64(copied) / float64(total) * 100
	}
	ip := int(pct)
	done := total > 0 && copied >= total
	if ip == r.lastPct && !done {
		return
	}
	r.lastPct = ip

	fmt.Fprintf(r.w, "\r  %-*s %s %3d%%  %s", nameWidth, truncate(name, nameWidth), Bar(pct), ip, sizePair(copied, total))
	if done {
		fmt.Fprintln(r.w)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func sizePair(copied, total int64) string {
	if total <= 0 {
		return humanBytes(copied)
	}
	return fmt.Sprintf("%s / %s", humanBytes(copied), humanBytes(total))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
