package plex

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestSplitTags(t *testing.T) {
	got := splitTags("Crime|Drama| |Thriller")
	want := []string{"Crime", "Drama", "Thriller"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if splitTags("") != nil {
		t.Fatal("empty should be nil")
	}
}

func TestToDate(t *testing.T) {
	cases := map[string]string{
		"-309657600":           "1960-03-10",
		"":                     "",
		"2014-09-01":           "2014-09-01",
		"1999-03-31T00:00:00Z": "1999-03-31",
	}
	for in, want := range cases {
		if got := toDate(in); got != want {
			t.Errorf("toDate(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDecodeFileURL(t *testing.T) {
	cases := map[string]string{
		"file:///mnt/data/plex/films-korea/(2020)%20Alive.srt": "/mnt/data/plex/films-korea/(2020) Alive.srt",
		"file:///media/plex/show/ep.en.srt":                    "/media/plex/show/ep.en.srt",
		"":                                                     "", // not a file url
		"http://example/x.srt":                                 "", // wrong scheme
	}
	for in, want := range cases {
		if got := decodeFileURL(in); got != want {
			t.Errorf("decodeFileURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArtworkPath(t *testing.T) {
	dir := "/meta"
	hash := "a000ec1d539ef47158152454be418f84c2db89f2"
	url := "metadata://posters/tv.plex.agents.movie_19edb5"
	want := filepath.Join(dir, "Movies", "a", "000ec1d539ef47158152454be418f84c2db89f2.bundle",
		"Contents", "_combined", "posters", "tv.plex.agents.movie_19edb5")
	if got := artworkPath(dir, "Movies", hash, url); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	wantUpload := filepath.Join(dir, "Movies", "a", "000ec1d539ef47158152454be418f84c2db89f2.bundle",
		"Uploads", "posters", "im.dat")
	if got := artworkPath(dir, "Movies", hash, "upload://posters/im.dat"); got != wantUpload {
		t.Fatalf("upload got %q want %q", got, wantUpload)
	}
	if artworkPath(dir, "Movies", hash, "http://x/y.jpg") != "" {
		t.Fatal("unknown scheme should be empty")
	}
	if artworkPath("", "Movies", hash, url) != "" {
		t.Fatal("empty metadata dir should be empty")
	}
}

func TestArtworkExistenceCheck(t *testing.T) {
	root := t.TempDir()
	hash := "abc123def456"
	bundle := filepath.Join(root, "Movies", "a", "bc123def456.bundle", "Contents", "_combined", "posters")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "p1"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &DB{metadataDir: root}
	if got := d.artwork("Movies", hash, "metadata://posters/p1"); got == "" {
		t.Fatal("expected resolved poster path")
	}
	if got := d.artwork("Movies", hash, "metadata://posters/missing"); got != "" {
		t.Fatalf("missing file should resolve to empty, got %q", got)
	}
}

// --- synthetic-schema integration tests ---

// newTestDB builds a minimal Plex schema with a couple of movies and a show, then
// opens it through the package's read-only Open.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "library.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE library_sections (id INTEGER PRIMARY KEY, name TEXT, section_type INTEGER)`,
		`CREATE TABLE metadata_items (id INTEGER PRIMARY KEY, library_section_id INTEGER, parent_id INTEGER,
			metadata_type INTEGER, title TEXT, year INTEGER, "index" INTEGER, summary TEXT,
			originally_available_at TEXT, duration INTEGER, rating REAL, content_rating TEXT,
			tags_genre TEXT, tags_director TEXT, tags_writer TEXT, tags_star TEXT, hash TEXT,
			user_thumb_url TEXT, deleted_at TEXT)`,
		`CREATE TABLE media_items (id INTEGER PRIMARY KEY, metadata_item_id INTEGER)`,
		`CREATE TABLE media_parts (id INTEGER PRIMARY KEY, media_item_id INTEGER, file TEXT, "index" INTEGER, deleted_at TEXT)`,
		`CREATE TABLE media_streams (id INTEGER PRIMARY KEY, media_part_id INTEGER, stream_type_id INTEGER, url TEXT, language TEXT, codec TEXT)`,
		// Movie section with two movies.
		`INSERT INTO library_sections VALUES (1,'Films',1)`,
		`INSERT INTO metadata_items (id,library_section_id,metadata_type,title,year,summary,tags_genre,tags_star,hash) VALUES
			(10,1,1,'Alpha',2001,'a movie','Action|Drama','Jane|Joe','aaa'),
			(11,1,1,'Beta',2002,'b movie','Comedy','Sam','bbb')`,
		`INSERT INTO media_items VALUES (100,10),(101,11)`,
		`INSERT INTO media_parts (id,media_item_id,file,"index") VALUES
			(1000,100,'/mnt/plex/films/Alpha/Alpha.mkv',0),
			(1001,101,'/mnt/plex/films/Beta/Beta.mkv',0)`,
		`INSERT INTO media_streams (id,media_part_id,stream_type_id,url,language,codec) VALUES
			(1,1000,3,'file:///mnt/plex/films/Alpha/Alpha.en.srt','eng','srt')`,
		// Show section: one show, one season, two episodes.
		`INSERT INTO library_sections VALUES (2,'Series',2)`,
		`INSERT INTO metadata_items (id,library_section_id,parent_id,metadata_type,title,year,"index",hash) VALUES
			(20,2,NULL,2,'Gamma',2003,NULL,'ccc')`,
		`INSERT INTO metadata_items (id,library_section_id,parent_id,metadata_type,"index") VALUES
			(21,2,20,3,1)`,
		`INSERT INTO metadata_items (id,library_section_id,parent_id,metadata_type,title,"index") VALUES
			(22,2,21,4,'Ep1',1),(23,2,21,4,'Ep2',2)`,
		`INSERT INTO media_items VALUES (200,22),(201,23)`,
		`INSERT INTO media_parts (id,media_item_id,file,"index") VALUES
			(2000,200,'/mnt/plex/series/Gamma/S01/Gamma - s01e01.mkv',0),
			(2001,201,'/mnt/plex/series/Gamma/S01/Gamma - s01e02.mkv',0)`,
	}
	for _, s := range stmts {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("setup exec failed: %v\n%s", err, s)
		}
	}
	raw.Close()

	d, err := Open(path, "/meta")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestSections(t *testing.T) {
	d := newTestDB(t)
	secs, err := d.Sections()
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 2 {
		t.Fatalf("want 2 sections, got %d (%+v)", len(secs), secs)
	}
	byName := map[string]Section{}
	for _, s := range secs {
		byName[s.Name] = s
	}
	if got := byName["Films"]; got.Kind != "movie" || got.Count != 2 {
		t.Fatalf("Films: %+v", got)
	}
	if got := byName["Series"]; got.Kind != "show" || got.Count != 1 {
		t.Fatalf("Series: %+v", got)
	}
}

func TestSampleFilesSpread(t *testing.T) {
	d := newTestDB(t)
	files, err := d.SampleFiles([]string{"Films", "Series"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Two movie files plus one per-show file (episodes share a folder, deduped).
	if len(files) != 3 {
		t.Fatalf("want 3 sample files, got %d: %v", len(files), files)
	}
	sort.Strings(files)
	want := []string{
		"/mnt/plex/films/Alpha/Alpha.mkv",
		"/mnt/plex/films/Beta/Beta.mkv",
		"/mnt/plex/series/Gamma/S01/Gamma - s01e01.mkv",
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("got %v want %v", files, want)
	}
}

func TestItemsAndFiles(t *testing.T) {
	d := newTestDB(t)
	items, err := d.Items("")
	if err != nil {
		t.Fatal(err)
	}
	var movie, show *Item
	for i := range items {
		switch items[i].Title {
		case "Alpha":
			movie = &items[i]
		case "Gamma":
			show = &items[i]
		}
	}
	if movie == nil || show == nil {
		t.Fatalf("missing items: %+v", items)
	}
	if movie.IsShow || len(movie.Files) != 1 || movie.Files[0].Path != "/mnt/plex/films/Alpha/Alpha.mkv" {
		t.Fatalf("movie: %+v", movie)
	}
	if len(movie.Files[0].Subtitles) != 1 || movie.Files[0].Subtitles[0].Path != "/mnt/plex/films/Alpha/Alpha.en.srt" {
		t.Fatalf("movie subs: %+v", movie.Files[0].Subtitles)
	}
	if !show.IsShow || len(show.Files) != 2 {
		t.Fatalf("show: %+v", show)
	}
	if show.Files[0].Season != 1 || show.Files[0].Episode != 1 {
		t.Fatalf("show ep numbering: %+v", show.Files[0])
	}
}
