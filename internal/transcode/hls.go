package transcode

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"filefin/internal/ffrun"
	"filefin/internal/logging"
)

const (
	segmentSeconds     = 6
	segmentWaitTimeout = 30 * time.Second
	sessionIdleTimeout = 60 * time.Second
	reapInterval       = 30 * time.Second
	// repositionLead is how many segments past the encode head a request may be before
	// it counts as a seek (rather than normal prebuffer) and relaunches the encoder.
	repositionLead = 3
)

var segmentName = regexp.MustCompile(`^seg\d+\.ts$`)

// Options configures the external tool paths the Manager invokes and the video
// encoder it uses.
type Options struct {
	FFmpegPath  string
	FFprobePath string

	// HWAccel ("auto"|"off") and HWAccelDevice steer DetectEncoder; Encoder is the
	// detected result the Manager encodes with (zero value falls back to software).
	HWAccel       string
	HWAccelDevice string
	Encoder       Encoder

	// Logger receives frontend playback events (watching, seek); may be nil.
	Logger *logging.Logger
}

// Manager runs and tracks on-the-fly HLS transcode sessions. Each session owns a
// temp dir of segments produced by one ffmpeg run; idle sessions are reaped.
type Manager struct {
	opts     Options
	encoder  Encoder
	log      *logging.Scoped
	mu       sync.Mutex
	sessions map[string]*session
	stop     chan struct{}
}

type session struct {
	dir        string
	inputPath  string
	title      string // display label for playback log events
	remux      bool   // copy path: races through the file, never repositioned
	duration   float64
	startSeg   int // first segment the current encoder was launched at
	cancel     context.CancelFunc
	run        *ffmpegRun // currently active encoder run; replaced on a seek relaunch
	lastAccess time.Time
}

// ffmpegRun tracks one encoder process so a segment wait can react to the process dying
// instead of grinding through the full timeout. done is closed when the process exits;
// err is the exit error of a genuine failure (a cancelled/repositioned run leaves it
// nil) and is safe to read once done is closed.
type ffmpegRun struct {
	done chan struct{}
	err  error
}

// failed reports whether the run ended in a genuine encoder failure (not our own cancel
// for a reposition). Read only after the run's done channel is closed.
func (r *ffmpegRun) failed() bool { return r != nil && r.err != nil }

// NewManager constructs a Manager and starts its idle-session reaper.
func NewManager(opts Options) *Manager {
	if opts.FFmpegPath == "" {
		opts.FFmpegPath = "ffmpeg"
	}
	if opts.FFprobePath == "" {
		opts.FFprobePath = "ffprobe"
	}
	enc := opts.Encoder
	if enc.Kind == "" {
		enc = softwareEncoder
	}
	m := &Manager{
		opts:     opts,
		encoder:  enc,
		log:      opts.Logger.For(logging.Frontend),
		sessions: map[string]*session{},
		stop:     make(chan struct{}),
	}
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

// ActiveSessions reports how many transcode sessions are currently live. The optimizer
// reads it to yield to viewers (live playback always wins over background encoding).
func (m *Manager) ActiveSessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// Playlist ensures a session for key/inputPath exists and returns its VOD media
// playlist. The playlist lists every segment up front (with #EXT-X-ENDLIST) so the
// player shows the full seek bar before encoding finishes.
func (m *Manager) Playlist(key, inputPath, title string) ([]byte, error) {
	s, err := m.ensure(key, inputPath, title)
	if err != nil {
		return nil, err
	}
	return []byte(buildPlaylist(s.duration)), nil
}

// Segment returns the on-disk path of a named segment for an existing session,
// waiting briefly for the encoder to produce it. name must match seg<N>.ts. The wait
// ends as soon as the segment appears, the request's ctx is cancelled, the encoder
// process dies, or the timeout elapses - whichever comes first.
func (m *Manager) Segment(ctx context.Context, key, name string) (string, error) {
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
	if _, err := os.Stat(path); err == nil {
		return path, nil // already encoded (covers buffered-back seeks)
	}

	// Not on disk yet: a far-forward or backward seek may need the lone encoder
	// relaunched to seek there rather than grinding forward to it.
	m.maybeReposition(s, segIndex(name))

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(segmentWaitTimeout)
	// consumed remembers a run whose exit we already handled, so a finished encoder's
	// closed done channel does not busy-spin the select. A reposition swaps in a new run,
	// which is != consumed and so is waited on afresh.
	var consumed *ffmpegRun
	for {
		m.mu.Lock()
		run := s.run
		m.mu.Unlock()
		var doneCh <-chan struct{}
		if run != nil && run != consumed {
			doneCh = run.done
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", fmt.Errorf("transcode: segment %s not ready", name)
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		case <-doneCh:
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
			if run.failed() {
				return "", fmt.Errorf("transcode: encoder for %s exited: %w", name, run.err)
			}
			consumed = run // clean or cancelled exit: stop selecting this run's done
		}
	}
}

// segIndex extracts N from a name already validated against segmentName (seg<N>.ts).
func segIndex(name string) int {
	n, _ := strconv.Atoi(name[len("seg") : len(name)-len(".ts")])
	return n
}

// repositionTarget decides whether the lone encoder should be relaunched to serve a
// requested segment. produced is the highest segment present from the current run
// (startSeg-1 if none yet). A request behind the run's start, or far enough past the
// encode head that waiting would stall, relaunches the encoder seeking to it.
func repositionTarget(startSeg, produced, requested int) (target int, reposition bool) {
	if requested < startSeg || requested > produced+repositionLead {
		return requested, true
	}
	return 0, false
}

// highestSeg returns the largest segment index >= startSeg present in dir, or
// startSeg-1 when the current run has produced none yet.
func highestSeg(dir string, startSeg int) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return startSeg - 1
	}
	max := startSeg - 1
	for _, e := range entries {
		name := e.Name()
		if !segmentName.MatchString(name) {
			continue
		}
		if n := segIndex(name); n >= startSeg && n > max {
			max = n
		}
	}
	return max
}

