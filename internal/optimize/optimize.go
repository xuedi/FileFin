// Package optimize holds the thin, orchestration-free helpers behind the background
// pre-transcoder: deriving the candidate files that need a direct-play copy, running one
// ffmpeg encode with live progress, and sweeping crashed-run locks. The server package
// owns the queue, agents, and scaling.
package optimize

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"filefin/internal/db"
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
// cannot direct-play that lack a fresh optimized copy. Remux-eligible files are not
// filtered here (that needs ffprobe) - the agent skips them after probing.
func Candidates(files []db.MediaFile) []Candidate {
	var out []Candidate
	for _, f := range files {
		if !transcode.NeedsTranscode(f.Ext) {
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

// Encode transcodes src into a faststart H.264+AAC MP4 at tmp using enc, reporting
// progress percent (out_time vs duration) through onProgress as it runs and finishing at
// 100 on ffmpeg's progress=end. duration is the probed source length in seconds; a
// non-positive duration disables the percentage (onProgress is still called with 100 at
// the end).
func Encode(ctx context.Context, ffmpeg string, enc transcode.Encoder, src, tmp string, duration float64, onProgress func(pct int)) error {
	base := transcode.OptimizeArgs(enc, src, tmp)
	// Insert the progress flags before the output path (the last arg); ffmpeg rejects
	// options placed after the output file.
	out := base[len(base)-1]
	args := append(base[:len(base)-1:len(base)-1], "-progress", "pipe:1", "-nostats", out)

	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	scanProgress(stdout, duration, onProgress)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, lastLine(stderr.Bytes()))
	}
	return nil
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

// lastLine returns the last non-empty line of ffmpeg output for a compact error.
func lastLine(b []byte) string {
	s := strings.TrimRight(string(b), "\r\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
