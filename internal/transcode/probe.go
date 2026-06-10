package transcode

import (
	"context"
	"fmt"

	"filefin/internal/ffprobe"
)

// Streams is the subset of ffprobe output we care about: the codecs present and the
// container duration in seconds.
type Streams = ffprobe.Streams

// Probe inspects inputPath with ffprobe and reports its video/audio codecs and duration.
// The probe itself lives in the ffprobe package (one shared `-show_format -show_streams`
// decode); this thin wrapper keeps the transcode-scoped error prefix.
func Probe(ctx context.Context, ffprobePath, inputPath string) (Streams, error) {
	s, err := ffprobe.ProbeStreams(ctx, ffprobePath, inputPath)
	if err != nil {
		return Streams{}, fmt.Errorf("transcode: %w", err)
	}
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
