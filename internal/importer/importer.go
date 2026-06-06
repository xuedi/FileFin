// Package importer copies an external media file into the data directory in the
// canonical "(YYYY) Title/" layout. It writes only inside the data directory;
// the source is read-only.
package importer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"filefin/internal/meta"
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

// ProgressFunc is called during a file copy with the bytes copied so far and the
// total size (0 if unknown). It may be nil. Implementations must be cheap: it is
// invoked on every write.
type ProgressFunc func(name string, copied, total int64)

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
	Progress   ProgressFunc
	// Label is what the progress bar shows for this copy. Empty falls back to the
	// target file name; the batch importers set it to the "[i/N] category / title"
	// line so each copy renders on a single, self-describing line.
	Label string
}

// Result reports what an import produced.
type Result struct {
	Folder      string
	TargetPath  string
	MetaExisted bool
	Skipped     bool // target already existed with the same size; the copy was skipped
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
	// Re-copy only when the file is new or its size changed; same size is treated as
	// unchanged and skipped (a content hash would mean reading the whole source, which
	// over a remote mount costs as much as copying). --force always re-copies.
	skipped := false
	if ti, statErr := os.Stat(target); statErr == nil && !req.Force {
		skipped = ti.Size() == info.Size()
	}

	_, metaErr := os.Stat(filepath.Join(dir, "meta.md"))
	if !skipped {
		label := req.Label
		if label == "" {
			label = filepath.Base(target)
		}
		if err := placeFile(req.SourcePath, target, req.Move, req.Progress, label); err != nil {
			return nil, err
		}
	}
	return &Result{Folder: dir, TargetPath: target, MetaExisted: metaErr == nil, Skipped: skipped}, nil
}

func placeFile(src, dst string, move bool, prog ProgressFunc, label string) error {
	if move {
		if err := os.Rename(src, dst); err == nil {
			return nil
		}
		// Fall back to copy+remove across filesystems or read-only sources.
		if err := copyFile(src, dst, prog, label); err != nil {
			return err
		}
		return os.Remove(src)
	}
	return copyFile(src, dst, prog, label)
}

