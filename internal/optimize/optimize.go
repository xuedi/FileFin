// Package optimize holds the thin, orchestration-free helpers behind the background
// pre-transcoder: deriving the candidate files that need a direct-play copy, running one
// ffmpeg encode with live progress, and sweeping crashed-run locks. The server package
// owns the queue, agents, and scaling.
package optimize

import (
	"bufio"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"filefin/internal/db"
	"filefin/internal/ffrun"
	"filefin/internal/transcode"
)

// Candidate is one media file that should be optimized, with the target path of its
// optimized copy.
type Candidate struct {
	MediaID   string
	FileIdx   int
	Source    string
	Optimized string
}

// Candidates derives the pending work from the cached media files: sources the browser
// cannot direct-play that lack a fresh optimized copy. The "cannot direct-play" judgement
// uses the probed true format when the row carries it (so a `.avi`-named H.264/MP4 is not
// queued), falling back to the filename extension for a row the probe agent has not
// reached yet. Remux-eligible files are not filtered here (that needs ffprobe) - the agent
// skips them after probing.
func Candidates(files []db.MediaFile) []Candidate {
	var out []Candidate
	for _, f := range files {
		if !needsTranscode(f) {
			continue
		}
		opt, fresh := transcode.OptimizedSibling(f.Path)
		if fresh {
			continue
		}
		out = append(out, Candidate{MediaID: f.MediaID, FileIdx: f.Idx, Source: f.Path, Optimized: opt})
	}
	return out
}

// needsTranscode reports whether a file is not browser-direct-playable, by its probed
// format when known and by the filename extension otherwise.
func needsTranscode(f db.MediaFile) bool {
	if f.Container != "" && f.VideoCodec != "" {
		return !transcode.DirectPlayable(f.Container, f.VideoCodec, f.AudioCodec)
	}
	return transcode.NeedsTranscode(f.Ext)
}

// EncodeOptions configures one optimize encode. Source/Output are the input file and the
// (temp) output path; Duration is the probed source length in seconds (non-positive
// disables the percentage); OnProgress, if non-nil, receives the running percent and a
// final 100 on ffmpeg's progress=end.
type EncodeOptions struct {
	FFmpeg     string
	Encoder    transcode.Encoder
	Source     string
	Output     string
	Duration   float64
	OnProgress func(pct int)
}

// Encode transcodes opts.Source into a faststart H.264+AAC MP4 at opts.Output using the
// configured encoder, reporting progress through opts.OnProgress as it runs.
func Encode(ctx context.Context, opts EncodeOptions) error {
	args := transcode.OptimizeArgs(opts.Encoder, opts.Source, opts.Output, "-progress", "pipe:1", "-nostats")
	p, err := ffrun.Start(ctx, opts.FFmpeg, args...)
	if err != nil {
		return err
	}
	scanProgress(p.Stdout, opts.Duration, opts.OnProgress)
	return p.Wait()
}

// scanProgress reads ffmpeg's `-progress pipe:1` key=value stream, emitting a percent
// from out_time_ms (microseconds) against duration, and 100 when ffmpeg reports
// progress=end. It returns when the stream closes.
func scanProgress(r io.Reader, duration float64, onProgress func(pct int)) {
	if onProgress == nil {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	sc := bufio.NewScanner(r)
	last := -1
	for sc.Scan() {
		key, val, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		switch key {
		case "out_time_ms", "out_time_us":
			if duration <= 0 {
				continue
			}
			us, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				continue
			}
			pct := int(float64(us) / 1e6 / duration * 100)
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			if pct != last {
				last = pct
				onProgress(pct)
			}
		case "progress":
			if val == "end" && last != 100 {
				last = 100
				onProgress(100)
			}
		}
	}
}

// SweepStaleLocks removes optimizer in-progress temp files (the per-item locks) left
// under dataDir by a crashed run, returning how many it cleared. The caller must own the
// data dir at that moment (no live agent), so it never removes an active lock.
func SweepStaleLocks(dataDir string) (int, error) {
	if dataDir == "" {
		return 0, nil
	}
	n := 0
	err := filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), transcode.OptimizedTmpSuffix) {
			if os.Remove(path) == nil {
				n++
			}
		}
		return nil
	})
	return n, err
}
