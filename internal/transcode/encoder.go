package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	hwAccelAuto = "auto"
	hwAccelOff  = "off"
)

// Encoder describes how startFFmpeg should encode video: software (libx264) or a
// hardware path (VAAPI) bound to a specific DRM render node.
type Encoder struct {
	Kind   string // "software" | "vaapi"
	Device string // render node path for vaapi, "" for software
	Codec  string // ffmpeg encoder name, e.g. "libx264" or "h264_vaapi"
}

// softwareEncoder is the libx264 fallback used when no GPU is available or hardware
// acceleration is disabled. It is also the zero-value-equivalent default.
var softwareEncoder = Encoder{Kind: "software", Codec: "libx264"}

// SoftwareEncoder returns the libx264 CPU encoder. The optimizer uses it for its
// CPU-only workers, which run alongside the single GPU worker.
func SoftwareEncoder() Encoder { return softwareEncoder }

// videoCodecArgs returns the pre-input global flags and the video-codec flags (no
// keyframe or container options) for enc, shared by the HLS and optimize encoders.
func videoCodecArgs(enc Encoder) (preInput, vcodec []string) {
	if enc.Kind == "vaapi" {
		return []string{"-vaapi_device", enc.Device}, []string{
			"-vf", "format=nv12,hwupload",
			"-c:v", "h264_vaapi", "-rc_mode", "CQP", "-qp", "23",
		}
	}
	return nil, []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "23"}
}

// OptimizeArgs builds the ffmpeg arguments to transcode inputPath into a browser
// direct-play faststart MP4 at outputPath, using enc (GPU when vaapi). Unlike the HLS
// encoder this is a plain single-file encode: no segmenting or forced keyframes.
func OptimizeArgs(enc Encoder, inputPath, outputPath string) []string {
	preInput, vcodec := videoCodecArgs(enc)
	args := append([]string{"-nostdin", "-y"}, preInput...)
	args = append(args, "-i", inputPath)
	args = append(args, vcodec...)
	// -f mp4 is explicit because the worker writes to a ".tmp" path ffmpeg cannot infer.
	return append(args, "-c:a", "aac", "-b:a", "160k", "-ac", "2",
		"-movflags", "+faststart", "-sn", "-f", "mp4", outputPath)
}

// DetectEncoder probes for a usable VAAPI H.264 encoder, honoring the hardware-accel
// settings on opts. It returns the software encoder when hardware is disabled, when
// ffmpeg lacks h264_vaapi, or when no render node can actually encode. Detection ends
// in a real micro-encode, not a capability guess, so a returned vaapi encoder is known
// to work end to end (driver + ffmpeg + GPU).
func DetectEncoder(ctx context.Context, opts Options) Encoder {
	if opts.HWAccel == hwAccelOff {
		return softwareEncoder
	}
	ffmpeg := opts.FFmpegPath
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	if !ffmpegHasEncoder(ctx, ffmpeg, "h264_vaapi") {
		return softwareEncoder
	}
	nodes := renderNodes("/dev/dri")
	if opts.HWAccelDevice != "" {
		nodes = []string{opts.HWAccelDevice}
	}
	for _, node := range nodes {
		if probeVAAPIEncode(ctx, ffmpeg, node) {
			return Encoder{Kind: "vaapi", Device: node, Codec: "h264_vaapi"}
		}
	}
	return softwareEncoder
}

// renderNodes lists DRM render nodes (/dev/dri/renderD*) in dir, sorted.
func renderNodes(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var nodes []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "renderD") {
			nodes = append(nodes, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(nodes)
	return nodes
}

// ffmpegHasEncoder reports whether `ffmpeg -encoders` lists the named encoder.
func ffmpegHasEncoder(ctx context.Context, ffmpeg, name string) bool {
	out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		return false
	}
	return encoderListed(string(out), name)
}

// encoderListed scans `ffmpeg -encoders` output for an encoder name, which appears as a
// whitespace-delimited token after the per-line capability-flags column.
func encoderListed(out, name string) bool {
	for _, line := range strings.Split(out, "\n") {
		for _, f := range strings.Fields(line) {
			if f == name {
				return true
			}
		}
	}
	return false
}

// probeVAAPIEncode runs a tiny real encode on node to confirm the full VAAPI H.264 path
// works (driver + ffmpeg + GPU), discarding the output.
func probeVAAPIEncode(ctx context.Context, ffmpeg, node string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner",
		"-vaapi_device", node,
		"-f", "lavfi", "-i", "testsrc=duration=0.1:size=128x128:rate=5",
		"-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi",
		"-f", "null", "-",
	)
	return cmd.Run() == nil
}
