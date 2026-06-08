package importer

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"filefin/internal/ffprobe"
	"filefin/internal/subtitle"
)

// bitmapSubCodecs are embedded subtitle codecs that are images, not text. They cannot
// be turned into SRT without OCR (and the player only renders SRT), so they are never
// extracted. Everything else is treated as a text track ffmpeg can mux to SRT.
var bitmapSubCodecs = map[string]bool{
	"hdmv_pgs_subtitle": true,
	"pgssub":            true,
	"dvd_subtitle":      true,
	"dvdsub":            true,
	"dvb_subtitle":      true,
	"dvbsub":            true,
	"dvb_teletext":      true,
	"xsub":              true,
}

// embeddedPick is one subtitle track chosen for extraction: its stream index (the N in
// ffmpeg's "0:s:N") and the normalised language to name the output file after.
type embeddedPick struct {
	Index int
	Lang  string
}

// chooseEmbeddedSubtitles decides which embedded subtitle tracks to extract: text tracks
// only (bitmap codecs are skipped), each carrying a real language tag (a missing or
// "und" tag is skipped - we will not guess), whose language is not already covered. The
// first track of a given language wins, so a second same-language track is skipped.
// present holds the languages already on disk as sidecars.
func chooseEmbeddedSubtitles(streams []ffprobe.SubtitleStream, present map[string]bool) []embeddedPick {
	claimed := map[string]bool{}
	for k := range present {
		claimed[k] = true
	}
	var out []embeddedPick
	for _, st := range streams {
		if bitmapSubCodecs[strings.ToLower(strings.TrimSpace(st.Codec))] {
			continue
		}
		raw := strings.ToLower(strings.TrimSpace(st.Language))
		if raw == "" || raw == "und" {
			continue // unknown language: we cannot name it, so skip
		}
		lang := subtitle.NormalizeLang(st.Language, "")
		if lang == "" || claimed[lang] {
			continue
		}
		claimed[lang] = true
		out = append(out, embeddedPick{Index: st.Index, Lang: lang})
	}
	return out
}

// ExtractEmbeddedSubtitles externalises a video's embedded text subtitle tracks as
// "<base>.<lang>.srt" sidecars so the player (which only renders SRT sidecars) can show
// them. It skips any language already present as a sidecar and any track without a
// recognised language. It runs after PlaceSubtitles so the just-placed sidecars are part
// of the dedup set. Best-effort: a missing ffmpeg/ffprobe or a failed extract is a silent
// no-op. Returns how many tracks were extracted.
func ExtractEmbeddedSubtitles(videoTarget, ffmpegBin, ffprobeBin string) int {
	present := map[string]bool{}
	for _, s := range FindSidecarSubtitles(videoTarget) {
		if lang := subtitle.NormalizeLang(s.Language, ""); lang != "" {
			present[lang] = true
		}
	}
	picks := chooseEmbeddedSubtitles(ffprobe.SubtitleStreams(context.Background(), ffprobeBin, videoTarget), present)
	n := 0
	for _, p := range picks {
		dst := SubtitleTargetName(videoTarget, p.Lang, ".srt")
		if err := extractSubtitleTrack(ffmpegBin, videoTarget, p.Index, dst); err == nil {
			n++
		}
	}
	return n
}

// extractSubtitleTrack muxes one subtitle track (by its 0:s:N index) out of src to dst as
// SRT, through a temp file plus atomic rename so a crashed run never leaves a partial
// .srt. An empty ffmpeg path, a missing binary, or a non-zero exit is an error.
func extractSubtitleTrack(ffmpegBin, src string, index int, dst string) error {
	if ffmpegBin == "" {
		return os.ErrNotExist
	}
	tmp := dst + ".tmp.srt" // ffmpeg picks the muxer by extension, so keep .srt
	cmd := exec.Command(ffmpegBin, "-nostdin", "-y", "-i", src,
		"-map", "0:s:"+strconv.Itoa(index), "-c:s", "srt", tmp)
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
