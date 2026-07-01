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

// Kind is an encoder family. The two paths the app supports are the libx264 CPU encoder
// and the VAAPI GPU encoder; the empty Kind means "unset", treated as software.
type Kind string

const (
	KindSoftware Kind = "software"
	KindVAAPI    Kind = "vaapi"
)

// Encoder describes how startFFmpeg should encode video: software (libx264) or a
// hardware path (VAAPI) bound to a specific DRM render node.
type Encoder struct {
	Kind   Kind   // KindSoftware | KindVAAPI
	Device string // render node path for vaapi, "" for software
	Codec  string // ffmpeg encoder name, e.g. "libx264" or "h264_vaapi"
}

// softwareEncoder is the libx264 fallback used when no GPU is available or hardware
// acceleration is disabled. It is also the zero-value-equivalent default.
var softwareEncoder = Encoder{Kind: KindSoftware, Codec: "libx264"}

// SoftwareEncoder returns the libx264 CPU encoder.
func SoftwareEncoder() Encoder { return softwareEncoder }

// videoCodecArgs returns the pre-input global flags and the video-codec flags (no
// keyframe or container options) for enc.
func videoCodecArgs(enc Encoder) (preInput, vcodec []string) {
	if enc.Kind == KindVAAPI {
		return []string{"-vaapi_device", enc.Device}, []string{
			"-vf", "format=nv12,hwupload",
			"-c:v", "h264_vaapi", "-rc_mode", "CQP", "-qp", "23",
		}
	}
	return nil, []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "23"}
}

// OptimizeArgs builds the ffmpeg arguments to transcode inputPath into a browser
// direct-play faststart MP4 at outputPath, using enc (GPU when vaapi). Unlike the HLS
// encoder this is a plain single-file encode: no segmenting or forced keyframes. extra
// holds any flags the caller wants placed just before the output path (e.g. the
// optimizer's `-progress pipe:1 -nostats`); ffmpeg rejects options after the output file,
// so threading them here avoids the caller splicing the slice by index.
func OptimizeArgs(enc Encoder, inputPath, outputPath string, extra ...string) []string {
	preInput, vcodec := videoCodecArgs(enc)
	args := append([]string{"-nostdin", "-y"}, preInput...)
	// Confine ffmpeg to local files (plus the crypto/data layers a normal container may use)
	// so a crafted input cannot pivot to a network/other-file protocol.
	args = append(args, "-protocol_whitelist", "file,crypto,data", "-i", inputPath)
	args = append(args, vcodec...)
	// -f mp4 is explicit because the worker writes to a ".tmp" path ffmpeg cannot infer.
	args = append(args, "-c:a", "aac", "-b:a", "160k", "-ac", "2",
		"-movflags", "+faststart", "-sn", "-f", "mp4")
	args = append(args, extra...)
	return append(args, outputPath)
}

// DetectEncoders probes for usable VAAPI H.264 encoders, honoring the hardware-accel
// settings on opts. It returns one Encoder per render node that can actually encode, in
// sorted node order, so a multi-GPU host yields one encoder per card. It falls back to a
// single software encoder when hardware is disabled, when ffmpeg lacks h264_vaapi, or when
// no render node can encode. Detection ends in a real micro-encode, not a capability guess,
// so a returned vaapi encoder is known to work end to end (driver + ffmpeg + GPU). The
// result always has at least one element.
func DetectEncoders(ctx context.Context, opts Options) []Encoder {
	ffmpeg := opts.FFmpegPath
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	return detectEncoders(opts,
		func(name string) bool { return ffmpegHasEncoder(ctx, ffmpeg, name) },
		func(node string) bool { return probeVAAPIEncode(ctx, ffmpeg, node) },
		renderNodes("/dev/dri"),
	)
}

// DetectEncoder returns the first encoder DetectEncoders finds (a single GPU, or software).
// It is the single-encoder entry point used by live HLS playback.
func DetectEncoder(ctx context.Context, opts Options) Encoder {
	return DetectEncoders(ctx, opts)[0]
}

// detectEncoders is the GPU-free core of DetectEncoders: hasEncoder and probe are injected
// so the node-selection logic is testable without a real ffmpeg or GPU.
func detectEncoders(opts Options, hasEncoder, probe func(string) bool, nodes []string) []Encoder {
	if opts.HWAccel == hwAccelOff || !hasEncoder("h264_vaapi") {
		return []Encoder{softwareEncoder}
	}
	if opts.HWAccelDevice != "" {
		nodes = []string{opts.HWAccelDevice}
	}
	var encs []Encoder
	for _, node := range nodes {
		if probe(node) {
			encs = append(encs, Encoder{Kind: KindVAAPI, Device: node, Codec: "h264_vaapi"})
		}
	}
	if len(encs) == 0 {
		return []Encoder{softwareEncoder}
	}
	return encs
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
