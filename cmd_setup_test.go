package main

import (
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"filefin/internal/config"
)

func TestPerformSetup(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".filefin.md")
	dataDir := filepath.Join(dir, "media")

	if err := performSetup(cfgPath, dataDir, "alice", "secret"); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != dataDir {
		t.Fatalf("data dir: %q", cfg.DataDir)
	}
	hash, ok := cfg.Users["alice"]
	if !ok {
		t.Fatal("user not stored")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("secret")) != nil {
		t.Fatal("stored hash does not verify against password")
	}
}

func TestPerformSetupRejectsEmpty(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), ".filefin.md")
	if err := performSetup(cfgPath, t.TempDir(), "", "secret"); err == nil {
		t.Fatal("expected error for empty username")
	}
}
