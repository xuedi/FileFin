// Package mediafmt enumerates the media-folder naming formats the app can enforce and
// builds the names themselves. The choice is made once in Settings and is permanent.
// FolderName and FileName are what the importer writes and what the rename engine
// compares an existing name against; the human-readable examples are shown by the
// frontend.
package mediafmt

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Valid names. The on-disk layout is always {dataDir}/{category}/{mediafolder}/; the
// format only dictates the naming style of the media folder and its files.
const (
	FileFin  = "filefin"  // year first: (2000) Title
	Jellyfin = "jellyfin" // year last, SxxEyy episodes: Title (2000)
	Plex     = "plex"     // year last, sNNeNN episodes: Title (2000)
)

// Valid reports whether s is a known format.
func Valid(s string) bool {
	switch s {
	case FileFin, Jellyfin, Plex:
		return true
	default:
		return false
	}
}

// sanitizeComponent makes a title safe as a single path component: path separators
// in the title would otherwise be read as directory boundaries. They are replaced
// with a hyphen so the name stays one human-readable folder/file.
func sanitizeComponent(s string) string {
	return strings.NewReplacer("/", "-", "\\", "-").Replace(s)
}

// FolderName is the media folder name for a format. filefin puts the year first;
// jellyfin and plex put it last. Unknown formats fall back to filefin.
func FolderName(format string, year int, title string) string {
	title = sanitizeComponent(title)
	switch format {
	case Jellyfin, Plex:
		return fmt.Sprintf("%s (%d)", title, year)
	default:
		return fmt.Sprintf("(%d) %s", year, title)
	}
}

// FileName is the media file name for a format. A non-zero season/episode appends an
// episode marker in the format's style (filefin SxE, jellyfin SxxEyy, plex sNNeNN);
// a non-zero part appends " - partN" so multi-file items do not collide. ext keeps
// its leading dot.
func FileName(format string, year int, title string, season, episode, part int, ext string) string {
	title = sanitizeComponent(title)
	var base string
	switch format {
	case Jellyfin, Plex:
		base = fmt.Sprintf("%s (%d)", title, year)
	default:
		base = fmt.Sprintf("(%d) %s", year, title)
	}
	if season > 0 || episode > 0 {
		switch format {
		case Jellyfin:
			base += fmt.Sprintf(" S%02dE%02d", season, episode)
		case Plex:
			base += fmt.Sprintf(" - s%02de%02d", season, episode)
		default:
			base += fmt.Sprintf(" - %dx%d", season, episode)
		}
	}
	if part > 0 {
		base += fmt.Sprintf(" - part%d", part)
	}
	return base + ext
}

// rePart matches the " - partN" suffix FileName appends to a multi-part media file.
var rePart = regexp.MustCompile(` - part(\d+)$`)

// PartFromName reads a file's part number back out of its name, returning 0 when it has
// none. The cache records a file's season and episode but never its part, so re-deriving
// a multi-part film's names without this would collapse every part onto one name.
func PartFromName(name string) int {
	m := rePart.FindStringSubmatch(strings.TrimSuffix(name, filepath.Ext(name)))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
