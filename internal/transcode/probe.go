package transcode

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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

// MediaInfo is the rich technical description of a media file from a full ffprobe
// inspection (`-show_format -show_streams`). It is the basis for the `## technical`
// section of meta.md and is separate from the lean Streams that HLS uses.
type MediaInfo struct {
	Container         string // format_name, first token (e.g. "matroska")
	VideoCodec        string
	VideoProfile      string
	Width, Height     int
	BitDepth          int      // bits_per_raw_sample, else inferred from pix_fmt
	HDR               string   // "PQ" / "HLG" from color_transfer, else ""
	FrameRate         float64  // avg_frame_rate "num/den" parsed
	AudioCodec        string   // first audio stream
	AudioChannels     string   // first audio stream's channel_layout, else channel count
	AudioLanguages    []string // tags.language across all audio streams
	SubtitleLanguages []string // tags.language across all subtitle streams
	BitRate           int64    // format.bit_rate (bits/sec)
	Size              int64    // format.size (bytes)
}

var rePixDepth = regexp.MustCompile(`p(\d{1,2})(le|be)?$`)

// Inspect runs a full ffprobe inspection of path and returns its technical facts.
// Video fields come from the first video stream; audio/subtitle languages are
// collected across every matching stream.
func Inspect(ctx context.Context, ffprobePath, path string) (MediaInfo, error) {
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-show_format",
		"-show_streams",
		"-of", "json",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return MediaInfo{}, fmt.Errorf("transcode: ffprobe inspect %s: %w", path, err)
	}

	var parsed struct {
		Streams []struct {
			CodecType        string `json:"codec_type"`
			CodecName        string `json:"codec_name"`
			Profile          string `json:"profile"`
			Width            int    `json:"width"`
			Height           int    `json:"height"`
			PixFmt           string `json:"pix_fmt"`
			BitsPerRawSample string `json:"bits_per_raw_sample"`
			ColorTransfer    string `json:"color_transfer"`
			AvgFrameRate     string `json:"avg_frame_rate"`
			Channels         int    `json:"channels"`
			ChannelLayout    string `json:"channel_layout"`
			Tags             struct {
				Language string `json:"language"`
			} `json:"tags"`
		} `json:"streams"`
		Format struct {
			FormatName string `json:"format_name"`
			Size       string `json:"size"`
			BitRate    string `json:"bit_rate"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return MediaInfo{}, fmt.Errorf("transcode: parse ffprobe inspect output: %w", err)
	}

	var mi MediaInfo
	mi.Container = firstToken(parsed.Format.FormatName)
	mi.Size, _ = strconv.ParseInt(parsed.Format.Size, 10, 64)
	mi.BitRate, _ = strconv.ParseInt(parsed.Format.BitRate, 10, 64)

	videoSeen := false
	for _, st := range parsed.Streams {
		switch st.CodecType {
		case "video":
			if !videoSeen {
				videoSeen = true
				mi.VideoCodec = st.CodecName
				mi.VideoProfile = st.Profile
				mi.Width = st.Width
				mi.Height = st.Height
				mi.BitDepth = bitDepth(st.BitsPerRawSample, st.PixFmt)
				mi.HDR = hdrFromTransfer(st.ColorTransfer)
				mi.FrameRate = parseFrameRate(st.AvgFrameRate)
			}
		case "audio":
			if mi.AudioCodec == "" {
				mi.AudioCodec = st.CodecName
				if st.ChannelLayout != "" {
					mi.AudioChannels = st.ChannelLayout
				} else if st.Channels > 0 {
					mi.AudioChannels = strconv.Itoa(st.Channels)
				}
			}
			if lang := normLang(st.Tags.Language); lang != "" {
				mi.AudioLanguages = append(mi.AudioLanguages, lang)
			}
		case "subtitle":
			if lang := normLang(st.Tags.Language); lang != "" {
				mi.SubtitleLanguages = append(mi.SubtitleLanguages, lang)
			}
		}
	}
	return mi, nil
}

func firstToken(s string) string {
	if i := strings.IndexByte(s, ','); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func bitDepth(bitsPerRawSample, pixFmt string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(bitsPerRawSample)); err == nil && n > 0 {
		return n
	}
	if m := rePixDepth.FindStringSubmatch(pixFmt); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if pixFmt != "" {
		return 8
	}
	return 0
}

func hdrFromTransfer(t string) string {
	switch t {
	case "smpte2084":
		return "PQ"
	case "arib-std-b67":
		return "HLG"
	default:
		return ""
	}
}

func parseFrameRate(s string) float64 {
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	n, _ := strconv.ParseFloat(num, 64)
	d, _ := strconv.ParseFloat(den, 64)
	if d == 0 {
		return 0
	}
	return n / d
}

func normLang(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "und" {
		return ""
	}
	return s
}

// RemuxEligible reports whether the source can be copied straight into an MP4/TS
// container (no re-encode): H.264 video with browser-friendly audio (AAC/MP3), or no
// audio at all.
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
