package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filefin/internal/meta"
	"filefin/internal/model"
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
	folder, _, err := m.Apply(data, false, true, nil, nil, 1, 1)
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

	folder, stats, err := m.Apply(data, false, true, nil, nil, 1, 1)
	if err != nil || stats.Copied != 1 || stats.Skipped != 0 {
		t.Fatalf("first apply: stats=%+v err=%v", stats, err)
	}

	// Edit the already-enriched sidecars; a reimport must not overwrite them, and the
	// unchanged media file (same size) must be skipped. The hand edit keeps the
	// mediaEnriched flag so the folder still reads as enriched.
	mediaPath := filepath.Join(folder, "(2020) Movie.avi")
	metaPath := filepath.Join(folder, "meta.md")
	posterPath := filepath.Join(folder, "poster.jpg")
	os.WriteFile(metaPath, []byte("# Movie\nHAND EDITED\n\n## technical\n - mediaEnriched: true\n"), 0o644)
	os.WriteFile(posterPath, []byte("HAND EDITED ART"), 0o644)

	m.Meta = MetaContent{Description: "second"}
	_, stats, err = m.Apply(data, false, true, nil, nil, 1, 1)
	if err != nil || stats.Copied != 0 || stats.Skipped != 1 {
		t.Fatalf("reimport stats: %+v err=%v", stats, err)
	}
	if b, _ := os.ReadFile(metaPath); !strings.Contains(string(b), "HAND EDITED") {
		t.Fatalf("meta.md was overwritten: %q", b)
	}
	if b, _ := os.ReadFile(posterPath); string(b) != "HAND EDITED ART" {
		t.Fatalf("poster.jpg was overwritten: %q", b)
	}

	// A changed source (different size) is re-copied on reimport.
	os.WriteFile(src, []byte("A MUCH LONGER REPLACEMENT FILE"), 0o644)
	_, stats, err = m.Apply(data, false, true, nil, nil, 1, 1)
	if err != nil || stats.Copied != 1 || stats.Skipped != 0 {
		t.Fatalf("changed-file reimport stats: %+v err=%v", stats, err)
	}
	if b, _ := os.ReadFile(mediaPath); string(b) != "A MUCH LONGER REPLACEMENT FILE" {
		t.Fatalf("changed media not re-copied: %q", b)
	}

	// --force overwrites sidecars too.
	if _, _, err = m.Apply(data, true, true, nil, nil, 1, 1); err != nil {
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
	if _, _, err := m.Apply(data, false, false, nil, nil, 1, 1); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(data, "Shows", "(2002) Firefly")
	for _, name := range []string{"(2002) Firefly - 1x1.avi", "(2002) Firefly - 1x2.avi", "meta.md"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
}

func TestWriteMetaTechnicalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	mc := MetaContent{
		Title: "Movie",
		Technical: []model.KV{
			{Key: "container", Value: "matroska"},
			{Key: "videoCodec", Value: "h264, hevc"},
			{Key: "mediaEnriched", Value: "true"},
		},
	}
	if err := WriteMeta(dir, mc); err != nil {
		t.Fatal(err)
	}
	parsed, err := meta.ParseFile(filepath.Join(dir, "meta.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"container": "matroska", "videoCodec": "h264, hevc", "mediaEnriched": "true"}
	got := map[string]string{}
	for _, kv := range parsed.Technical {
		got[kv.Key] = kv.Value
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("technical[%q] = %q, want %q (parsed: %+v)", k, got[k], v, parsed.Technical)
		}
	}
}

func TestWriteMetaRatingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	mc := MetaContent{
		Title:    "Movie",
		Metadata: []model.KV{{Key: "release", Value: "1999"}},
		Ratings: []model.KV{
			{Key: "imdb", Value: "8.1 (835,123 votes)"},
			{Key: "rottenTomatoes", Value: "89%"},
			{Key: "metacritic", Value: "84/100"},
		},
	}
	if err := WriteMeta(dir, mc); err != nil {
		t.Fatal(err)
	}
	parsed, err := meta.ParseFile(filepath.Join(dir, "meta.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Ratings) != 3 || parsed.Ratings[0].Key != "imdb" || parsed.Ratings[0].Value != "8.1 (835,123 votes)" {
		t.Fatalf("ratings round-trip: %+v", parsed.Ratings)
	}
	if parsed.Ratings[1].Value != "89%" || parsed.Ratings[2].Value != "84/100" {
		t.Fatalf("ratings round-trip: %+v", parsed.Ratings)
	}
}

func TestWriteMetaNoRatingsSection(t *testing.T) {
	dir := t.TempDir()
	if err := WriteMeta(dir, StubMeta("Movie", 2020)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "meta.md"))
	if strings.Contains(string(b), "## ratings") {
		t.Fatalf("empty Ratings should omit the section:\n%s", b)
	}
}

