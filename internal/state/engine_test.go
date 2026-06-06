package state

import (
	"reflect"
	"testing"
)

func TestRefs(t *testing.T) {
	if got := Refs([]FileKey{{}}); !reflect.DeepEqual(got, []string{""}) {
		t.Errorf("single file: %#v", got)
	}
	if got := Refs([]FileKey{{Season: 1, Episode: 1}, {Season: 2, Episode: 4}}); !reflect.DeepEqual(got, []string{"1x1", "2x4"}) {
		t.Errorf("episodes: %#v", got)
	}
	if got := Refs([]FileKey{{}, {}, {}}); !reflect.DeepEqual(got, []string{"#1", "#2", "#3"}) {
		t.Errorf("non-numbered multi: %#v", got)
	}
}

func TestApplyForwardSeconds(t *testing.T) {
	refs := []string{""}
	s := Apply(UserState{}, refs, 0, 100, 1000)
	if s.Progress.Seconds != 100 {
		t.Fatalf("want 100s, got %d", s.Progress.Seconds)
	}
	s = Apply(s, refs, 0, 250, 1000)
	if s.Progress.Seconds != 250 {
		t.Fatalf("want 250s, got %d", s.Progress.Seconds)
	}
	// A rewind report does not move the pointer back.
	s = Apply(s, refs, 0, 30, 1000)
	if s.Progress.Seconds != 250 {
		t.Fatalf("rewind moved pointer back to %d", s.Progress.Seconds)
	}
}

func TestApplyNinetyAdvances(t *testing.T) {
	refs := []string{"1x1", "1x2", "1x3"}
	s := Apply(UserState{}, refs, 0, 900, 1000) // 90% of file 0
	if s.Progress.File != "1x2" || s.Progress.Seconds != 0 {
		t.Fatalf("want advance to 1x2 @ 0s, got %#v", s.Progress)
	}
	if s.Watched {
		t.Fatal("folder should not be watched yet")
	}
}

func TestApplyLastFileSetsWatched(t *testing.T) {
	refs := []string{"1x1", "1x2"}
	s := Apply(UserState{Progress: &Pointer{File: "1x2", Seconds: 10}}, refs, 1, 950, 1000)
	if !s.Watched {
		t.Fatal("last file crossing 90% should set folder watched")
	}
}

func TestApplyJumpAheadMarksEarlierWatched(t *testing.T) {
	refs := []string{"1x1", "1x2", "1x3"}
	s := Apply(UserState{}, refs, 2, 40, 1000) // start watching file 2 directly
	v := View(s, refs)
	if v.ContinueIndex != 2 {
		t.Fatalf("continue index want 2, got %d", v.ContinueIndex)
	}
	if !v.PerFile[0] || !v.PerFile[1] {
		t.Fatalf("earlier files should be watched: %#v", v.PerFile)
	}
	if v.PerFile[2] {
		t.Fatal("current file should not be marked watched")
	}
}

func TestApplyRewatchDoesNotMoveBack(t *testing.T) {
	refs := []string{"1x1", "1x2", "1x3"}
	s := UserState{Progress: &Pointer{File: "1x3", Seconds: 20}}
	s = Apply(s, refs, 0, 500, 1000) // rewatch file 0
	if s.Progress.File != "1x3" || s.Progress.Seconds != 20 {
		t.Fatalf("rewatch moved pointer to %#v", s.Progress)
	}
}

func TestWatchedStaysSetAfterRewatch(t *testing.T) {
	refs := []string{"1x1", "1x2"}
	s := UserState{Watched: true, Progress: &Pointer{File: "1x2", Seconds: 900}}
	s = Apply(s, refs, 0, 10, 1000) // rewatch from the start
	if !s.Watched {
		t.Fatal("watched flag must stay set on rewatch")
	}
}

func TestViewSingleFilmResume(t *testing.T) {
	refs := []string{""}
	v := View(UserState{Progress: &Pointer{File: "", Seconds: 421}}, refs)
	if v.ContinueIndex != 0 || v.ContinueSeconds != 421 {
		t.Fatalf("single-film resume: %#v", v)
	}
}

func TestViewWatchedMarksAll(t *testing.T) {
	refs := []string{"1x1", "1x2"}
	v := View(UserState{Watched: true, Progress: &Pointer{File: "1x2", Seconds: 5}}, refs)
	if !v.PerFile[0] || !v.PerFile[1] {
		t.Fatalf("watched folder should mark all files: %#v", v.PerFile)
	}
}
