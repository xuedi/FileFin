package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"filefin/internal/recognize"
)

// buildShape materialises a folder tree as zero-byte files, so an entry-level test reads as
// the tree it describes. Every path is relative to the import root.
func buildShape(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// episodes lists an item's (season, episode) pairs, sorted. A season collision is only
// visible in this set, never in the item's title or file count.
func episodes(it importItem) [][2]int {
	out := make([][2]int, 0, len(it.probes))
	for _, p := range it.probes {
		out = append(out, [2]int{p.Season, p.Episode})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func byTitle(items []importItem) map[string]importItem {
	m := map[string]importItem{}
	for _, it := range items {
		m[it.Title] = it
	}
	return m
}

// TestEntryIsOneMedia is the core of entry-scoped grouping: a folder of episodes is one
// media no matter how its files are named, because the folder is what the admin dropped.
func TestEntryIsOneMedia(t *testing.T) {
	cases := []struct {
		name   string
		paths  []string
		title  string
		year   int
		files  int
		isShow bool
	}{
		{
			name: "episode titles in the file names",
			paths: []string{
				"Deaths.Game.S01.1080p.AMZN.WEB-DL.DDP2.0.H.264-MARK/Deaths.Game.S01E01.Death.1080p.AMZN.WEB-DL.DDP2.0.H.264-MARK.mkv",
				"Deaths.Game.S01.1080p.AMZN.WEB-DL.DDP2.0.H.264-MARK/Deaths.Game.S01E02.The.Reason.Youre.Going.to.Hell.1080p.AMZN.WEB-DL.DDP2.0.H.264-MARK.mkv",
			},
			title: "Deaths Game", files: 2, isShow: true,
		},
		{
			name: "fansub numbering with a quality tag",
			paths: []string{
				"[HorribleSubs] Sakura Quest [720p]/[HorribleSubs] Sakura Quest - 01 [720p].mkv",
				"[HorribleSubs] Sakura Quest [720p]/[HorribleSubs] Sakura Quest - 02 [720p].mkv",
			},
			title: "Sakura Quest", files: 2, isShow: true,
		},
		{
			name: "Ep.NN numbering",
			paths: []string{
				"[Arigatou] Master Keaton [c]/Arigatou.Master.Keaton.Ep.01.[x264.AAC][BD9CCF15].mkv",
				"[Arigatou] Master Keaton [c]/Arigatou.Master.Keaton.Ep.02.[x264.AAC][2AA8BB01].mkv",
			},
			title: "Master Keaton", files: 2, isShow: true,
		},
		{
			name: "EPNN numbering",
			paths: []string{
				"The Long Ballad (2021) Complete 1080p WEB-DL AAC x264-JK/The Long Ballad EP01 WEB-DL.mkv",
				"The Long Ballad (2021) Complete 1080p WEB-DL AAC x264-JK/The Long Ballad EP02 WEB-DL.mkv",
			},
			title: "The Long Ballad", year: 2021, files: 2, isShow: true,
		},
		{
			name: "the group name stuck to the folder is dropped",
			paths: []string{
				"Call of the Night LostYears/[LostYears] Call of the Night - S01E01 (WEB 1080p x264) [3E4551E0].mkv",
				"Call of the Night LostYears/[LostYears] Call of the Night - S01E02v2 (WEB 1080p x264) [1C6CB102].mkv",
			},
			title: "Call of the Night", files: 2, isShow: true,
		},
		{
			name:  "a cryptic file name borrows the folder's title and year",
			paths: []string{"Baby.and.Me.2008.DVDRip.XviD-BiFOS/bifos-babyme.avi"},
			title: "Baby and Me", year: 2008, files: 1,
		},
		{
			name: "a useless folder name loses to the file",
			paths: []string{
				"LLBJ/(1990) Liu Lang Bei Jing.avi",
			},
			title: "Liu Lang Bei Jing", year: 1990, files: 1,
		},
		{
			name: "a sample folder is not part of the media",
			paths: []string{
				"Love.For.Life.2011.720p.BluRay.x264.DTS-HDChina/Love.For.Life.2011.720p.BluRay.x264.DTS-HDChina.mkv",
				"Love.For.Life.2011.720p.BluRay.x264.DTS-HDChina/sample/Love.For.Life.sample.mkv",
			},
			title: "Love For Life", year: 2011, files: 1,
		},
		{
			name: "a film split over discs stays one media",
			paths: []string{
				"The Mad Monk/The Mad Monk/The Mad Monk  CD 1.avi",
				"The Mad Monk/The Mad Monk/The Mad Monk  CD 2.avi",
			},
			title: "The Mad Monk", files: 2,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items, err := scanImportFolder(buildShape(t, c.paths...))
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 {
				t.Fatalf("got %d items, want 1: %+v", len(items), items)
			}
			it := items[0]
			if it.Title != c.title || it.Year != c.year || it.Files != c.files || it.IsShow != c.isShow {
				t.Errorf("got {title:%q year:%d files:%d show:%t}, want {title:%q year:%d files:%d show:%t}",
					it.Title, it.Year, it.Files, it.IsShow, c.title, c.year, c.files, c.isShow)
			}
		})
	}
}

// TestLooseFilesMerge checks the other grouping rule: files lying loose in the import root
// are grouped by what they recognise to, so two episodes of one show are one media.
func TestLooseFilesMerge(t *testing.T) {
	root := buildShape(t,
		"Beauty.of.Resilience.S01E01.2023.1080p.WEB-DL.AAC.H264-JKCT.mp4",
		"Beauty.of.Resilience.S01E02.2023.1080p.WEB-DL.AAC.H264-JKCT.mp4",
		"A.Quiet.Dream.2016.1080p.AMZN.WEB-DL.DDP2.0.H.264-NWD.mkv",
	)
	items, err := scanImportFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(items), items)
	}
	got := byTitle(items)
	show, ok := got["Beauty of Resilience"]
	if !ok || show.Files != 2 || !show.IsShow || show.Year != 2023 {
		t.Errorf("show = %+v, want 2 files, isShow, year 2023", show)
	}
	film, ok := got["A Quiet Dream"]
	if !ok || film.Files != 1 || film.IsShow {
		t.Errorf("film = %+v, want 1 file, not a show", film)
	}
}

