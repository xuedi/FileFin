package importer

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// imageExts are the poster image formats recognised beside a source video.
var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}

// FindSidecarPoster returns the path of a poster image sitting next to a source video:
// first one named after the video ("<base>.<imgext>"), otherwise a folder-level
// "poster.<imgext>". It reads the video's directory; a read error or no match yields "".
func FindSidecarPoster(videoPath string) string {
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	folderPoster := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !imageExts[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if stem == base {
			return filepath.Join(dir, name) // a per-video poster wins outright
		}
		if strings.EqualFold(stem, "poster") && folderPoster == "" {
			folderPoster = filepath.Join(dir, name)
		}
	}
	return folderPoster
}

// PlacePoster copies a source poster into the placed video's media folder as
// "poster.<ext>" and returns that base name for the media row. Best-effort like the
// subtitles: a missing or unreadable source yields an error the caller ignores. A
// source with no usable image extension (Plex bundle posters carry none) is
// content-sniffed so it still lands under a sensible extension.
func PlacePoster(srcPoster, videoTarget string) (string, error) {
	ext := strings.ToLower(filepath.Ext(srcPoster))
	if !imageExts[ext] {
		ext = sniffImageExt(srcPoster)
	}
	name := "poster" + ext
	dst := filepath.Join(filepath.Dir(videoTarget), name)
	if err := copySidecar(srcPoster, dst); err != nil {
		return "", err
	}
	return name, nil
}

// sniffImageExt reads a file's leading bytes and returns the matching image
// extension, defaulting to ".jpg" when the type is JPEG or unrecognised.
func sniffImageExt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ".jpg"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	switch http.DetectContentType(buf[:n]) {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".jpg"
	}
}
