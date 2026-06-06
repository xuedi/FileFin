package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseName(t *testing.T) {
	cases := []struct {
		name string
		want Parsed
	}{
		{"(1962) Lawrence of Arabia.avi", Parsed{Title: "Lawrence of Arabia", Year: 1962, Ext: ".avi"}},
		{"(2002) Firefly - 1x1.mkv", Parsed{Title: "Firefly", Year: 2002, Season: 1, Episode: 1, Ext: ".mkv"}},
		{"The.Matrix.1999.1080p.BluRay.x264.mkv", Parsed{Title: "The Matrix", Year: 1999, Ext: ".mkv"}},
		{"(1968) 2001 A Space Odyssey Part 1.avi", Parsed{Title: "2001 A Space Odyssey Part 1", Year: 1968, Ext: ".avi"}},
		{"Some Random Movie.mp4", Parsed{Title: "Some Random Movie", Ext: ".mp4"}},
		{"Show S03E07 The Episode.mp4", Parsed{Title: "Show The Episode", Season: 3, Episode: 7, Ext: ".mp4"}},
	}
	for _, c := range cases {
		got := ParseName(c.name)
		if got != c.want {
			t.Errorf("ParseName(%q)\n got: %+v\nwant: %+v", c.name, got, c.want)
		}
	}
}

