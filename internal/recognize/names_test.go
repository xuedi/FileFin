package recognize

import "testing"

// TestParseRealNames is the regression suite for recognition, taken from a real incoming
// library: one row per distinct naming scheme found there, never one row per file. Every
// field of the result is asserted, because a title that survives while the episode number
// silently drops is still a bug.
func TestParseRealNames(t *testing.T) {
	cases := []struct {
		name    string
		isShow  bool
		title   string
		year    int
		season  int
		episode int
		part    int
		show    bool
	}{
		// Films: dotted release names. The title ends at the year.
		{name: "Dragon.Inn.1967.Criterion.Collection.BluRay.1080p.x264.FLAC.1.0-HDChina.mkv", title: "Dragon Inn", year: 1967},
		{name: "Suzume.2022.1080p.iTunes.WEB-DL.DD5.1.H.264-xiaopie@CHDWEB.mkv", title: "Suzume", year: 2022},
		{name: "Only.Yesterday.1991.BluRay.1080p.x265.10bit.3Audio.MNHD-FRDS.mkv", title: "Only Yesterday", year: 1991},
		{name: "An.Arrow.Through.the.heart.2024.1080p.IQ.WEB-DL.AAC2.0.H.264-unco@AvistaZ.mkv", title: "An Arrow Through the heart", year: 2024},
		{name: "Dog.Days.2016.CHINESE.1080p.DSNP.WEBRip.DDP5.1.x264-NOGRP.mkv", title: "Dog Days", year: 2016},
		{name: "The.Inspector.Wears.Skirts.1988.BluRay.576p-ATHEiST.mkv", title: "The Inspector Wears Skirts", year: 1988},
		// A word of the title that is also a packaging token must survive when the year cuts first.
		{name: "(1972) Intimate Confessions of a Chinese Courtesan.avi", title: "Intimate Confessions of a Chinese Courtesan", year: 1972},

		// Films: the year in front, in three spellings.
		{name: "(2024) Twilight of the Warriors Walled In.avi", title: "Twilight of the Warriors Walled In", year: 2024},
		{name: "1996 Sex and Zen II.avi", title: "Sex and Zen II", year: 1996},
		{name: "[1983]project.a.-.'a'.gai.wak.mkv", title: "project a - 'a' gai wak", year: 1983},

		// Films: the year at the end of a plain name.
		{name: "Nae Yeojachingureul Sogae Habnida Windstruck 2004.mkv", title: "Nae Yeojachingureul Sogae Habnida Windstruck", year: 2004},

		// Films: fansub decorations. Group tag, quality tag and CRC all drop away.
		{name: "[SDM] Perfect Blue [1080p] [FLAC] [44A5F0F1].mkv", title: "Perfect Blue"},
		{name: "[RH] Gyakusatsu Kikan (Dual Audio 5.1 FLAC) [8E3F476A].mkv", title: "Gyakusatsu Kikan"},
		{name: "[SubsPlease] Ooyukiumi no Kaina - Hoshi no Kenja (1080p) [78267228].mkv", title: "Ooyukiumi no Kaina - Hoshi no Kenja"},

		// Films: a number that belongs to the title, not to an episode.
		{name: "Fantasy.Magician.2.2023.2160p.WEB-DL.H265.DDP5.1-TAGWEB.mkv", title: "Fantasy Magician 2", year: 2023},
		{name: "Blade 2.mkv", title: "Blade 2"},
		{name: "Ocean's 11.mkv", title: "Ocean's 11"},

		// Films split over discs.
		{name: "The Mad Monk  CD 1.avi", title: "The Mad Monk", part: 1},
		{name: "The Mad Monk  CD 2.avi", title: "The Mad Monk", part: 2},

		// A CJK title in front of the romanised one: only the romanised part is kept, so the
		// metadata lookup has something it can match.
		{name: "狂飙.The.Knockout.2023.S01E13.1080p.WEB-DL.H264.AAC-OurTV.mkv", title: "The Knockout", year: 2023, season: 1, episode: 13, show: true},
		{name: "梁祝.The.Lovers.1994.HD080P.国粤双语.中字.mp4", title: "The Lovers", year: 1994},
		{name: "目中无人2.Eye.for.an.Eye.2024.V2.2160p.WEB-DL.H265.DDP5.1.mkv", title: "Eye for an Eye", year: 2024},
		{name: "流浪地球2.The.Wandering.Earth.Ⅱ.2023.2160p.WEB-DL.H265.AAC-GPTHD.mp4", title: "The Wandering Earth Ⅱ", year: 2023},

		// Shows: the episode title after the marker is never part of the media title. This is
		// what used to make every episode its own media.
		{name: "Deaths.Game.S01E02.The.Reason.Youre.Going.to.Hell.1080p.AMZN.WEB-DL.DDP2.0.H.264-MARK.mkv", title: "Deaths Game", season: 1, episode: 2, show: true},
		{name: "Tengoku.Daimakyo.S01E06.100.Percent.Safe.Water.1080p.DSNP.WEB-DL.AAC2.0.H.264-NTb.mkv", title: "Tengoku Daimakyo", season: 1, episode: 6, show: true},
		{name: "The.Litchi.Road.S01E01.2025.1080p.WEB-DL.AAC.H264-JKCT.mkv", title: "The Litchi Road", year: 2025, season: 1, episode: 1, show: true},
		{name: "Knight.Flower.2024.S01E07.1080p.Viu.WEB-DL.AAC2.0.x264-unco@Avistaz.mkv", title: "Knight Flower", year: 2024, season: 1, episode: 7, show: true},

		// Shows: a season with no episode still marks a show.
		{name: "Deaths.Game.S01.1080p.AMZN.WEB-DL.DDP2.0.H.264-MARK", title: "Deaths Game", season: 1, show: true},

		// Shows: the episode schemes that used to go unrecognised.
		{name: "The Long Ballad EP04 WEB-DL.mkv", title: "The Long Ballad", season: 1, episode: 4, show: true},
		{name: "Arigatou.Master.Keaton.Ep.01.[x264.AAC][BD9CCF15].mkv", title: "Arigatou Master Keaton", season: 1, episode: 1, show: true},
		{name: "[Exiled-Destiny]_Banner_Of_The_Stars_Ep06v2_(B53FD279).mkv", title: "Banner Of The Stars", season: 1, episode: 6, show: true},
		{name: "[LostYears] Call of the Night - S01E02v2 (WEB 1080p x264 10-bit AAC E-AC-3) [1C6CB102].mkv", title: "Call of the Night", season: 1, episode: 2, show: true},
		{name: "Nokdu.Flower.E01-E02.1080p.WEB-DL.H264.AAC-AppleTor.mkv", title: "Nokdu Flower", season: 1, episode: 1, show: true},
		{name: "Luoyang.2021.E01.1080P.WEB-DL.H264.AAC-JKCT.mp4", title: "Luoyang", year: 2021, season: 1, episode: 1, show: true},
		{name: "[HorribleSubs] Sakura Quest - 02 [720p].mkv", title: "Sakura Quest", season: 1, episode: 2, show: true},
		{name: "(Hi10)_InuYasha_-_001_(DVD_480p)_(a-S)_(163C0F1F).mkv", title: "InuYasha", season: 1, episode: 1, show: true},
		{name: "[Dekinai] Shouwa Genroku Rakugo Shinjuu - 01 (Director's Cut) [BA79ADED].mkv", title: "Shouwa Genroku Rakugo Shinjuu", season: 1, episode: 1, show: true},

		// A film ordinal is not an episode: "Movie - 01" counts films in a series.
		{name: "(Hi10)_InuYasha_Movie_-_02_The_Castle_Beyond_the_Looking_Glass_(BD_720p)_(Raizel)_(3A78DA41).mkv",
			title: "InuYasha Movie - 02 The Castle Beyond the Looking Glass"},

		// A bare trailing number is an episode only where a show is already established.
		{name: "Beck 04.mkv", isShow: true, title: "Beck", season: 1, episode: 4, show: true},
		{name: "Beck 04.mkv", title: "Beck 04"},
	}
	for _, c := range cases {
		p := ParseName(c.name, c.isShow)
		if p.Title != c.title || p.Year != c.year || p.Season != c.season ||
			p.Episode != c.episode || p.Part != c.part || p.IsShow != c.show {
			t.Errorf("ParseName(%q, %t) =\n got {title:%q year:%d s:%d e:%d part:%d show:%t}\nwant {title:%q year:%d s:%d e:%d part:%d show:%t}",
				c.name, c.isShow, p.Title, p.Year, p.Season, p.Episode, p.Part, p.IsShow,
				c.title, c.year, c.season, c.episode, c.part, c.show)
		}
	}
}

