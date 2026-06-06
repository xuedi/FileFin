package state

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	in := map[string]UserState{
		"alice": {Progress: &Pointer{File: "2x4", Seconds: 843}, Watched: true, Extra: map[string]string{}},
		"bob":   {Progress: &Pointer{File: "#3", Seconds: 120}, Extra: map[string]string{"rating": "5"}},
		"cara":  {Progress: &Pointer{File: "", Seconds: 12}, Extra: map[string]string{}},
	}
	got := Parse(Serialize(in))
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", got, in)
	}
}

func TestSerializeStable(t *testing.T) {
	m := map[string]UserState{
		"bob":   {Watched: true, Extra: map[string]string{}},
		"alice": {Progress: &Pointer{File: "1x1", Seconds: 5}, Extra: map[string]string{"zeta": "9", "favorite": "true"}},
	}
	want := "## alice\n- progress: 1x1 @ 5s\n- favorite: true\n- zeta: 9\n\n## bob\n- watched: true\n"
	if got := Serialize(m); got != want {
		t.Fatalf("serialize:\n got %q\nwant %q", got, want)
	}
}

func TestFavoriteRoundTrip(t *testing.T) {
	in := map[string]UserState{
		"alice": {
			Progress: &Pointer{File: "1x1", Seconds: 5},
			Watched:  true,
			Favorite: true,
			Extra:    map[string]string{"rating": "9"},
		},
	}
	got := Parse(Serialize(in))
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("favorite round trip:\n got %#v\nwant %#v", got, in)
	}
	// Known-key order: progress, watched, favorite, then sorted extra.
	want := "## alice\n- progress: 1x1 @ 5s\n- watched: true\n- favorite: true\n- rating: 9\n"
	if s := Serialize(in); s != want {
		t.Fatalf("serialize order:\n got %q\nwant %q", s, want)
	}
}

func TestFavoriteAbsentIsFalse(t *testing.T) {
	m := Parse("## bob\n- progress: 10s\n")
	if m["bob"].Favorite {
		t.Fatal("favorite should default to false")
	}
	if s := Serialize(m); s != "## bob\n- progress: 10s\n" {
		t.Fatalf("a non-favorite must not emit the bullet: %q", s)
	}
}

func TestSingleFileProgress(t *testing.T) {
	m := map[string]UserState{"alice": {Progress: &Pointer{File: "", Seconds: 843}, Extra: map[string]string{}}}
	if got := Serialize(m); got != "## alice\n- progress: 843s\n" {
		t.Fatalf("single-file progress: got %q", got)
	}
	back := Parse("## alice\n- progress: 843s\n")
	if p := back["alice"].Progress; p == nil || p.File != "" || p.Seconds != 843 {
		t.Fatalf("parse single-file progress: %#v", back["alice"].Progress)
	}
}

func TestParseForgiving(t *testing.T) {
	m := Parse("garbage line\n## alice\n- progress: not-a-number\n- watched: yes\n- ok: v\n")
	us := m["alice"]
	if us.Progress != nil {
		t.Errorf("bad progress should be nil, got %#v", us.Progress)
	}
	if us.Watched {
		t.Errorf("watched should be false for non-true value")
	}
	if us.Extra["ok"] != "v" {
		t.Errorf("unknown key not preserved: %#v", us.Extra)
	}
}

func TestLoadMissing(t *testing.T) {
	m, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Fatalf("missing file should yield empty map, got %#v", m)
	}
}

func TestManagerUpdatePreservesOthers(t *testing.T) {
	dir := t.TempDir()
	seed := "## bob\n- progress: #2 @ 50s\n- rating: 9\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager()
	err := mgr.Update(dir, "alice", func(s UserState) UserState {
		s.Progress = &Pointer{File: "1x1", Seconds: 10}
		return s
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p := got["bob"].Progress; p == nil || p.File != "#2" || p.Seconds != 50 {
		t.Errorf("bob's progress clobbered: %#v", got["bob"].Progress)
	}
	if got["bob"].Extra["rating"] != "9" {
		t.Errorf("bob's unknown key lost: %#v", got["bob"].Extra)
	}
	if p := got["alice"].Progress; p == nil || p.File != "1x1" || p.Seconds != 10 {
		t.Errorf("alice's progress not written: %#v", got["alice"].Progress)
	}
}
