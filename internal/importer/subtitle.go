package importer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"filefin/internal/subtitle"
)

// Subtitle is one external subtitle file to place beside its video. Ext is the
// lowercased source extension (".srt", ".ass", ".sub", ...); Language is the raw
// language tag (possibly empty, resolved against the default at copy time). For a
// VobSub pair, Path may point at either the .sub or the .idx; both are copied.
type Subtitle struct {
	Path     string
	Language string
	Ext      string
}

// subtitleExts are the sidecar extensions the importers recognise. VobSub is the
// one tolerated bitmap format (copied verbatim); the rest are text.
var subtitleExts = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".sub": true,
	".idx": true, ".vtt": true, ".sup": true, ".smi": true,
}

// SubtitleTargetName derives a subtitle's canonical name from its video's target
// name: the video base (with its own extension dropped) plus ".<lang><sext>", so
// the subtitle inherits any " - SxE" / " - partN" suffix. sext includes the dot.
func SubtitleTargetName(videoTargetName, lang, sext string) string {
	base := strings.TrimSuffix(videoTargetName, filepath.Ext(videoTargetName))
	return base + "." + lang + sext
}

// FindSidecarSubtitles returns the external subtitle files sitting next to a video
// that match its base name, with an optional language infix. A VobSub .sub/.idx
// pair is attached once. It reads the video's directory; a read error yields none.
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

// placeSubtitles copies (or, for ASS/SSA, converts to SRT) each subtitle next to
// the placed video under "<video-base>.<lang>[.<n>].<ext>". It is best-effort:
// every failure is swallowed so a subtitle never fails the import. force re-places
// even when an unchanged copy already exists.
func placeSubtitles(videoTarget, defaultLang, ffmpegPath string, subs []Subtitle, force bool) {
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
		switch ext {
		case ".sub", ".idx":
			dst := name(lang, ".sub")
			dstBase := strings.TrimSuffix(dst, ".sub")
			srcBase := strings.TrimSuffix(s.Path, filepath.Ext(s.Path))
			_ = copySidecar(srcBase+".sub", dstBase+".sub", force)
			_ = copySidecar(srcBase+".idx", dstBase+".idx", force)
		case ".ass", ".ssa":
			dst := name(lang, ".srt")
			if err := convertSubtitle(ffmpegPath, s.Path, dst, force); err != nil {
				// ffmpeg missing or failed: keep the original styling verbatim under
				// the same base but its real extension.
				_ = copySidecar(s.Path, strings.TrimSuffix(dst, ".srt")+ext, force)
			}
		default:
			_ = copySidecar(s.Path, name(lang, ext), force)
		}
	}
}

// convertSubtitle transcodes a text subtitle (ASS/SSA) to SRT with ffmpeg, keeping
// dialogue and timing and dropping styling. It writes through a temp file so a
// crashed conversion never leaves a partial .srt. An empty ffmpegPath, a missing
// binary, or a non-zero exit is reported as an error for the caller to fall back on.
func convertSubtitle(ffmpegPath, src, dst string, force bool) error {
	if ffmpegPath == "" {
		return os.ErrNotExist
	}
	if !force {
		if _, err := os.Stat(dst); err == nil {
			return nil
		}
	}
	tmp := dst + ".tmp.srt" // ffmpeg picks the muxer by extension, so keep .srt
	cmd := exec.Command(ffmpegPath, "-nostdin", "-y", "-i", src, tmp)
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// copySidecar copies src to dst, skipping when an unchanged same-size copy already
// exists (unless force). A missing source (e.g. half a VobSub pair) is silently
// ignored.
func copySidecar(src, dst string, force bool) error {
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !force {
		if di, err := os.Stat(dst); err == nil && di.Size() == si.Size() {
			return nil
		}
	}
	return copyFile(src, dst, nil, "")
}
