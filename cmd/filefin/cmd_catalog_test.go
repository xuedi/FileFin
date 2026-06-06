package main

import (
	"strings"
	"testing"

	"filefin/internal/transcode"
)

func TestAggregate(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"uniform collapses", []string{"h264", "h264", "h264"}, "h264"},
		{"distinct joined", []string{"h264", "hevc"}, "h264, hevc"},
		{"empties dropped", []string{"", "h264", "", "hevc"}, "h264, hevc"},
		{"order preserved dedupe", []string{"b", "a", "b", "a"}, "b, a"},
		{"all empty", []string{"", ""}, ""},
	}
	for _, c := range cases {
		if got := aggregate(c.in); got != c.want {
			t.Errorf("%s: aggregate(%v) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestAggregateOverflow(t *testing.T) {
	var in []string
	for i := 0; i < 13; i++ {
		in = append(in, string(rune('a'+i)))
	}
	got := aggregate(in)
	if !strings.HasSuffix(got, ", 3 more") {
		t.Fatalf("overflow suffix wrong: %q", got)
	}
	// First 10 distinct values, then the overflow note.
	if strings.Count(got, ", ") != 10 { // 9 separators between 10 values + 1 before "3 more"
		t.Fatalf("expected 10 listed values before overflow: %q", got)
	}
}

func TestBuildTechnicalFileSizeSummed(t *testing.T) {
	infos := []transcode.MediaInfo{
		{Container: "matroska", VideoCodec: "h264", Width: 1920, Height: 1080, Size: 1000, BitRate: 0},
		{Container: "matroska", VideoCodec: "hevc", Width: 1920, Height: 1080, Size: 2000, BitRate: 0},
	}
	kvs := buildTechnical(infos)
	got := map[string]string{}
	for _, kv := range kvs {
		got[kv.Key] = kv.Value
	}
	if got["videoCodec"] != "h264, hevc" {
		t.Errorf("videoCodec = %q, want %q", got["videoCodec"], "h264, hevc")
	}
	if got["resolution"] != "1920x1080" {
		t.Errorf("resolution = %q, want collapsed single value", got["resolution"])
	}
	// fileSize is the sum (3000 bytes), a single value, never a list.
	if got["fileSize"] != "2.9 KB" {
		t.Errorf("fileSize = %q, want summed single value", got["fileSize"])
	}
	if strings.Contains(got["fileSize"], ",") {
		t.Errorf("fileSize must not be a list: %q", got["fileSize"])
	}
}

func TestFormatFrameRate(t *testing.T) {
	cases := map[float64]string{
		25.0:         "25",
		23.976023976: "23.976",
		30.0:         "30",
	}
	for in, want := range cases {
		if got := formatFrameRate(in); got != want {
			t.Errorf("formatFrameRate(%v) = %q, want %q", in, got, want)
		}
	}
}
