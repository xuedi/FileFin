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
	c.APIKeys["tmdb"] = "abc123"
	c.Users["alice"] = "hash1"
	c.Users["bob"] = "hash2"

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
	if got.APIKeys["tmdb"] != "abc123" {
		t.Fatalf("apikey mismatch: %v", got.APIKeys)
	}
	if got.Users["alice"] != "hash1" || got.Users["bob"] != "hash2" {
		t.Fatalf("users mismatch: %v", got.Users)
	}
}
