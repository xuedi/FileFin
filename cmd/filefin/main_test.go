package main

import (
	"testing"

	"filefin/internal/config"
)

func TestDoSetupWritesPending(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dataDir := t.TempDir()
	cfg, err := doSetup(9000, "127.0.0.1", dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9000 || cfg.BindAddress != "127.0.0.1" || cfg.DataDir != dataDir {
		t.Fatalf("unexpected pending config: %+v", cfg)
	}
	if cfg.SetupToken == "" {
		t.Fatal("setup did not mint a token")
	}
	if cfg.SetupComplete() {
		t.Fatal("a fresh setup must be pending, not complete")
	}
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.SetupToken != cfg.SetupToken || got.Port != 9000 {
		t.Fatalf("config not persisted: %+v", got)
	}
}

func TestDoSetupDefaultsPort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := doSetup(0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != config.DefaultPort {
		t.Fatalf("port = %d, want default %d", cfg.Port, config.DefaultPort)
	}
}

func TestDoSetupRejectsRelativeData(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := doSetup(0, "", "relative/path"); err == nil {
		t.Fatal("expected an error for a relative --data path")
	}
	if config.Exists() {
		t.Fatal("no config should be written when --data is invalid")
	}
}

func TestDoSetupRefusesWhenComplete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := config.Save(&config.Config{Port: 8080, Users: map[string]config.User{"admin": {Hash: "x", Admin: true}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := doSetup(0, "", ""); err == nil {
		t.Fatal("setup should refuse once an admin account exists")
	}
}

func TestDoSetupRefreshesPending(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	first, err := config.Create(7000, "")
	if err != nil {
		t.Fatal(err)
	}
	// No overrides: keep the pending port, but mint a fresh token.
	second, err := doSetup(0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.Port != 7000 {
		t.Fatalf("refresh changed the port: %d, want 7000", second.Port)
	}
	if second.SetupToken == first.SetupToken {
		t.Fatal("refresh should mint a new token")
	}
}

func TestBootstrapServeCreatesWhenAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, created, err := bootstrapServe(8085, "")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("bootstrapServe should create a config when none exists")
	}
	if cfg.Port != 8085 || cfg.SetupComplete() {
		t.Fatalf("unexpected bootstrapped config: %+v", cfg)
	}
	if !config.Exists() {
		t.Fatal("bootstrapServe did not persist the config")
	}
}

func TestBootstrapServeNoopWhenPresent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := config.Create(8080, ""); err != nil {
		t.Fatal(err)
	}
	cfg, created, err := bootstrapServe(0, "")
	if err != nil {
		t.Fatal(err)
	}
	if created || cfg != nil {
		t.Fatalf("bootstrapServe should be a no-op when a config exists: created=%v cfg=%v", created, cfg)
	}
}
