// Package transcode streams non-browser-native media as seekable HLS via ffmpeg.
package transcode

import (
	"os"
	"path/filepath"
	"strings"
)

// OptimizedExt is the suffix of a pre-transcoded, browser-direct-play copy of a source
// media file, stored beside it as `<source-base>.optimized.mp4`.
const OptimizedExt = ".optimized.mp4"

// OptimizedTmpSuffix names the optimizer's in-progress temp file (`<optimized>.tmp`).
// The optimizer creates it atomically so it doubles as a per-item lock; the suffix is
// a single source of truth shared by the worker and the stale-lock sweep. It is not a
// video extension, so a leftover is never scanned as media.
const OptimizedTmpSuffix = OptimizedExt + ".tmp"

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

// directPlay holds the browser-native container extensions served as-is with byte-range
// support. This is the cheap first-pass filter used by NeedsTranscode when no probed
// format is known yet; the content-based DirectPlayable below is the authority once a
// file has been probed.
var directPlay = map[string]bool{
	".mp4":  true,
	".webm": true,
	".m4v":  true,
}

// NeedsTranscode reports whether a file with the given extension must be transcoded
// to play in the browser. The extension is matched case-insensitively and may be
// given with or without a leading dot, so filepath.Ext output can be passed directly.
// It is the fallback for a row with no probed format yet (a legacy item the probe
// agent has not reached); DirectPlayable is preferred whenever the format is known.
func NeedsTranscode(ext string) bool {
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return !directPlay[ext]
}

// mp4Family and mkvFamily are the ffprobe container tokens for the two browser-native
// container groups. ffprobe reports format_name as a comma-listed set (e.g.
// "mov,mp4,m4a,3gp,3g2,mj2" or "matroska,webm"), so DirectPlayable matches any token of
// the probed container against these sets.
var (
	mp4Family = map[string]bool{"mov": true, "mp4": true, "m4a": true, "m4v": true, "3gp": true, "3g2": true, "mj2": true}
	mkvFamily = map[string]bool{"matroska": true, "webm": true}
)

// webmVideo and webmAudio are the codecs a WebM/Matroska container may carry and still
// direct-play in the browser. An empty audio codec (no audio track) is allowed.
var (
	webmVideo = map[string]bool{"vp8": true, "vp9": true, "av1": true}
	webmAudio = map[string]bool{"opus": true, "vorbis": true, "": true}
)

// DirectPlayable reports whether a file with the given probed container and video/audio
// codecs can be served to the browser as-is (byte-range, no transcode). The rule is by
// content, not filename: an MP4-family container with H.264 video and AAC/MP3 (or no)
// audio - which is exactly RemuxEligible - or a WebM/Matroska container with VP8/VP9/AV1
// video and Opus/Vorbis (or no) audio. Everything else must go to HLS. An empty
// container (never probed) is not direct-playable here; callers fall back to
// NeedsTranscode(ext).
func DirectPlayable(container, videoCodec, audioCodec string) bool {
	switch {
	case containerIn(container, mp4Family):
		return RemuxEligible(Streams{VideoCodec: strings.ToLower(videoCodec), AudioCodec: strings.ToLower(audioCodec)})
	case containerIn(container, mkvFamily):
		return webmVideo[strings.ToLower(videoCodec)] && webmAudio[strings.ToLower(audioCodec)]
	}
	return false
}

// containerIn reports whether any comma-separated token of an ffprobe format_name is in
// the given family set.
func containerIn(container string, family map[string]bool) bool {
	for _, tok := range strings.Split(strings.ToLower(container), ",") {
		if family[strings.TrimSpace(tok)] {
			return true
		}
	}
	return false
}
