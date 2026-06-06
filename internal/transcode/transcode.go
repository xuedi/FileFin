// Package transcode streams non-browser-native media as seekable HLS via ffmpeg.
package transcode

import (
	"strings"
	"sync"
)

// directPlay holds the browser-native containers served as-is with byte-range support.
var directPlay = map[string]bool{
	".mp4":  true,
	".webm": true,
	".m4v":  true,
}

// NeedsTranscode reports whether a file with the given extension must be transcoded
// to play in the browser. The extension is matched case-insensitively and may be
// given with or without a leading dot, so filepath.Ext output can be passed directly.
func NeedsTranscode(ext string) bool {
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return !directPlay[ext]
}

// ringBuffer keeps only the last n bytes written to it, for capturing ffmpeg stderr
// without unbounded growth.
type ringBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
}

func newRingBuffer(size int) *ringBuffer { return &ringBuffer{size: size} }

func (b *ringBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.size {
		b.buf = b.buf[len(b.buf)-b.size:]
	}
	return len(p), nil
}

func (b *ringBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
