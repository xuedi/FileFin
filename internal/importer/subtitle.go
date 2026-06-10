package importer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"filefin/internal/ffrun"
	"filefin/internal/subtitle"
)

// Subtitle is one external subtitle file to place beside its video. Ext is the
// lowercased source extension (".srt", ".ass", ".sub", ...); Language is the raw
// language tag (possibly empty, resolved against the default at copy time). For a
// VobSub pair, Path may point at either the .sub or the .idx; both are copied.
type Subtitle struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Ext      string `json:"ext"`
}

// subtitleExts are the sidecar extensions the importer recognises. VobSub
// (.sub/.idx) and PGS (.sup) are the tolerated bitmap formats (copied verbatim);
// the rest are text.
var subtitleExts = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".sub": true,
	".idx": true, ".vtt": true, ".sup": true, ".smi": true,
}

// bitmapSubExts cannot become SRT, so they are copied verbatim rather than
// converted.
var bitmapSubExts = map[string]bool{".sub": true, ".idx": true, ".sup": true}

// SubtitleTargetName derives a subtitle's canonical name from its video's target
// name: the video base (its own extension dropped) plus ".<lang><sext>", so the
// subtitle inherits any " - SxE" / " - partN" suffix. sext includes the dot.
func SubtitleTargetName(videoTargetName, lang, sext string) string {
	base := strings.TrimSuffix(videoTargetName, filepath.Ext(videoTargetName))
	return base + "." + lang + sext
}

// FindSidecarSubtitles returns the external subtitle files sitting next to a video
// that match its base name, with an optional language infix. A VobSub .sub/.idx
// pair is attached once per language. It reads the video's directory; a read error
// yields none.
func FindSidecarSubtitles(videoPath string) []Subtitle {
	dir := filepath.Dir(videoPath)
	videoBase := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var subs []Subtitle
	vobsubSeen := map[string]bool{} // language -> pair already attached
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !subtitleExts[ext] {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		lang := ""
		switch {
		case stem == videoBase:
			// no language infix
		case strings.HasPrefix(stem, videoBase+"."):
			if lang = subtitle.LangFromName(name); lang == "" {
				continue // a non-language infix means this is not our sidecar
			}
		default:
			continue
		}
		if ext == ".sub" || ext == ".idx" {
			if vobsubSeen[lang] {
				continue
			}
			vobsubSeen[lang] = true
			subs = append(subs, Subtitle{Path: filepath.Join(dir, name), Language: lang, Ext: ".sub"})
			continue
		}
		subs = append(subs, Subtitle{Path: filepath.Join(dir, name), Language: lang, Ext: ext})
	}
	return subs
}

// PlaceSubtitles copies (or, for text formats other than SRT, converts to SRT) each
// subtitle next to the placed video under "<video-base>.<lang>[.<n>].<ext>". Bitmap
// subtitles (VobSub, PGS) are copied verbatim. It is best-effort: every failure is
// swallowed so a subtitle never fails the import.
func PlaceSubtitles(ctx context.Context, videoTarget, defaultLang, ffmpegPath string, subs []Subtitle) {
	used := map[string]int{}
	name := func(lang, ext string) string {
		used[lang+ext]++
		if n := used[lang+ext]; n > 1 {
			base := strings.TrimSuffix(videoTarget, filepath.Ext(videoTarget))
			return base + "." + lang + "." + strconv.Itoa(n) + ext
		}
		return SubtitleTargetName(videoTarget, lang, ext)
	}
	for _, s := range subs {
		lang := subtitle.NormalizeLang(s.Language, defaultLang)
		ext := strings.ToLower(s.Ext)
		if ext == "" {
			ext = strings.ToLower(filepath.Ext(s.Path))
		}
		switch {
		case ext == ".sub" || ext == ".idx":
			dst := name(lang, ".sub")
			dstBase := strings.TrimSuffix(dst, ".sub")
			srcBase := strings.TrimSuffix(s.Path, filepath.Ext(s.Path))
			_ = copySidecar(srcBase+".sub", dstBase+".sub")
			_ = copySidecar(srcBase+".idx", dstBase+".idx")
		case bitmapSubExts[ext]: // .sup
			_ = copySidecar(s.Path, name(lang, ext))
		case ext == ".srt":
			_ = copySidecar(s.Path, name(lang, ".srt"))
		default: // text subtitle (.ass/.ssa/.vtt/.smi/...) -> convert to SRT
			dst := name(lang, ".srt")
			if err := runFFmpegToSRT(ctx, ffmpegPath, s.Path, dst); err != nil {
				// ffmpeg missing or failed: keep the original verbatim under the same
				// base but its real extension, so the track is not lost.
				_ = copySidecar(s.Path, strings.TrimSuffix(dst, ".srt")+ext)
			}
		}
	}
}

// runFFmpegToSRT muxes/transcodes src to an SRT sidecar at dst through a temp .srt plus
// atomic rename, so a crashed run never leaves a partial file. extraArgs select the
// source content: none for a whole-file text-subtitle conversion, or a "-map 0:s:N -c:s
// srt" track selector to externalise one embedded track. An empty ffmpeg path, a missing
// binary, or a non-zero exit is an error the caller falls back on. It is the one ffmpeg
// path shared by the sidecar converter and the embedded-track extractor.
func runFFmpegToSRT(ctx context.Context, ffmpegBin, src, dst string, extraArgs ...string) error {
	if ffmpegBin == "" {
		return os.ErrNotExist
	}
	tmp := dst + ".tmp.srt" // ffmpeg picks the muxer by extension, so keep .srt
	args := append([]string{"-nostdin", "-y", "-i", src}, extraArgs...)
	args = append(args, tmp)
	if err := ffrun.Run(ctx, ffmpegBin, args...); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename subtitle %s: %w", dst, err)
	}
	return nil
}

// copySidecar copies src to dst. A missing source (e.g. half a VobSub pair) is
// silently ignored.
func copySidecar(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	return CopyFile(src, dst, nil)
}