func TestExecuteProgress(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "movie.avi")
	payload := strings.Repeat("x", 100_000)
	if err := os.WriteFile(src, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	var lastCopied, lastTotal int64
	calls := 0
	_, err := Execute(Request{
		SourcePath: src, DataDir: filepath.Join(dir, "data"), Category: "C",
		Title: "Movie", Year: 2020,
		Progress: func(name string, copied, total int64) {
			calls++
			lastCopied, lastTotal = copied, total
			if name != "(2020) Movie.avi" {
				t.Errorf("progress name = %q", name)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("progress callback was never invoked")
	}
	if lastTotal != int64(len(payload)) || lastCopied != lastTotal {
		t.Fatalf("final progress copied=%d total=%d, want both %d", lastCopied, lastTotal, len(payload))
	}
}

func TestExecute(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data")

	res, err := Execute(Request{
		SourcePath: src, DataDir: data, Category: "Films - English",
		Title: "The Matrix", Year: 1999,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(data, "Films - English", "(1999) The Matrix", "(1999) The Matrix.mp4")
	if res.TargetPath != want {
		t.Fatalf("target: got %q want %q", res.TargetPath, want)
	}
	if b, _ := os.ReadFile(want); string(b) != "payload" {
		t.Fatalf("copied content: %q", b)
	}
	if res.MetaExisted {
		t.Fatal("meta.md should not have existed yet")
	}
	// Source must be untouched (copy, not move).
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source was removed: %v", err)
	}

	// Writing the stub produces a parseable meta.md.
	if err := WriteMeta(res.Folder, StubMeta("The Matrix", 1999)); err != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile(filepath.Join(res.Folder, "meta.md"))
	if err != nil || !strings.Contains(string(meta), "release: 1999") {
		t.Fatalf("meta stub: %q err=%v", meta, err)
	}
}

func TestExecuteEpisodeNamingAndKeepsMeta(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("x"), 0o644)
		return p
	}
	data := filepath.Join(dir, "data")

	// First episode: folder is new, no meta yet.
	r1, err := Execute(Request{SourcePath: mk("e1.mp4"), DataDir: data, Category: "Shows", Title: "Firefly", Year: 2002, Season: 1, Episode: 1})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(r1.TargetPath) != "(2002) Firefly - 1x1.mp4" {
		t.Fatalf("episode name: %s", r1.TargetPath)
	}
	if r1.MetaExisted {
		t.Fatal("first import: meta should not exist yet")
	}
	if err := WriteMeta(r1.Folder, StubMeta("Firefly", 2002)); err != nil {
		t.Fatal(err)
	}

	// Second episode lands in the same folder and reports meta as already present.
	r2, err := Execute(Request{SourcePath: mk("e2.mp4"), DataDir: data, Category: "Shows", Title: "Firefly", Year: 2002, Season: 1, Episode: 2})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Folder != r1.Folder {
		t.Fatalf("episodes split across folders: %s vs %s", r1.Folder, r2.Folder)
	}
	if !r2.MetaExisted {
		t.Fatal("second import should see existing meta.md")
	}
}

func TestMediaApply(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "blade.avi")
	os.WriteFile(src, []byte("MOVIE"), 0o644)
	poster := filepath.Join(dir, "poster_src")
	os.WriteFile(poster, []byte("JPEGDATA"), 0o644)
	data := filepath.Join(dir, "data")

	m := Media{
		Category:   "x_Cyberpunk",
		Title:      "Blade Runner",
		Year:       1982,
		Meta:       MetaContent{Description: "A blade runner.", Tags: []string{"sci-fi"}},
		PosterPath: poster,
		Files:      []SourceFile{{Path: src}},
	}
	folder, _, err := m.Apply(data, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(folder, "(1982) Blade Runner.avi")); err != nil || string(b) != "MOVIE" {
		t.Fatalf("media: %q err=%v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(folder, "poster.jpg")); err != nil || string(b) != "JPEGDATA" {
		t.Fatalf("poster: %q err=%v", b, err)
	}
	meta, err := os.ReadFile(filepath.Join(folder, "meta.md"))
	if err != nil || !strings.Contains(string(meta), "A blade runner.") || !strings.Contains(string(meta), "sci-fi") {
		t.Fatalf("meta: %q err=%v", meta, err)
	}
	// Title is taken from Media.Title even if Meta.Title is unset.
	if !strings.Contains(string(meta), "# Blade Runner") {
		t.Fatalf("meta title: %q", meta)
	}
}

func TestReimportSkipsUnchangedAndSidecars(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "movie.avi")
	os.WriteFile(src, []byte("ORIGINAL"), 0o644)
	poster := filepath.Join(dir, "poster_src")
	os.WriteFile(poster, []byte("ART"), 0o644)
	data := filepath.Join(dir, "data")

	m := Media{
		Category:   "C",
		Title:      "Movie",
		Year:       2020,
		Meta:       MetaContent{Description: "first"},
		PosterPath: poster,
		Files:      []SourceFile{{Path: src}},
	}

	folder, stats, err := m.Apply(data, false, true, nil)
	if err != nil || stats.Copied != 1 || stats.Skipped != 0 {
		t.Fatalf("first apply: stats=%+v err=%v", stats, err)
	}

	// Edit the already-imported sidecars; a reimport must not overwrite them, and the
	// unchanged media file (same size) must be skipped.
	mediaPath := filepath.Join(folder, "(2020) Movie.avi")
	metaPath := filepath.Join(folder, "meta.md")
	posterPath := filepath.Join(folder, "poster.jpg")
	os.WriteFile(metaPath, []byte("HAND EDITED"), 0o644)
	os.WriteFile(posterPath, []byte("HAND EDITED ART"), 0o644)

	m.Meta = MetaContent{Description: "second"}
	_, stats, err = m.Apply(data, false, true, nil)
	if err != nil || stats.Copied != 0 || stats.Skipped != 1 {
		t.Fatalf("reimport stats: %+v err=%v", stats, err)
	}
	if b, _ := os.ReadFile(metaPath); string(b) != "HAND EDITED" {
		t.Fatalf("meta.md was overwritten: %q", b)
	}
	if b, _ := os.ReadFile(posterPath); string(b) != "HAND EDITED ART" {
		t.Fatalf("poster.jpg was overwritten: %q", b)
	}

	// A changed source (different size) is re-copied on reimport.
	os.WriteFile(src, []byte("A MUCH LONGER REPLACEMENT FILE"), 0o644)
	_, stats, err = m.Apply(data, false, true, nil)
	if err != nil || stats.Copied != 1 || stats.Skipped != 0 {
		t.Fatalf("changed-file reimport stats: %+v err=%v", stats, err)
	}
	if b, _ := os.ReadFile(mediaPath); string(b) != "A MUCH LONGER REPLACEMENT FILE" {
		t.Fatalf("changed media not re-copied: %q", b)
	}

	// --force overwrites sidecars too.
	if _, _, err = m.Apply(data, true, true, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(metaPath); !strings.Contains(string(b), "second") {
		t.Fatalf("force did not rewrite meta.md: %q", b)
	}
	if b, _ := os.ReadFile(posterPath); string(b) != "ART" {
		t.Fatalf("force did not recopy poster: %q", b)
	}
}

func TestMediaApplyEpisodes(t *testing.T) {
	dir := t.TempDir()
	mk := func(n string) string {
		p := filepath.Join(dir, n)
		os.WriteFile(p, []byte("x"), 0o644)
		return p
	}
	data := filepath.Join(dir, "data")
	m := Media{
		Category: "Shows", Title: "Firefly", Year: 2002, IsShow: true,
		Files: []SourceFile{
			{Path: mk("e1.avi"), Season: 1, Episode: 1},
			{Path: mk("e2.avi"), Season: 1, Episode: 2},
		},
	}
	if _, _, err := m.Apply(data, false, false, nil); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(data, "Shows", "(2002) Firefly")
	for _, name := range []string{"(2002) Firefly - 1x1.avi", "(2002) Firefly - 1x2.avi", "meta.md"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
}

func TestExecuteRequiresFields(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.mp4")
	os.WriteFile(src, []byte("x"), 0o644)
	if _, err := Execute(Request{SourcePath: src, DataDir: dir, Title: "X", Year: 1999}); err == nil {
		t.Fatal("expected error for missing category")
	}
	if _, err := Execute(Request{SourcePath: src, DataDir: dir, Category: "C", Title: "X"}); err == nil {
		t.Fatal("expected error for missing year")
	}
}
