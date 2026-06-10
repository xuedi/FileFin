// Package ffrun is the one place the app launches ffmpeg. It captures a bounded tail of
// stderr and wraps a non-zero exit as "ffmpeg: <err>: <last line>", so every caller -
// the optimizer, the thumbnailer, the HLS streamer, the subtitle extractor - reports a
// failure the same compact way instead of each reinventing the capture.
package ffrun

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// stderrCap bounds the captured ffmpeg stderr so a long-running encode never grows it
// without limit; only the tail matters for an error message.
const stderrCap = 4096

// Run executes bin with args and waits for it to finish, capturing stderr. A non-zero
// exit is returned as "ffmpeg: <err>: <last stderr line>". Use this for one-shot encodes
// (thumbnails, subtitle muxing); use Start for a process whose output is consumed live.
func Run(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	stderr := newRingBuffer(stderrCap)
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, lastLine(stderr.String()))
	}
	return nil
}

// Process is a launched ffmpeg whose lifetime the caller manages: the optimizer reads
// Stdout for `-progress` events, the HLS streamer just waits for it (or cancels its ctx).
type Process struct {
	cmd    *exec.Cmd
	stderr *ringBuffer
	// Stdout is the process stdout, for callers parsing `-progress pipe:1`. Callers that
	// do not pass that flag can ignore it; ffmpeg then writes nothing to it.
	Stdout io.ReadCloser
}

// Start launches bin with args, piping stdout and capturing stderr, and returns before
// the process exits. The caller drives it: read Stdout (if parsing progress), then Wait,
// or cancel ctx to stop it.
func Start(ctx context.Context, bin string, args ...string) (*Process, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	stderr := newRingBuffer(stderrCap)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	return &Process{cmd: cmd, stderr: stderr, Stdout: stdout}, nil
}

// Wait waits for the process to exit, wrapping a non-zero exit with the last stderr line.
// A ctx-cancelled process returns the cancellation error from exec; the caller decides
// whether that counts as a failure.
func (p *Process) Wait() error {
	if err := p.cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, lastLine(p.stderr.String()))
	}
	return nil
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

// lastLine returns the last non-empty line of captured output, for a compact one-line
// error rather than a wall of progress text.
func lastLine(s string) string {
	end := len(s)
	for end > 0 && (s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	start := end
	for start > 0 && s[start-1] != '\n' {
		start--
	}
	return s[start:end]
}
