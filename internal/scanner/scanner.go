// Package scanner walks the data directory and turns the on-disk layout into
// domain records. It never writes; the filesystem is the single source of truth.
package scanner

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"filefin/internal/meta"
	"filefin/internal/model"
	"filefin/internal/state"
	"filefin/internal/transcode"
)

var (
	folderRe = regexp.MustCompile(`^\((\d{4})\)\s+(.+)$`)
	// "(YYYY) Title" optionally followed by " - SxE" and/or " - partN".
	fileRe = regexp.MustCompile(`^\((\d{4})\)\s+(.+?)(?:\s+-\s+(\d+)x(\d+))?(?:\s+-\s+part\d+)?$`)
)

var videoExts = map[string]bool{
	".mp4": true, ".webm": true, ".mkv": true, ".avi": true,
	".mov": true, ".m4v": true, ".ts": true, ".m2ts": true,
}

// MediaID is a stable identifier derived from the category and folder name, so
// it survives cache rebuilds.
func MediaID(category, folder string) string {
	sum := sha1.Sum([]byte(category + "/" + folder))
	return hex.EncodeToString(sum[:])[:12]
}

// Scan walks the data directory and returns the catalog plus any issues found.
func Scan(dataDir string) (*model.Scan, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}
	s := &model.Scan{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		cat := model.Category{Name: e.Name(), Path: filepath.Join(dataDir, e.Name())}
		media, issues := scanCategory(cat)
		cat.Media = media
		s.Categories = append(s.Categories, cat)
		s.Issues = append(s.Issues, issues...)
	}
	sort.Slice(s.Categories, func(i, j int) bool { return s.Categories[i].Name < s.Categories[j].Name })
	return s, nil
}

func scanCategory(cat model.Category) ([]model.Media, []string) {
	entries, err := os.ReadDir(cat.Path)
	if err != nil {
		return nil, []string{fmt.Sprintf("cannot read category %q: %v", cat.Name, err)}
	}
	var out []model.Media
	var issues []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.IsDir() {
			issues = append(issues, fmt.Sprintf("%s: stray file %q (expected media folders only)", cat.Name, e.Name()))
			continue
		}
		mm := folderRe.FindStringSubmatch(e.Name())
		if mm == nil {
			issues = append(issues, fmt.Sprintf("%s: folder %q does not match \"(YYYY) Title\"", cat.Name, e.Name()))
			continue
		}
		year, _ := strconv.Atoi(mm[1])
		m := model.Media{
			ID:       MediaID(cat.Name, e.Name()),
			Category: cat.Name,
			Folder:   e.Name(),
			Path:     filepath.Join(cat.Path, e.Name()),
			Year:     year,
			Title:    strings.TrimSpace(mm[2]),
		}
		issues = append(issues, scanMediaFolder(&m)...)
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Year != out[j].Year {
			return out[i].Year < out[j].Year
		}
		return out[i].Title < out[j].Title
	})
	return out, issues
}

func scanMediaFolder(m *model.Media) []string {
	entries, err := os.ReadDir(m.Path)
	if err != nil {
		return []string{fmt.Sprintf("%s/%s: cannot read: %v", m.Category, m.Folder, err)}
	}
	var issues []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)
		ext := strings.ToLower(filepath.Ext(name))
		base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
		switch {
		case strings.HasSuffix(lower, transcode.OptimizedExt):
			// Derived direct-play copy of a source file, discovered live at playback
			// time (transcode.OptimizedSibling), never a media file of its own.
		case lower == state.FileName:
			// Per-user watch state, app-written and read live at request time; not a
			// catalog input, so the scan ignores it.
		case base == "poster":
			m.Poster = filepath.Join(m.Path, name)
		case lower == "meta.md":
			mt, err := meta.ParseFile(filepath.Join(m.Path, name))
			if err != nil {
				issues = append(issues, fmt.Sprintf("%s/%s: meta.md: %v", m.Category, m.Folder, err))
			} else {
				m.Meta = mt
			}
		case videoExts[ext]:
			m.Files = append(m.Files, parseMediaFile(filepath.Join(m.Path, name), name, ext))
		}
	}
	if len(m.Files) == 0 {
		issues = append(issues, fmt.Sprintf("%s/%s: no media files found", m.Category, m.Folder))
	}
	sort.Slice(m.Files, func(i, j int) bool {
		a, b := m.Files[i], m.Files[j]
		if a.Season != b.Season {
			return a.Season < b.Season
		}
		if a.Episode != b.Episode {
			return a.Episode < b.Episode
		}
		return a.Name < b.Name
	})
	return issues
}

func parseMediaFile(path, name, ext string) model.MediaFile {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	mf := model.MediaFile{Path: path, Name: name, Ext: ext}
	if mm := fileRe.FindStringSubmatch(base); mm != nil && mm[3] != "" {
		mf.Season, _ = strconv.Atoi(mm[3])
		mf.Episode, _ = strconv.Atoi(mm[4])
	}
	return mf
}
