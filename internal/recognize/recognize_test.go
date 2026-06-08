package recognize

import (
	"path/filepath"
	"testing"
)

// TestFromPathMatrix is the behavioural contract for import-folder recognition:
// it feeds a path (relative to the import root, slashes rewritten to the OS
// separator) and asserts the derived title, year, season, episode, and show flag.
func TestFromPathMatrix(t *testing.T) {
	cases := []struct {
		path    string
		title   string
		year    int
		season  int
		episode int
		isShow  bool
	}{
		// Movies, flat in the import root.
		{"(1962) Lawrence of Arabia.avi", "Lawrence of Arabia", 1962, 0, 0, false},
		{"The.Matrix.1999.1080p.BluRay.x264.mkv", "The Matrix", 1999, 0, 0, false},
		{"Blade Runner (1982).mp4", "Blade Runner", 1982, 0, 0, false},
		{"Some Movie Without A Year.mp4", "Some Movie Without A Year", 0, 0, 0, false},
		// Trailing-number titles must not be read as episodes without show context.
		{"Blade 2.mkv", "Blade 2", 0, 0, 0, false},
		{"Ocean's 11.mkv", "Ocean's 11", 0, 0, 0, false},
		// Movie inside a generic subfolder: the folder is not a season, not a title source.
		{"Movies/(1999) The Matrix.mkv", "The Matrix", 1999, 0, 0, false},

		// Episodes, flat in the import root (explicit markers).
		{"Firefly - S01E03.mkv", "Firefly", 0, 1, 3, true},
		{"Firefly 2x05.mkv", "Firefly", 0, 2, 5, true},
		{"Show - Season 1 Episode 7.mkv", "Show", 0, 1, 7, true},
		{"Show - E50.mkv", "Show", 0, 1, 50, true},
		{"Show S01E123.mkv", "Show", 0, 1, 123, true},

		// Show folder, flat episodes.
		{"Firefly/Firefly - 1x01.mkv", "Firefly", 0, 1, 1, true},

		// Season subfolders: bare episode numbers honoured, season from the folder.
		{"Firefly/Season 1/Firefly - 01.mkv", "Firefly", 0, 1, 1, true},
		{"Breaking Bad/Season 02/05.mkv", "Breaking Bad", 0, 2, 5, true},
		{"Show/S03/Show - 12.mkv", "Show", 0, 3, 12, true},
		{"Naruto/Season 1/Naruto - 090.mkv", "Naruto", 0, 1, 90, true},
		// Title and year both borrowed from the show folder.
		{"(2008) Breaking Bad/Season 2/05.mkv", "Breaking Bad", 2008, 2, 5, true},
		// Episode-word name inside a season folder.
		{"Breaking Bad/Season 2/Episode 05.mkv", "Breaking Bad", 0, 2, 5, true},

		// Specials folder is season 0.
		{"Doctor Who/Specials/Doctor Who - 01.mkv", "Doctor Who", 0, 0, 1, true},

		// Explicit name season wins over a conflicting season folder.
		{"Show/Season 9/Show - S02E04.mkv", "Show", 0, 2, 4, true},
	}
	for _, c := range cases {
		p := FromPath(filepath.FromSlash(c.path))
		if p.Title != c.title || p.Year != c.year || p.Season != c.season ||
			p.Episode != c.episode || p.IsShow != c.isShow {
			t.Errorf("FromPath(%q) = {title:%q year:%d s:%d e:%d show:%t}, want {title:%q year:%d s:%d e:%d show:%t}",
				c.path, p.Title, p.Year, p.Season, p.Episode, p.IsShow,
				c.title, c.year, c.season, c.episode, c.isShow)
		}
	}
}

// TestParseNameExt checks the extension is captured and lowercased; FromPath relies
// on ParseName for this.
func TestParseNameExt(t *testing.T) {
	if got := ParseName("Movie.MKV", false).Ext; got != ".mkv" {
		t.Errorf("ext = %q, want .mkv", got)
	}
}
