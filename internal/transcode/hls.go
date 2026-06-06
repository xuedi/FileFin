package transcode

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	segmentSeconds     = 6
	segmentWaitTimeout = 30 * time.Second
	sessionIdleTimeout = 60 * time.Second
	reapInterval       = 30 * time.Second
)

var segmentName = regexp.MustCompile(`^seg\d+\.ts$`)

// Options configures the external tool paths the Manager invokes.
type Options struct {
	FFmpegPath  string
	FFprobePath string
}

// Manager runs and tracks on-the-fly HLS transcode sessions. Each session owns a
// temp dir of segments produced by one ffmpeg run; idle sessions are reaped.
type Manager struct {
	opts     Options
	mu       sync.Mutex
	sessions map[string]*session
	stop     chan struct{}
}

type session struct {
	dir        string
	duration   float64
	cancel     context.CancelFunc
	lastAccess time.Time
}

// NewManager constructs a Manager and starts its idle-session reaper.
func NewManager(opts Options) *Manager {
	if opts.FFmpegPath == "" {
		opts.FFmpegPath = "ffmpeg"
	}
	if opts.FFprobePath == "" {
		opts.FFprobePath = "ffprobe"
	}
	m := &Manager{opts: opts, sessions: map[string]*session{}, stop: make(chan struct{})}
	go m.reap()
	return m
}

// Close cancels every active session and removes its temp dir.
func (m *Manager) Close() {
	close(m.stop)
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, s := range m.sessions {
		s.cancel()
		_ = os.RemoveAll(s.dir)
		delete(m.sessions, key)
	}
}

// Playlist ensures a session for key/inputPath exists and returns its VOD media
// playlist. The playlist lists every segment up front (with #EXT-X-ENDLIST) so the
// player shows the full seek bar before encoding finishes.
func (m *Manager) Playlist(key, inputPath string) ([]byte, error) {
	s, err := m.ensure(key, inputPath)
	if err != nil {
		return nil, err
	}
	return []byte(buildPlaylist(s.duration)), nil
}

// Segment returns the on-disk path of a named segment for an existing session,
// waiting briefly for the encoder to produce it. name must match seg<N>.ts.
func (m *Manager) Segment(key, name string) (string, error) {
	if !segmentName.MatchString(name) {
		return "", fmt.Errorf("transcode: invalid segment name %q", name)
	}
	m.mu.Lock()
	s := m.sessions[key]
	if s != nil {
		s.lastAccess = time.Now()
	}
	m.mu.Unlock()
	if s == nil {
		return "", fmt.Errorf("transcode: no session for %q", key)
	}

	path := filepath.Join(s.dir, name)
	deadline := time.Now().Add(segmentWaitTimeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("transcode: segment %s not ready", name)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (m *Manager) ensure(key, inputPath string) (*session, error) {
	m.mu.Lock()
	if s := m.sessions[key]; s != nil {
		s.lastAccess = time.Now()
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	// Probe and start outside the lock; ffprobe/ffmpeg launch can be slow.
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 30*time.Second)
	streams, err := Probe(probeCtx, m.opts.FFprobePath, inputPath)
	cancelProbe()
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "filefin-hls-")
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	if err := m.startFFmpeg(runCtx, dir, inputPath, RemuxEligible(streams)); err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return nil, err
	}

	s := &session{dir: dir, duration: streams.Duration, cancel: cancel, lastAccess: time.Now()}

	m.mu.Lock()
	// Another request may have created the session while we were probing.
	if existing := m.sessions[key]; existing != nil {
		m.mu.Unlock()
		cancel()
		_ = os.RemoveAll(dir)
		existing.lastAccess = time.Now()
		return existing, nil
	}
	m.sessions[key] = s
	m.mu.Unlock()
	return s, nil
}

func (m *Manager) startFFmpeg(ctx context.Context, dir, inputPath string, remux bool) error {
	args := []string{"-nostdin", "-i", inputPath}
	if remux {
		args = append(args, "-c", "copy")
	} else {
		args = append(args,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds),
			"-c:a", "aac", "-b:a", "160k", "-ac", "2",
		)
	}
	args = append(args,
		"-sn",
		"-f", "hls",
		"-hls_time", fmt.Sprint(segmentSeconds),
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_segment_type", "mpegts",
		"-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(dir, "seg%d.ts"),
		filepath.Join(dir, "index.m3u8"),
	)

	cmd := exec.CommandContext(ctx, m.opts.FFmpegPath, args...)
	cmd.Stderr = newRingBuffer(4096)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("transcode: start ffmpeg: %w", err)
	}
	go func() { _ = cmd.Wait() }() // reaped by ctx cancel; exit code surfaced via missing segments
	return nil
}

func (m *Manager) reap() {
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.mu.Lock()
			for key, s := range m.sessions {
				if time.Since(s.lastAccess) > sessionIdleTimeout {
					s.cancel()
					_ = os.RemoveAll(s.dir)
					delete(m.sessions, key)
				}
			}
			m.mu.Unlock()
		}
	}
}

func buildPlaylist(duration float64) string {
	n := int(math.Ceil(duration / segmentSeconds))
	if n < 1 {
		n = 1
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:3\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", segmentSeconds)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	for i := 0; i < n; i++ {
		secs := float64(segmentSeconds)
		if i == n-1 {
			if rem := duration - float64(segmentSeconds*(n-1)); rem > 0 {
				secs = rem
			}
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\nseg%d.ts\n", secs, i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}
