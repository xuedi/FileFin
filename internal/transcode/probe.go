package transcode

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// Streams is the subset of ffprobe output we care about: the codecs present and the
// container duration in seconds.
type Streams struct {
	VideoCodec string
	AudioCodec string
	Duration   float64
}

// Probe inspects inputPath with ffprobe and reports its video/audio codecs and duration.
func Probe(ctx context.Context, ffprobePath, inputPath string) (Streams, error) {
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-show_entries", "stream=codec_type,codec_name",
		"-show_entries", "format=duration",
		"-of", "json",
		inputPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return Streams{}, fmt.Errorf("transcode: ffprobe %s: %w", inputPath, err)
	}

	var parsed struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return Streams{}, fmt.Errorf("transcode: parse ffprobe output: %w", err)
	}

	var s Streams
	for _, st := range parsed.Streams {
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
	s.Duration, _ = strconv.ParseFloat(parsed.Format.Duration, 64)
	return s, nil
}

// RemuxEligible reports whether the source can be copied straight into a TS container
// (no re-encode): H.264 video with browser-friendly audio (AAC/MP3), or no audio at
// all. The HLS Manager prefers this fast path when the source qualifies.
func RemuxEligible(s Streams) bool {
	if s.VideoCodec != "h264" {
		return false
	}
	switch s.AudioCodec {
	case "aac", "mp3", "":
		return true
	default:
		return false
	}
}
