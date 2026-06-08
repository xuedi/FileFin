// Package ffprobe extracts technical details from a media file by shelling out to
// ffprobe. It is best-effort: when ffprobe is not on PATH or fails, Probe returns an
// empty Technical and no error, so an import still succeeds without it.
package ffprobe

import (
	"context"
	"encoding/json"
	"math"
	"os/exec"
	"strconv"
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
		} `json:"tags"`
	} `json:"streams"`
}

// SubtitleStream is one embedded subtitle track of a media file. Index is the track's
// position among the file's subtitle streams (0-based), i.e. the N in ffmpeg's
// "0:s:N" map specifier. Language is the raw "tags.language" tag ("" when absent).
type SubtitleStream struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
}

// Probe runs ffprobe on path and returns its technical details. A missing ffprobe
// binary or any probe error yields an empty Technical, never an error.
func Probe(ctx context.Context, path string) Technical {
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		return Technical{}
	}
	out, err := exec.CommandContext(ctx, bin,
		"-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", path).Output()
	if err != nil {
		return Technical{}
	}
	var po probeOutput
	if json.Unmarshal(out, &po) != nil {
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

// SubtitleStreams lists the embedded subtitle tracks of path, in stream order, using
// the given ffprobe binary (falling back to "ffprobe" on PATH when empty). It is
// best-effort like Probe: a missing binary or any error yields no streams, never an
// error.
func SubtitleStreams(ctx context.Context, ffprobeBin, path string) []SubtitleStream {
	bin := ffprobeBin
	if bin == "" {
		bin = "ffprobe"
	}
	out, err := exec.CommandContext(ctx, bin,
		"-v", "quiet", "-print_format", "json", "-show_streams", path).Output()
	if err != nil {
		return nil
	}
	return parseSubtitleStreams(out)
}

// parseSubtitleStreams extracts the subtitle tracks from ffprobe's -show_streams JSON,
// numbering them by their position among subtitle streams (the "0:s:N" index).
func parseSubtitleStreams(data []byte) []SubtitleStream {
	var po probeOutput
	if json.Unmarshal(data, &po) != nil {
		return nil
	}
	var subs []SubtitleStream
	rel := 0
	for _, s := range po.Streams {
		if s.CodecType != "subtitle" {
			continue
		}
		subs = append(subs, SubtitleStream{Index: rel, Codec: s.CodecName, Language: s.Tags.Language})
		rel++
	}
	return subs
}
