package mediafmt

import "testing"

func TestFolderName(t *testing.T) {
	cases := []struct {
		format string
		want   string
	}{
		{FileFin, "(1999) The Matrix"},
		{Jellyfin, "The Matrix (1999)"},
		{Plex, "The Matrix (1999)"},
	}
	for _, c := range cases {
		if got := FolderName(c.format, 1999, "The Matrix"); got != c.want {
			t.Errorf("FolderName(%q) = %q, want %q", c.format, got, c.want)
		}
	}
	// Path separators in the title are sanitized to hyphens.
	if got := FolderName(FileFin, 2014, "Summer/Autumn"); got != "(2014) Summer-Autumn" {
		t.Errorf("sanitize folder = %q", got)
	}
}

func TestFileNameMovie(t *testing.T) {
	cases := []struct {
		format string
		want   string
	}{
		{FileFin, "(1999) The Matrix.mkv"},
		{Jellyfin, "The Matrix (1999).mkv"},
		{Plex, "The Matrix (1999).mkv"},
	}
	for _, c := range cases {
		if got := FileName(c.format, 1999, "The Matrix", 0, 0, 0, ".mkv"); got != c.want {
			t.Errorf("FileName(%q) movie = %q, want %q", c.format, got, c.want)
		}
	}
}

func TestFileNameEpisodeAndPart(t *testing.T) {
	cases := []struct {
		format     string
		season, ep int
		part       int
		want       string
	}{
		{FileFin, 1, 3, 0, "(2002) Firefly - 1x3.mkv"},
		{Jellyfin, 1, 3, 0, "Firefly (2002) S01E03.mkv"},
		{Plex, 1, 3, 0, "Firefly (2002) - s01e03.mkv"},
		{FileFin, 0, 0, 2, "(1999) The Matrix - part2.mkv"},
	}
	for _, c := range cases {
		title := "Firefly"
		year := 2002
		if c.part > 0 {
			title, year = "The Matrix", 1999
		}
		if got := FileName(c.format, year, title, c.season, c.ep, c.part, ".mkv"); got != c.want {
			t.Errorf("FileName(%q, %d/%d, part %d) = %q, want %q", c.format, c.season, c.ep, c.part, got, c.want)
		}
	}
}
