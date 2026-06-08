package db

import (
	"context"
	"testing"
)

func TestPathUsesCacheDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if p == "" {
		t.Fatal("empty cache path")
	}
}

func TestOpenAndBuild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	pool, err := Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer pool.Close()
	if err := Build(context.Background(), pool); err != nil {
		t.Fatalf("build: %v", err)
	}
}
