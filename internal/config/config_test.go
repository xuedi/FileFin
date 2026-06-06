package config

import (
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".filefin.md")
	c := New()
	c.DataDir = "/srv/media"
	c.CachePath = "/tmp/cache.sqlite"
	c.Port = 9000
	c.FFmpegPath = "/opt/ffmpeg"
	c.FFprobePath = "/opt/ffprobe"
	c.TranscodeEnabled = false
	c.OptimizeEnabled = true
	c.OptimizeMaxWorkers = 6
	c.OptimizeTargetLoad = 75
	c.LogLevel = "debug"
	c.LogOutput = "/var/log/filefin.log"
	c.APIKeys["tmdb"] = "abc123"
	c.Users["alice"] = User{Hash: "hash1", Admin: true}
	c.Users["bob"] = User{Hash: "hash2"}

	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DataDir != c.DataDir || got.CachePath != c.CachePath || got.Port != c.Port {
		t.Fatalf("scalar mismatch: %+v", got)
	}
	if got.FFmpegPath != "/opt/ffmpeg" || got.FFprobePath != "/opt/ffprobe" || got.TranscodeEnabled {
		t.Fatalf("transcode config mismatch: %+v", got)
	}
	if d := New(); d.FFmpegPath != "ffmpeg" || d.FFprobePath != "ffprobe" || !d.TranscodeEnabled {
		t.Fatalf("transcode defaults wrong: %+v", d)
	}
	if !got.OptimizeEnabled || got.OptimizeMaxWorkers != 6 || got.OptimizeTargetLoad != 75 {
		t.Fatalf("optimize config mismatch: %+v", got)
	}
	if d := New(); d.OptimizeEnabled || d.OptimizeMaxWorkers != 0 || d.OptimizeTargetLoad != 0 {
		t.Fatalf("optimize defaults wrong: %+v", d)
	}
	if got.LogLevel != "debug" || got.LogOutput != "/var/log/filefin.log" {
		t.Fatalf("logging config mismatch: %+v", got)
	}
	if d := New(); d.LogLevel != "info" || d.LogOutput != "STDOUT" {
		t.Fatalf("logging defaults wrong: %+v", d)
	}
	if got.APIKeys["tmdb"] != "abc123" {
		t.Fatalf("apikey mismatch: %v", got.APIKeys)
	}
	if got.Users["alice"] != (User{Hash: "hash1", Admin: true}) || got.Users["bob"] != (User{Hash: "hash2"}) {
		t.Fatalf("users mismatch: %v", got.Users)
	}
}

func TestParseUser(t *testing.T) {
	cases := map[string]User{
		"abc":              {Hash: "abc"},
		"abc (admin)":      {Hash: "abc", Admin: true},
		"abc   (admin)":    {Hash: "abc", Admin: true},
		"abc (Admin)":      {Hash: "abc", Admin: true},
		"$2a$10$x/y (foo)": {Hash: "$2a$10$x/y (foo)"}, // unrelated trailing parens stay in the hash
	}
	for in, want := range cases {
		if got := parseUser(in); got != want {
			t.Errorf("parseUser(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestAdminRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".filefin.md")
	c := New()
	c.Users["root"] = User{Hash: "h", Admin: true}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Users["root"].Admin || got.Users["root"].Hash != "h" {
		t.Fatalf("admin marker not round-tripped: %+v", got.Users["root"])
	}
}
