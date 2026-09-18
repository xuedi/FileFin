// Package ffprobe extracts media details by shelling out to ffprobe. Every variant runs
// the same single `-show_format -show_streams` decode and reads a different slice of the
// result: Probe for the durable technical block, ProbeStreams for the codec/duration the
// transcode decision needs, SubtitleStreams for the embedded subtitle tracks. The one
// exception is VideoKeyframes, a separate packet-level pass the HLS remux path needs.
package ffprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Technical is the subset of ffprobe output stored in a media folder's meta.json.
type Technical struct {
	Duration   int    `json:"duration,omitempty"`
	Container  string `json:"container,omitempty"`
	VideoCodec string `json:"videoCodec,omitempty"`
	AudioCodec string `json:"audioCodec,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

// Empty reports whether nothing was collected (so callers can omit the section).
func (t Technical) Empty() bool {
	return t == Technical{}
}

// Streams is the codec/duration subset the transcode decision needs. Duration is the
// container length in seconds (float, for precise HLS segment math).
type Streams struct {
	VideoCodec string
	AudioCodec string
	Duration   float64
}

type probeOutput struct {
	Format struct {
		Duration   string `json:"duration"`
		FormatName string `json:"format_name"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Tags      struct {
			Language string `json:"language"`
			Title    string `json:"title"`
		} `json:"tags"`
	} `json:"streams"`
}

// SubtitleStream is one embedded subtitle track of a media file. Index is the track's
// position among the file's subtitle streams (0-based), i.e. the N in ffmpeg's
// "0:s:N" map specifier. Language is the raw "tags.language" tag ("" when absent) and
// Title the raw "tags.title" tag, which often names the language when the language tag
// itself is missing.
type SubtitleStream struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
	Title    string `json:"title"`
}

// decode runs the single shared ffprobe invocation and parses its JSON. bin falls back to
// "ffprobe" on PATH when empty.
func decode(ctx context.Context, bin, path string) (probeOutput, error) {
	out, err := run(ctx, bin, path, "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams")
	if err != nil {
		return probeOutput{}, err
	}
	var po probeOutput
	if err := json.Unmarshal(out, &po); err != nil {
		return probeOutput{}, fmt.Errorf("parse ffprobe output for %s: %w", path, err)
	}
	return po, nil
}

// run invokes ffprobe with args on path. bin falls back to "ffprobe" on PATH when empty.
func run(ctx context.Context, bin, path string, args ...string) ([]byte, error) {
	if bin == "" {
		bin = "ffprobe"
	}
	// -protocol_whitelist confines ffprobe to local files (plus the crypto/data layers a
	// normal container may use), so a file whose bytes are actually a playlist cannot make it
	// open an http:/concat: target. A relative path is made explicit with "./" so a name
	// beginning with "-" is never parsed as an option (ffprobe has no "--" terminator).
	if !filepath.IsAbs(path) {
		path = "." + string(filepath.Separator) + path
	}
	args = append(args, "-protocol_whitelist", "file,crypto,data", path)
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	return out, nil
}

// VideoKeyframes lists the presentation times, in seconds and ascending, of the keyframes
// of path's first video stream. It reads packet headers only (no decode), so it costs one
// demux pass over the file.
func VideoKeyframes(ctx context.Context, bin, path string) ([]float64, error) {
	out, err := run(ctx, bin, path, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time,flags", "-of", "csv=p=0")
	if err != nil {
		return nil, err
	}
	return parseKeyframes(string(out)), nil
}

// parseKeyframes reads ffprobe's "pts_time,flags" CSV lines, keeping the keyframe (K) rows
// with a known timestamp.
func parseKeyframes(csv string) []float64 {
	var kf []float64
	for _, line := range strings.Split(csv, "\n") {
		pts, flags, ok := strings.Cut(strings.TrimSpace(line), ",")
		if !ok || !strings.Contains(flags, "K") {
			continue
		}
		if t, err := strconv.ParseFloat(pts, 64); err == nil {
			kf = append(kf, t)
		}
	}
	sort.Float64s(kf)
	return kf
}

// Probe runs ffprobe on path and returns its technical details, using the given binary
// (falling back to "ffprobe" on PATH when empty). A missing ffprobe binary or any probe
// error yields an empty Technical, never an error - an import still succeeds without it.
func Probe(ctx context.Context, bin, path string) Technical {
	po, err := decode(ctx, bin, path)
	if err != nil {
		return Technical{}
	}
	var t Technical
	if secs, err := strconv.ParseFloat(po.Format.Duration, 64); err == nil {
		t.Duration = int(math.Round(secs))
	}
	t.Container = po.Format.FormatName
	for _, s := range po.Streams {
		switch s.CodecType {
		case "video":
			if t.VideoCodec == "" {
				t.VideoCodec = s.CodecName
				t.Width = s.Width
				t.Height = s.Height
			}
		case "audio":
			if t.AudioCodec == "" {
				t.AudioCodec = s.CodecName
			}
		}
	}
	return t
}

// ProbeStreams reports the first video/audio codec and the container duration of path. It
// returns an error (unlike the best-effort Probe) because the transcode decision cannot
// proceed without it.
func ProbeStreams(ctx context.Context, ffprobeBin, path string) (Streams, error) {
	po, err := decode(ctx, ffprobeBin, path)
	if err != nil {
		return Streams{}, err
	}
	var s Streams
	for _, st := range po.Streams {
		switch st.CodecType {
		case "video":
			if s.VideoCodec == "" {
				s.VideoCodec = st.CodecName
			}
		case "audio":
			if s.AudioCodec == "" {
				s.AudioCodec = st.CodecName
			}
		}
	}
	s.Duration, _ = strconv.ParseFloat(po.Format.Duration, 64)
	return s, nil
}

// SubtitleStreams lists the embedded subtitle tracks of path, in stream order, using the
// given ffprobe binary (falling back to "ffprobe" on PATH when empty). It is best-effort
// like Probe: a missing binary or any error yields no streams, never an error.
func SubtitleStreams(ctx context.Context, ffprobeBin, path string) []SubtitleStream {
	po, err := decode(ctx, ffprobeBin, path)
	if err != nil {
		return nil
	}
	return subtitlesFrom(po)
}

// parseSubtitleStreams extracts the subtitle tracks from ffprobe JSON; malformed JSON
// yields none.
func parseSubtitleStreams(data []byte) []SubtitleStream {
	var po probeOutput
	if json.Unmarshal(data, &po) != nil {
		return nil
	}
	return subtitlesFrom(po)
}

// subtitlesFrom numbers the subtitle streams by their position among subtitle streams
// (the "0:s:N" index), ignoring all other stream types.
func subtitlesFrom(po probeOutput) []SubtitleStream {
	var subs []SubtitleStream
	rel := 0
	for _, s := range po.Streams {
		if s.CodecType != "subtitle" {
			continue
		}
		subs = append(subs, SubtitleStream{
			Index: rel, Codec: s.CodecName, Language: s.Tags.Language, Title: s.Tags.Title,
		})
		rel++
	}
	return subs
}
