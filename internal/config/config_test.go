package config

import (
	"os"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if Exists() {
		t.Fatal("Exists should be false before any save")
	}
	in := &Config{Port: 9090, Users: map[string]User{"admin": {Hash: "h", Admin: true}},
		LogLevel: "debug", LogOutput: "/var/log/filefin.log"}
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	if !Exists() {
		t.Fatal("Exists should be true after save")
	}
	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if out.Port != 9090 || out.Users["admin"].Hash != "h" || !out.Users["admin"].Admin {
		t.Fatalf("round trip mismatch: %+v", out)
	}
	if out.LogLevel != "debug" || out.LogOutput != "/var/log/filefin.log" {
		t.Fatalf("logging fields lost in round trip: %+v", out)
	}
}

func TestSaveMode0600(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(&Config{Port: DefaultPort, Users: map[string]User{}}); err != nil {
		t.Fatal(err)
	}
	p, _ := Path()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestOptimizeModeDefault(t *testing.T) {
	c := &Config{}
	if c.OptimizeModeOr() != OptimizeNone {
		t.Errorf("optimize mode default = %q, want none", c.OptimizeModeOr())
	}
	c.OptimizeMode = OptimizeAll
	if c.OptimizeModeOr() != "all" {
		t.Errorf("optimize mode = %q, want all", c.OptimizeModeOr())
	}
	if !ValidOptimizeMode["cpu"] || !ValidOptimizeMode["gpu"] || ValidOptimizeMode["bogus"] {
		t.Error("ValidOptimizeMode set wrong")
	}
}

func TestOptimizeModeRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(&Config{Port: DefaultPort, Users: map[string]User{}, OptimizeMode: OptimizeGPU}); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if out.OptimizeMode != OptimizeGPU {
		t.Fatalf("optimize mode lost in round trip: %q", out.OptimizeMode)
	}
}

func TestTranscodeDefaults(t *testing.T) {
	c := &Config{}
	if !c.TranscodeOn() {
		t.Error("transcoding should default to on when unset")
	}
	if c.FFmpeg() != "ffmpeg" || c.FFprobe() != "ffprobe" {
		t.Errorf("ffmpeg/ffprobe defaults wrong: %q %q", c.FFmpeg(), c.FFprobe())
	}
	if c.SubLang() != "en" {
		t.Errorf("subtitle language default = %q, want en", c.SubLang())
	}
	off := false
	c.TranscodeEnabled = &off
	if c.TranscodeOn() {
		t.Error("transcoding should be off when explicitly disabled")
	}
}