// TestMixedEntrySplitsShowsFromFilms is the InuYasha shape: one entry holding a long run of
// episodes, a second run under its own name, and a folder of films. The runs fold into one
// show with a season each; the films stay separate media.
func TestMixedEntrySplitsShowsFromFilms(t *testing.T) {
	entry := "(Hi10) InuYasha Complete Collection [DVDRip_BDRip_480p_720p]"
	root := buildShape(t,
		entry+"/[Hi10]_InuYasha_[DVD_480p]/(Hi10)_InuYasha_-_001_(DVD_480p)_(a-S)_(163C0F1F).mkv",
		entry+"/[Hi10]_InuYasha_[DVD_480p]/(Hi10)_InuYasha_-_002_(DVD_480p)_(a-S)_(F15BD6AB).mkv",
		entry+"/[Hi10]_InuYasha_[DVD_480p]/(Hi10)_InuYasha_-_OP_(DVD_480p)_(a-S)_(ADB93909).mkv",
		entry+"/[Hi10]_InuYasha_Kanketsu-hen_[BD_720p]/(Hi10)_InuYasha_Kanketsu-hen_-_01_(BD_720p)_(Raizel)_(1175DA34).mkv",
		entry+"/[Hi10]_InuYasha_Kanketsu-hen_[BD_720p]/(Hi10)_InuYasha_Kanketsu-hen_-_02_(BD_720p)_(Raizel)_(57322D9F).mkv",
		entry+"/(Hi10) InuYasha Movies 1-4 [720p]/(Hi10)_Inuyasha_Movie_-_01_Affections_Touching_Across_Time_(BD_720p)_(Raizel)_(008EA274).mkv",
		entry+"/(Hi10) InuYasha Movies 1-4 [720p]/(Hi10)_InuYasha_Movie_-_02_The_Castle_Beyond_the_Looking_Glass_(BD_720p)_(Raizel)_(3A78DA41).mkv",
	)
	items, err := scanImportFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 1 show + 2 films: %+v", len(items), items)
	}
	var show importItem
	films := 0
	for _, it := range items {
		if it.IsShow {
			show = it
		} else {
			films++
			if !strings.HasPrefix(it.Title, "InuYasha Movie") && !strings.HasPrefix(it.Title, "Inuyasha Movie") {
				t.Errorf("film title = %q, want an InuYasha Movie", it.Title)
			}
			if it.Files != 1 {
				t.Errorf("film %q has %d files, want 1", it.Title, it.Files)
			}
		}
	}
	if films != 2 {
		t.Errorf("got %d films, want 2", films)
	}
	if show.Title != "InuYasha" || show.Files != 5 {
		t.Errorf("show = {title:%q files:%d}, want {InuYasha 5}", show.Title, show.Files)
	}
	// The two runs must land on different seasons, or the importer would write two files to
	// the same name; the opening has no episode number of its own and becomes a special.
	want := [][2]int{{0, 1}, {1, 1}, {1, 2}, {2, 1}, {2, 2}}
	got := episodes(show)
	if len(got) != len(want) {
		t.Fatalf("episodes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("episodes = %v, want %v", got, want)
		}
	}
}

