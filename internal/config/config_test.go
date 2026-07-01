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

func TestSetupComplete(t *testing.T) {
	var c Config
	if c.SetupComplete() {
		t.Fatal("empty config should not be setup-complete")
	}
	c.Users = map[string]User{}
	if c.SetupComplete() {
		t.Fatal("config with no users should not be setup-complete")
	}
	c.Users["admin"] = User{Admin: true}
	if !c.SetupComplete() {
		t.Fatal("config with a user should be setup-complete")
	}
}

func TestBind(t *testing.T) {
	if got := (&Config{Port: 8080}).Bind(); got != ":8080" {
		t.Fatalf("all-interfaces bind = %q, want :8080", got)
	}
	if got := (&Config{Port: 80, BindAddress: "127.0.0.1"}).Bind(); got != "127.0.0.1:80" {
		t.Fatalf("loopback bind = %q, want 127.0.0.1:80", got)
	}
}

func TestNewSetupToken(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := NewSetupToken()
		if err != nil {
			t.Fatal(err)
		}
		// 32 bytes -> 43 chars of unpadded base64url; enough entropy that a guess is infeasible.
		if len(tok) < 40 {
			t.Fatalf("token too short: %q (%d chars)", tok, len(tok))
		}
		if seen[tok] {
			t.Fatalf("duplicate token minted: %q", tok)
		}
		seen[tok] = true
	}
}

func TestClearSetupToken(t *testing.T) {
	c := &Config{SetupToken: "secret"}
	c.ClearSetupToken()
	if c.SetupToken != "" {
		t.Fatalf("token not cleared: %q", c.SetupToken)
	}
}

func TestCreatePendingConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg, err := Create(80, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 80 || cfg.BindAddress != "127.0.0.1" {
		t.Fatalf("unexpected port/bind: %+v", cfg)
	}
	if cfg.SetupToken == "" {
		t.Fatal("Create did not mint a setup token")
	}
	if cfg.SetupComplete() {
		t.Fatal("a freshly created config must be pending, not complete")
	}
	if !Exists() {
		t.Fatal("Create did not write the config file")
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.SetupToken != cfg.SetupToken || got.Port != 80 || got.BindAddress != "127.0.0.1" {
		t.Fatalf("loaded config differs from created: %+v vs %+v", got, cfg)
	}
	if got.SetupComplete() {
		t.Fatal("loaded pending config must not be complete")
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
