package recognize

import (
	"reflect"
	"testing"
)

func TestMarkers(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{"The.Knockout.2023.1080p.WEB-DL.H265-JKCT.mkv", []string{"grp:jkct"}},
		{"Jirisan.S01.2021.1080p.TVING.WEB-DL-unco@AvistaZ", []string{"grp:unco", "plat:tving"}},
		{"[SubsPlease] Call of the Night - 01 (1080p) [8E3F476A].mkv", []string{"tag:subsplease"}},
		// A folder whose last dotted piece is the release group, not an extension.
		{"Nirvana.in.Fire.S01.1080p-JKCT", []string{"grp:jkct"}},
		// Script says who released it, not what it is about.
		{"狂飙.The.Knockout.2023", []string{"script:han"}},
		// Packaging is never a marker.
		{"Some.Film.1999.1080p.BluRay.x264.mkv", nil},
	}
	for _, tt := range tests {
		got := Markers(tt.name)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Markers(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
