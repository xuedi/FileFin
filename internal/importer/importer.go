// Package importer copies an external media file into the data directory in the
// canonical "(YYYY) Title/" layout. It writes only inside the data directory;
// the source is read-only.
package importer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"filefin/internal/model"
)

// Parsed is the best-effort identification of a media file from its name.
type Parsed struct {
	Title   string
	Year    int
	Season  int
	Episode int
	Ext     string
}

var (
	reYearParen = regexp.MustCompile(`\((19\d{2}|20\d{2})\)`)
	reYearBare  = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	reEpX       = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,2})\b`)
	reEpSE      = regexp.MustCompile(`(?i)\bS(\d{1,2})E(\d{1,2})\b`)
	reJunk      = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|4k|x264|x265|h\.?264|h\.?265|hevc|bluray|blu-ray|brrip|bdrip|web-?dl|webrip|hdtv|dvdrip|aac|ac3|dts|hdr|remux|proper|repack|extended|unrated|imax)\b`)
	reSpaces    = regexp.MustCompile(`\s+`)
)

// ParseName extracts title, year, and (if present) season/episode from a file
// name. It handles both prefix-year names ("(1962) Lawrence of Arabia.avi") and
// suffix-year release names ("The.Matrix.1999.1080p.mkv").
func ParseName(name string) Parsed {
	base := name[:len(name)-len(filepath.Ext(name))]
	p := Parsed{Ext: strings.ToLower(filepath.Ext(name))}

	if m := reEpX.FindStringSubmatch(base); m != nil {
		p.Season, _ = strconv.Atoi(m[1])
		p.Episode, _ = strconv.Atoi(m[2])
	} else if m := reEpSE.FindStringSubmatch(base); m != nil {
		p.Season, _ = strconv.Atoi(m[1])
		p.Episode, _ = strconv.Atoi(m[2])
	}

	var titlePart string
	switch {
	case reYearParen.FindStringSubmatchIndex(base) != nil:
		loc := reYearParen.FindStringSubmatchIndex(base)
		p.Year, _ = strconv.Atoi(base[loc[2]:loc[3]])
		if loc[0] <= 2 { // year at the front: prefix style, title follows
			titlePart = base[loc[1]:]
		} else {
			titlePart = base[:loc[0]]
		}
	case reYearBare.FindStringIndex(base) != nil:
		loc := reYearBare.FindStringIndex(base)
		p.Year, _ = strconv.Atoi(base[loc[0]:loc[1]])
		if loc[0] == 0 {
			titlePart = base[loc[1]:]
		} else {
			titlePart = base[:loc[0]]
		}
	default:
		titlePart = base
	}

	p.Title = cleanTitle(titlePart)
	return p
}

func cleanTitle(s string) string {
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	s = reEpX.ReplaceAllString(s, " ")
	s = reEpSE.ReplaceAllString(s, " ")
	s = reJunk.ReplaceAllString(s, " ")
	s = reSpaces.ReplaceAllString(s, " ")
	return strings.Trim(s, " -")
}

// Request describes one import.
type Request struct {
	SourcePath string
	DataDir    string
	Category   string
	Title      string
	Year       int
	Season     int
	Episode    int
	Move       bool
	Force      bool
}

// Result reports what an import produced.
type Result struct {
	Folder      string
	TargetPath  string
	MetaExisted bool
}

// FolderName is the canonical media folder name.
func FolderName(year int, title string) string {
	return fmt.Sprintf("(%d) %s", year, title)
}

// TargetFileName is the canonical media file name, with a season/episode suffix
// when this is part of a numbered series.
func TargetFileName(year int, title string, season, episode int, ext string) string {
	base := fmt.Sprintf("(%d) %s", year, title)
	if season > 0 || episode > 0 {
		base += fmt.Sprintf(" - %dx%d", season, episode)
	}
	return base + ext
}

// Execute performs the import: it creates the category and media folders, copies
// (or moves) the file in under its canonical name, and writes a meta.md stub if
// the folder has none. It never writes outside DataDir.
func Execute(req Request) (*Result, error) {
	req.Title = strings.TrimSpace(req.Title)
	req.Category = strings.TrimSpace(req.Category)
	if req.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if req.Year <= 0 {
		return nil, fmt.Errorf("year is required")
	}
	if req.Category == "" {
		return nil, fmt.Errorf("category is required")
	}
	info, err := os.Stat(req.SourcePath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("source %q is a directory", req.SourcePath)
	}

	ext := strings.ToLower(filepath.Ext(req.SourcePath))
	dir := filepath.Join(req.DataDir, req.Category, FolderName(req.Year, req.Title))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	target := filepath.Join(dir, TargetFileName(req.Year, req.Title, req.Season, req.Episode, ext))
	if _, err := os.Stat(target); err == nil && !req.Force {
		return nil, fmt.Errorf("target already exists: %s (use --force to overwrite)", target)
	}

	_, metaErr := os.Stat(filepath.Join(dir, "meta.md"))
	if err := placeFile(req.SourcePath, target, req.Move); err != nil {
		return nil, err
	}
	return &Result{Folder: dir, TargetPath: target, MetaExisted: metaErr == nil}, nil
}

func placeFile(src, dst string, move bool) error {
	if move {
		if err := os.Rename(src, dst); err == nil {
			return nil
		}
		// Fall back to copy+remove across filesystems or read-only sources.
		if err := copyFile(src, dst); err != nil {
			return err
		}
		return os.Remove(src)
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// MetaContent is the data used to render a media folder's meta.md.
type MetaContent struct {
	Title       string
	Description string
	Plot        string
	Metadata    []model.KV
	Actors      []string
	Tags        []string
}

// StubMeta is the minimal meta.md content used when no enrichment is available.
func StubMeta(title string, year int) MetaContent {
	return MetaContent{
		Title:    title,
		Metadata: []model.KV{{Key: "release", Value: strconv.Itoa(year)}},
	}
}

// WriteMeta renders meta.md into folder, overwriting any existing file. Callers
// decide whether to call it (e.g. skip when meta.md already exists).
func WriteMeta(folder string, m MetaContent) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", m.Title)
	b.WriteString("## description\n")
	if m.Description != "" {
		b.WriteString(m.Description + "\n")
	}
	b.WriteString("\n## plot\n")
	if m.Plot != "" {
		b.WriteString(m.Plot + "\n")
	}
	b.WriteString("\n## metadata\n")
	for _, kv := range m.Metadata {
		fmt.Fprintf(&b, " - %s: %s\n", kv.Key, kv.Value)
	}
	b.WriteString("\n## actors\n")
	for _, a := range m.Actors {
		fmt.Fprintf(&b, " - %s\n", a)
	}
	b.WriteString("\n## tags\n")
	for _, t := range m.Tags {
		fmt.Fprintf(&b, " - %s\n", t)
	}
	return os.WriteFile(filepath.Join(folder, "meta.md"), []byte(b.String()), 0o644)
}