// TestFilmFolderSplitsPerSubfolder is the Project A shape: an entry holding two films, each
// in its own folder, plus extras that are not media at all.
func TestFilmFolderSplitsPerSubfolder(t *testing.T) {
	entry := "Jackie Chan - Project A (1983) & Project A II (1987)"
	root := buildShape(t,
		entry+"/Project A (1983)/[1983]project.a.-.'a'.gai.wak.mkv",
		entry+"/Project A (1983)/Extras/[1983]project.a.-.deleted.scenes.mkv",
		entry+"/Project A II (1987)/[1987]project.a.II.-.'a'.gai.wak.juk.jap.mkv",
	)
	items, err := scanImportFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 films: %+v", len(items), items)
	}
	got := byTitle(items)
	for title, year := range map[string]int{"Project A": 1983, "Project A II": 1987} {
		it, ok := got[title]
		if !ok {
			t.Fatalf("missing %q, got %+v", title, items)
		}
		if it.Year != year || it.Files != 1 || it.IsShow {
			t.Errorf("%q = {year:%d files:%d show:%t}, want {%d 1 false}", title, it.Year, it.Files, it.IsShow, year)
		}
	}
}

// TestConfidenceReportsDoubt checks that the sanity checks reach the page: a clean entry is
// trusted outright, a nameless one is not, and the reason is spelled out.
func TestConfidenceReportsDoubt(t *testing.T) {
	clean, err := scanImportFolder(buildShape(t,
		"The.Litchi.Road.S01.2025.1080p.WEB-DL.AAC.H264-JKCT/The.Litchi.Road.S01E01.2025.1080p.WEB-DL.AAC.H264-JKCT.mkv",
		"The.Litchi.Road.S01.2025.1080p.WEB-DL.AAC.H264-JKCT/The.Litchi.Road.S01E02.2025.1080p.WEB-DL.AAC.H264-JKCT.mkv",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(clean) != 1 || clean[0].Confidence != string(recognize.High) || len(clean[0].Doubts) != 0 {
		t.Errorf("clean entry = %+v, want a single high-confidence item with no doubts", clean)
	}

	// Two unrelated films in one folder: neither a show nor one film, so the film-file-count
	// check must condemn it rather than let it read as trustworthy.
	messy, err := scanImportFolder(buildShape(t,
		"holiday dump/some.thing.mkv",
		"holiday dump/another.thing.mkv",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(messy) != 1 {
		t.Fatalf("got %d items, want 1: %+v", len(messy), messy)
	}
	if messy[0].Confidence != string(recognize.Low) {
		t.Errorf("confidence = %q, want low", messy[0].Confidence)
	}
	if !hasDoubt(messy[0], recognize.CheckFilmFiles) {
		t.Errorf("doubts = %v, want the film-file-count check", messy[0].Doubts)
	}
}

func hasDoubt(it importItem, want recognize.Check) bool {
	for _, d := range it.Doubts {
		if d == string(want) {
			return true
		}
	}
	return false
}
