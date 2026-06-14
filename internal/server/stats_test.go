package server

import (
	"encoding/json"
	"testing"

	"filefin/internal/db"
)

func TestStatsEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, h, admin, bob := installedServer(t, t.TempDir())

	// Non-admin is forbidden.
	if rr := do(t, h, "GET", "/api/admin/stats", "", bob); rr.Code != 403 {
		t.Fatalf("non-admin stats: %d, want 403", rr.Code)
	}

	rr := do(t, h, "GET", "/api/admin/stats", "", admin)
	if rr.Code != 200 {
		t.Fatalf("stats: %d %s", rr.Code, rr.Body.String())
	}
	var got statsView
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	// An empty library reports zero coverage and empty distributions, not a null payload.
	if got.Coverage.Percent != 0 || got.Containers == nil {
		t.Fatalf("unexpected empty-library stats: %+v", got)
	}
}

func TestClassifyFile(t *testing.T) {
	cases := []struct {
		name      string
		f         db.MediaFile
		want      string
		needsCopy bool
	}{
		{"probed direct play mp4/h264/aac", db.MediaFile{Container: "mov,mp4,m4a,3gp,3g2,mj2", VideoCodec: "h264", AudioCodec: "aac"}, bucketDirectPlay, false},
		{"probed direct play webm/vp9/opus", db.MediaFile{Container: "matroska,webm", VideoCodec: "vp9", AudioCodec: "opus"}, bucketDirectPlay, false},
		{"remux h264/ac3 in mkv", db.MediaFile{Container: "matroska,webm", VideoCodec: "h264", AudioCodec: "aac"}, bucketRemux, false},
		{"needs copy hevc", db.MediaFile{Container: "matroska,webm", VideoCodec: "hevc", AudioCodec: "ac3"}, bucketNeedsOptimize, true},
		{"unprobed native ext", db.MediaFile{Ext: ".mp4"}, bucketDirectPlay, false},
		{"unprobed foreign ext", db.MediaFile{Ext: ".avi"}, bucketUnprobed, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bucket, needsCopy := classifyFile(c.f)
			if bucket != c.want || needsCopy != c.needsCopy {
				t.Fatalf("classifyFile = (%q, %v), want (%q, %v)", bucket, needsCopy, c.want, c.needsCopy)
			}
		})
	}
}

func TestContainerLabel(t *testing.T) {
	cases := map[string]string{
		"mov,mp4,m4a,3gp,3g2,mj2": "MP4",
		"matroska,webm":           "Matroska/WebM",
		"avi":                     "AVI",
		"mpegts":                  "MPEGTS",
		"":                        "Unprobed",
	}
	for in, want := range cases {
		if got := containerLabel(in); got != want {
			t.Errorf("containerLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCoveragePercent(t *testing.T) {
	cases := []struct {
		optimized, pending, want int
	}{
		{0, 0, 0},
		{3, 1, 75},
		{1, 0, 100},
		{0, 5, 0},
	}
	for _, c := range cases {
		if got := coveragePercent(c.optimized, c.pending); got != c.want {
			t.Errorf("coveragePercent(%d,%d) = %d, want %d", c.optimized, c.pending, got, c.want)
		}
	}
}
