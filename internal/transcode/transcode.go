// Package transcode streams non-browser-native media as seekable HLS via ffmpeg.
package transcode

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// OptimizedExt is the suffix of a pre-transcoded, browser-direct-play copy of a source
// media file, stored beside it as `<source-base>.optimized.mp4`.
const OptimizedExt = ".optimized.mp4"

// OptimizedSibling returns the path of the optimized direct-play copy for srcPath and
// whether that copy currently exists and is fresh (at least as new as the source, so a
// re-imported source invalidates a stale copy). The path is returned even when not
// fresh, so callers can use it as the target to (re)produce.
func OptimizedSibling(srcPath string) (path string, fresh bool) {
	base := strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))
	path = filepath.Join(filepath.Dir(srcPath), base+OptimizedExt)
	of, err := os.Stat(path)
	if err != nil {
		return path, false
	}
	sf, err := os.Stat(srcPath)
	if err != nil {
		return path, false
	}
	return path, !of.ModTime().Before(sf.ModTime())
}

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
