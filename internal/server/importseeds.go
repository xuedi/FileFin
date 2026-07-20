package server

import (
	"strings"

	"filefin/internal/library"
)

// seed is a marker whose region or kind is known before any import has happened, paired
// with the words a category must declare (or be named after) for the seed to mean anything
// to it. A library with no Chinese category is never told about IQIYI.
type seed struct {
	marker string
	hints  []string
}

// seeds bridges the first few imports of a cold library, and nothing more. It is
// deliberately short: HorribleSubs shut down in 2020 and SubsPlease took its place, which is
// how fast such a list rots. Real evidence always outvotes it, so an entry that goes stale
// costs a wrong preselect exactly until the first import corrects it.
var seeds = []seed{
	// Region-locked streaming platforms. The global ones (Netflix, Amazon, Disney) are
	// deliberately absent: they carry every region and say nothing about where media belongs.
	{"plat:iqiyi", []string{"chinese", "mandarin", "china", "cn"}},
	{"plat:youku", []string{"chinese", "mandarin", "china", "cn"}},
	{"plat:mgtv", []string{"chinese", "mandarin", "china", "cn"}},
	{"plat:tencent", []string{"chinese", "mandarin", "china", "cn"}},
	{"plat:wetv", []string{"chinese", "mandarin", "china", "cn"}},
	{"plat:bilibili", []string{"chinese", "mandarin", "china", "cn"}},
	{"plat:tving", []string{"korean", "korea", "kr"}},
	{"plat:viu", []string{"korean", "korea", "kr"}},
	{"plat:wavve", []string{"korean", "korea", "kr"}},
	{"plat:kocowa", []string{"korean", "korea", "kr"}},
	{"plat:coupang", []string{"korean", "korea", "kr"}},
	{"plat:abema", []string{"japanese", "japan", "anime", "jp"}},
	{"plat:u-next", []string{"japanese", "japan", "anime", "jp"}},
	// Fansub groups that only ever release anime.
	{"grp:subsplease", []string{"anime"}},
	{"tag:subsplease", []string{"anime"}},
	{"grp:horriblesubs", []string{"anime"}},
	{"tag:horriblesubs", []string{"anime"}},
	{"grp:erai-raws", []string{"anime"}},
	{"tag:erai-raws", []string{"anime"}},
	{"tag:judas", []string{"anime"}},
	{"tag:ember", []string{"anime"}},
	{"tag:asw", []string{"anime"}},
}

// seedVotes casts the seeded vocabulary's votes: a shipped marker speaks only for the
// categories whose declared markers - or, on a library that has declared nothing yet, whose
// own name - match what the seed is about.
func (p *predictor) seedVotes(it importItem, eligible []library.Category, cast func(int64, int, string)) {
	found := map[string]bool{}
	for _, m := range itemMarkers(it) {
		found[m] = true
	}
	for _, sd := range seeds {
		if !found[sd.marker] {
			continue
		}
		for _, c := range eligible {
			if hint, ok := seedMatches(c, sd.hints); ok {
				cast(c.ID, seedWeight, markerValue(sd.marker)+" usually means "+hint+", like "+c.Alias)
			}
		}
	}
}

// seedMatches reports whether a category is about one of a seed's hints, and which one. The
// declared markers are asked first; the folder name and alias stand in for them on a library
// that has declared nothing, which is exactly the cold start the seeds exist for.
func seedMatches(c library.Category, hints []string) (string, bool) {
	declared := append(append([]string{}, c.Markers.Languages...), c.Markers.Countries...)
	declared = append(declared, c.Markers.Keywords...)
	for _, hint := range hints {
		for _, d := range declared {
			if strings.EqualFold(strings.TrimSpace(d), hint) {
				return hint, true
			}
		}
	}
	name := strings.ToLower(c.Leaf + " " + c.Alias)
	for _, hint := range hints {
		if len(hint) > 2 && containsWord(name, hint) {
			return hint, true
		}
	}
	return "", false
}
