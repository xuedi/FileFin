// Package progress renders Docker-style braille progress bars for the file copies
// the importers perform when loading media into the data directory.
package progress

import "strings"

// steps fill one cell from empty to full in eighths, using braille dots so a cell
// can show partial progress.
var steps = []rune{'⠀', '⡀', '⡄', '⡆', '⡇', '⣇', '⣧', '⣷', '⣿'}

// Width is the number of cells in a rendered bar.
const Width = 20

// Bar renders a braille progress bar for pct (0-100), always Width runes wide.
func Bar(pct float64) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filledEighths := int(pct * float64(Width) * 8 / 100)
	full := filledEighths / 8
	if full > Width {
		full = Width
	}
	partial := filledEighths % 8

	var b strings.Builder
	b.Grow(Width * 3)
	for i := 0; i < full; i++ {
		b.WriteRune(steps[8])
	}
	remaining := Width - full
	if remaining > 0 {
		if partial > 0 {
			b.WriteRune(steps[partial])
			remaining--
		}
		for i := 0; i < remaining; i++ {
			b.WriteRune(steps[0])
		}
	}
	return b.String()
}