func copyFile(src, dst string, prog ProgressFunc, label string) error {
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
	var w io.Writer = out
	if prog != nil {
		var total int64
		if fi, err := in.Stat(); err == nil {
			total = fi.Size()
		}
		w = &progressWriter{w: out, name: label, total: total, prog: prog}
	}
	if _, err := io.Copy(w, in); err != nil {
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

// progressWriter forwards writes to w while reporting cumulative bytes to prog.
type progressWriter struct {
	w      io.Writer
	name   string
	total  int64
	copied int64
	prog   ProgressFunc
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.copied += int64(n)
	p.prog(p.name, p.copied, p.total)
	return n, err
}

// MetaContent is the data used to render a media folder's meta.md.
type MetaContent struct {
	Title       string
	Description string
	Plot        string
	Metadata    []model.KV
	Ratings     []model.KV
	Technical   []model.KV
	Actors      []string
	Tags        []string
}

// TechnicalFunc derives the `## technical` facts for a media folder from its
// copied media files. It is built in the cmd layer (it shells out to ffprobe);
// the importer appends the mediaEnriched flag itself so the flag is guaranteed.
type TechnicalFunc func(mediaPaths []string) []model.KV

// AlreadyEnriched reports whether folder's meta.md carries a truthy mediaEnriched
// flag in its `## technical` section. A missing file or flag means not enriched.
func AlreadyEnriched(folder string) bool {
	m, err := meta.ParseFile(filepath.Join(folder, "meta.md"))
	if err != nil {
		return false
	}
	for _, kv := range m.Technical {
		if kv.Key == "mediaEnriched" {
			b, _ := strconv.ParseBool(kv.Value)
			return b
		}
	}
	return false
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
	if len(m.Ratings) > 0 {
		b.WriteString("\n## ratings\n")
		for _, kv := range m.Ratings {
			fmt.Fprintf(&b, " - %s: %s\n", kv.Key, kv.Value)
		}
	}
	b.WriteString("\n## actors\n")
	for _, a := range m.Actors {
		fmt.Fprintf(&b, " - %s\n", a)
	}
	b.WriteString("\n## tags\n")
	for _, t := range m.Tags {
		fmt.Fprintf(&b, " - %s\n", t)
	}
	if len(m.Technical) > 0 {
		b.WriteString("\n## technical\n")
		for _, kv := range m.Technical {
			fmt.Fprintf(&b, " - %s: %s\n", kv.Key, kv.Value)
		}
	}
	return os.WriteFile(filepath.Join(folder, "meta.md"), []byte(b.String()), 0o644)
}

// SourceFile is one media file to import, with season/episode (0 for a movie).
type SourceFile struct {
	Path    string
	Season  int
	Episode int
}

// Media is one media folder to import, assembled by a source-specific importer
// (e.g. plex, jellyfin) and applied with Apply.
type Media struct {
	Category   string
	Title      string
	Year       int
	IsShow     bool
	Meta       MetaContent
	PosterPath string // absolute source path to a poster image, or ""
	Files      []SourceFile
}

// TargetFolder is where this media will be written under dataDir.
func (m Media) TargetFolder(dataDir string) string {
	return filepath.Join(dataDir, m.Category, FolderName(m.Year, m.Title))
}

// ApplyStats reports how many of a media's files were copied vs skipped as
// unchanged, so callers can show meaningful progress on a reimport.
type ApplyStats struct {
	Copied  int
	Skipped int
}

// Apply copies the media's files into the canonical layout, then - once per media
// folder - enriches it: it writes meta.md (with a `## technical` section and the
// mediaEnriched flag) and, when posters is true, poster.jpg. Enrichment
// runs only when the folder is not yet enriched (or force is set); an already
// enriched folder keeps its hand-edited sidecars. Media files are copied
// independently of enrichment, so a changed file is still re-copied. tech, if
// non-nil, supplies the technical facts. prog, if non-nil, receives per-file copy
// progress; itemIndex/itemTotal position this media in the batch so the progress
// label reads "[i/N] category / title" for a single file, or "[j/M] ..." numbered
// within a multi-file folder. It returns the folder and per-file stats.
func (m Media) Apply(dataDir string, force, posters bool, prog ProgressFunc, tech TechnicalFunc, itemIndex, itemTotal int) (string, ApplyStats, error) {
	if len(m.Files) == 0 {
		return "", ApplyStats{}, errors.New("no media files")
	}
	var folder string
	var targets []string
	var stats ApplyStats
	for fi, f := range m.Files {
		index, total := itemIndex, itemTotal
		if len(m.Files) > 1 {
			index, total = fi+1, len(m.Files)
		}
		label := fmt.Sprintf("[%d/%d] %s / (%d) %s", index, total, m.Category, m.Year, m.Title)
		res, err := Execute(Request{
			SourcePath: f.Path, DataDir: dataDir, Category: m.Category,
			Title: m.Title, Year: m.Year, Season: f.Season, Episode: f.Episode, Force: force,
			Progress: prog, Label: label,
		})
		if err != nil {
			return "", stats, err
		}
		folder = res.Folder
		targets = append(targets, res.TargetPath)
		if res.Skipped {
			stats.Skipped++
		} else {
			stats.Copied++
		}
	}
	if force || !AlreadyEnriched(folder) {
		mc := m.Meta
		mc.Title = m.Title
		if tech != nil {
			mc.Technical = tech(targets)
		}
		mc.Technical = append(mc.Technical, model.KV{Key: "mediaEnriched", Value: "true"})
		if err := WriteMeta(folder, mc); err != nil {
			return "", stats, err
		}
		if posters && m.PosterPath != "" {
			_ = copyArt(m.PosterPath, filepath.Join(folder, "poster.jpg"))
		}
	}
	return folder, stats, nil
}

func copyArt(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