func TestWriteMetaNoTechnicalSection(t *testing.T) {
	dir := t.TempDir()
	if err := WriteMeta(dir, StubMeta("Movie", 2020)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "meta.md"))
	if strings.Contains(string(b), "## technical") {
		t.Fatalf("empty Technical should omit the section:\n%s", b)
	}
}

func TestAlreadyEnriched(t *testing.T) {
	dir := t.TempDir()
	if AlreadyEnriched(dir) {
		t.Error("missing meta.md should not read as enriched")
	}
	os.WriteFile(filepath.Join(dir, "meta.md"), []byte("# X\n\n## metadata\n - release: 2020\n"), 0o644)
	if AlreadyEnriched(dir) {
		t.Error("meta.md without the flag should not read as enriched")
	}
	os.WriteFile(filepath.Join(dir, "meta.md"), []byte("# X\n\n## technical\n - mediaEnriched: true\n"), 0o644)
	if !AlreadyEnriched(dir) {
		t.Error("mediaEnriched: true should read as enriched")
	}
	os.WriteFile(filepath.Join(dir, "meta.md"), []byte("# X\n\n## technical\n - mediaEnriched: false\n"), 0o644)
	if AlreadyEnriched(dir) {
		t.Error("mediaEnriched: false should not read as enriched")
	}
}

func TestApplyEnrichmentFlag(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "movie.avi")
	os.WriteFile(src, []byte("MOVIE"), 0o644)
	data := filepath.Join(dir, "data")

	m := Media{
		Category: "C", Title: "Movie", Year: 2020,
		Meta:  MetaContent{Description: "first"},
		Files: []SourceFile{{Path: src}},
	}
	// A technical func that records a marker key; the importer appends mediaEnriched.
	tech := func(paths []string) []model.KV {
		return []model.KV{{Key: "container", Value: "avi"}}
	}

	folder, _, err := m.Apply(data, false, false, nil, tech, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(folder, "meta.md")
	b, _ := os.ReadFile(metaPath)
	if !strings.Contains(string(b), "container: avi") || !strings.Contains(string(b), "mediaEnriched: true") {
		t.Fatalf("first apply missing technical/flag:\n%s", b)
	}
	if !AlreadyEnriched(folder) {
		t.Fatal("folder should read as enriched after first apply")
	}

	// Second apply must not re-enrich (the tech func would panic if called).
	panicTech := func(paths []string) []model.KV { panic("must not re-enrich") }
	os.WriteFile(metaPath, []byte("# Movie\nHAND\n\n## technical\n - mediaEnriched: true\n"), 0o644)
	if _, _, err := m.Apply(data, false, false, nil, panicTech, 1, 1); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(metaPath); !strings.Contains(string(b), "HAND") {
		t.Fatalf("enriched folder was re-enriched:\n%s", b)
	}

	// --force re-enriches.
	if _, _, err := m.Apply(data, true, false, nil, tech, 1, 1); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(metaPath); !strings.Contains(string(b), "container: avi") {
		t.Fatalf("--force did not re-enrich:\n%s", b)
	}
}

func TestApplyProgressLabels(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	mk := func(n string) string {
		p := filepath.Join(dir, n)
		os.WriteFile(p, []byte("payload-"+n), 0o644)
		return p
	}

	// A single-file folder uses the item position "[i/N]".
	var labels []string
	capture := func(name string, copied, total int64) {
		if total > 0 && copied >= total {
			labels = append(labels, name)
		}
	}
	single := Media{
		Category: "western", Title: "Django", Year: 1966,
		Files: []SourceFile{{Path: mk("d.avi")}},
	}
	if _, _, err := single.Apply(data, false, false, capture, nil, 3, 6); err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels[0] != "[3/6] western / (1966) Django" {
		t.Fatalf("single-file label = %v", labels)
	}

	// A multi-file folder numbers its files "[j/M]" regardless of item position.
	labels = nil
	show := Media{
		Category: "Show - Startrek", Title: "TNG", Year: 1987, IsShow: true,
		Files: []SourceFile{
			{Path: mk("e1.avi"), Season: 1, Episode: 1},
			{Path: mk("e2.avi"), Season: 1, Episode: 2},
			{Path: mk("e3.avi"), Season: 1, Episode: 3},
		},
	}
	if _, _, err := show.Apply(data, false, false, capture, nil, 1, 2); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"[1/3] Show - Startrek / (1987) TNG",
		"[2/3] Show - Startrek / (1987) TNG",
		"[3/3] Show - Startrek / (1987) TNG",
	}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Fatalf("multi-file labels = %v, want %v", labels, want)
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