// TestParseFolderKeepsDottedNames guards the difference between a file name and a folder
// name: a folder has no extension, so nothing may be stripped from its end.
func TestParseFolderKeepsDottedNames(t *testing.T) {
	cases := []struct {
		name  string
		title string
		year  int
	}{
		{"beijing.rocks", "beijing rocks", 0},
		{"Always.2011", "Always", 2011},
		{"From_Beijing_with_Love.1994", "From Beijing with Love", 1994},
		{"[Arigatou] Master Keaton [c]", "Master Keaton", 0},
		{"Banner of the Stars [Exiled-Destiny]", "Banner of the Stars", 0},
		{"(Hi10) InuYasha Complete Collection [DVDRip_BDRip_480p_720p]", "InuYasha", 0},
		{"[Hi10]_InuYasha_Kanketsu-hen_[BD_720p]", "InuYasha Kanketsu-hen", 0},
		{"Shouwa Genroku Rakugo Shinjuu (2016) [BD 1080p HEVC Ma10p - FLAC 2.0]", "Shouwa Genroku Rakugo Shinjuu", 2016},
		{"The Long Ballad (2021) Complete 1080p WEB-DL AAC x264-JK", "The Long Ballad", 2021},
		{"Project A II (1987)", "Project A II", 1987},
		{"Baby.and.Me.2008.DVDRip.XviD-BiFOS", "Baby and Me", 2008},
	}
	for _, c := range cases {
		p := ParseFolder(c.name)
		if p.Title != c.title || p.Year != c.year {
			t.Errorf("ParseFolder(%q) = {title:%q year:%d}, want {title:%q year:%d}",
				c.name, p.Title, p.Year, c.title, c.year)
		}
	}
}

// TestSkipRules checks what never counts as media, in either direction.
func TestSkipRules(t *testing.T) {
	for _, name := range []string{"Movie.teaser.mkv", "Movie-trailer.mp4", "sample.mkv", "The Film preview.mkv"} {
		if !SkipFile(name) {
			t.Errorf("SkipFile(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"The Trailerpark Boys.mkv", "Previews of Coming Attractions 2001.mkv"} {
		if SkipFile(name) {
			t.Errorf("SkipFile(%q) = true, want false", name)
		}
	}
	for _, name := range []string{"Extras", "sample", "Featurettes", "Behind the Scenes"} {
		if !SkipDir(name) {
			t.Errorf("SkipDir(%q) = false, want true", name)
		}
	}
	// Only a folder that is *nothing but* an extras name is skipped; a media whose title
	// happens to contain one keeps its files.
	for _, name := range []string{"Sample This 2011", "Extra Ordinary (2019)"} {
		if SkipDir(name) {
			t.Errorf("SkipDir(%q) = true, want false", name)
		}
	}
}