// maybeReposition relaunches the session's encoder seeking to segment n when the
// request is a seek out of the current run's reach. Re-encode sessions only; the
// remux/copy path is left to race through the file sequentially.
func (m *Manager) maybeReposition(s *session, n int) {
	if s.remux {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	produced := highestSeg(s.dir, s.startSeg)
	target, ok := repositionTarget(s.startSeg, produced, n)
	if !ok {
		return
	}
	// Start the new encoder before cancelling the old one so a launch failure leaves
	// the session encoding rather than dead. Segments already on disk stay valid; the
	// two processes write disjoint segment numbers, and +temp_file hides partials.
	runCtx, cancel := context.WithCancel(context.Background())
	run, err := m.startFFmpeg(runCtx, s.dir, s.inputPath, s.title, s.remux, target)
	if err != nil {
		cancel()
		return
	}
	s.cancel()
	s.cancel = cancel
	s.run = run
	s.startSeg = target
	pos := target * segmentSeconds
	m.log.Info(fmt.Sprintf("seek in %s to %s", s.title, clock(pos)), logging.Fields{
		"title": s.title, "position_s": pos, "to_seg": target,
	})
}

// clock formats whole seconds as H:MM:SS or M:SS.
func clock(sec int) string {
	h, m, s := sec/3600, (sec%3600)/60, sec%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func (m *Manager) ensure(key, inputPath, title string) (*session, error) {
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
	remux := RemuxEligible(streams)
	run, err := m.startFFmpeg(runCtx, dir, inputPath, title, remux, 0)
	if err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return nil, err
	}

	s := &session{dir: dir, inputPath: inputPath, title: title, remux: remux, duration: streams.Duration, cancel: cancel, run: run, lastAccess: time.Now()}

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
	m.log.Info("watching "+title, logging.Fields{
		"title": title, "remux": remux, "encoder": m.encoder.Kind,
	})
	return s, nil
}

// videoEncodeArgs returns the pre-input global flags and the codec flags for a
// re-encode, branching on the encoder. The -force_key_frames expression places
// keyframes on the absolute segmentSeconds grid (offset by startSeg under -copyts) so
// segment boundaries line up across seek relaunches; it is identical for both paths.
func videoEncodeArgs(enc Encoder, startSeg int) (preInput, codec []string) {
	keyframes := fmt.Sprintf("expr:gte(t,(n_forced+%d)*%d)", startSeg, segmentSeconds)
	preInput, vcodec := videoCodecArgs(enc)
	codec = append(vcodec, "-force_key_frames", keyframes, "-c:a", "aac", "-b:a", "160k", "-ac", "2")
	return preInput, codec
}

func (m *Manager) startFFmpeg(ctx context.Context, dir, inputPath, title string, remux bool, startSeg int) (*ffmpegRun, error) {
	args := []string{"-nostdin"}
	var codec []string
	if !remux {
		var preInput []string
		preInput, codec = videoEncodeArgs(m.encoder, startSeg)
		args = append(args, preInput...) // -vaapi_device is global, must precede -i
	}
	if startSeg > 0 {
		// Seek the input to the segment's start. -copyts preserves source timestamps so
		// the relaunched encoder's segments stay aligned with the up-front VOD playlist.
		args = append(args, "-copyts", "-ss", fmt.Sprint(startSeg*segmentSeconds))
	}
	// Confine ffmpeg to local files so a media file whose bytes are actually a playlist
	// cannot pivot to an http:/concat: input (the crypto/data layers a normal container may
	// legitimately use stay allowed).
	args = append(args, "-protocol_whitelist", "file,crypto,data", "-i", inputPath)
	if remux {
		args = append(args, "-c", "copy")
	} else {
		args = append(args, codec...)
	}
	args = append(args,
		"-sn",
		"-f", "hls",
		"-hls_time", fmt.Sprint(segmentSeconds),
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_segment_type", "mpegts",
		"-start_number", fmt.Sprint(startSeg),
		"-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(dir, "seg%d.ts"),
		filepath.Join(dir, "index.m3u8"),
	)

	p, err := ffrun.Start(ctx, m.opts.FFmpegPath, args...)
	if err != nil {
		return nil, fmt.Errorf("transcode: %w", err)
	}
	run := &ffmpegRun{done: make(chan struct{})}
	go func() {
		err := p.Wait()
		// A cancelled run (idle reap, Close, or a seek relaunch) is expected, not a
		// failure; only a non-context exit is recorded and logged, so the segment wait
		// fails fast instead of stalling for the full timeout.
		if err != nil && ctx.Err() == nil {
			run.err = err
			m.log.Error("transcode encoder failed for "+title, logging.Fields{
				"title": title, "error": err.Error(),
			})
		}
		close(run.done)
	}()
	return run, nil
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
