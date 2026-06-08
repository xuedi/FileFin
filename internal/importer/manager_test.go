package importer

import (
	"sync"
	"testing"

	"filefin/internal/state"
)

// TestMergeMetaPreservesState confirms an enrich-style merge carries base's state
// through unchanged and never picks state up from add.
func TestMergeMetaPreservesState(t *testing.T) {
	base := Meta{Title: "Alpha", Year: 2001, State: map[string]state.UserState{
		"alice": {Watched: true, Updated: 100},
	}}
	add := Meta{Title: "Alpha", Year: 2001, Description: "omdb plot", State: map[string]state.UserState{
		"mallory": {Favorite: true},
	}}
	got := MergeMeta(base, add)
	if got.Description != "omdb plot" {
		t.Fatalf("description gap not filled: %q", got.Description)
	}
	if _, ok := got.State["mallory"]; ok {
		t.Fatalf("add must not contribute state: %+v", got.State)
	}
	if us := got.State["alice"]; !us.Watched || us.Updated != 100 {
		t.Fatalf("base state not preserved: %+v", got.State)
	}
}

// TestMetaStateRoundTrip confirms the state object survives WriteMeta/ReadMeta.
func TestMetaStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := StubMeta("The Matrix", 1999)
	m.State = map[string]state.UserState{
		"alice": {Progress: &state.Pointer{File: "2x4", Seconds: 843}, Watched: true, Favorite: true, Updated: 42},
	}
	if err := WriteMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	us, ok := got.State["alice"]
	if !ok || us.Progress == nil || us.Progress.File != "2x4" || us.Progress.Seconds != 843 || !us.Watched || us.Updated != 42 {
		t.Fatalf("state round trip mismatch: %+v", got.State)
	}
}

// TestUpdateStateStampsAndPreserves confirms UpdateState bumps Updated, writes through,
// and leaves other users and the rich metadata intact.
func TestUpdateStateStampsAndPreserves(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager()
	if err := WriteMeta(dir, StubMeta("Alpha", 2001)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.UpdateState(dir, "bob", func(s state.UserState) state.UserState {
		s.Favorite = true
		return s
	}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.UpdateState(dir, "alice", func(s state.UserState) state.UserState {
		s.Progress = &state.Pointer{File: "1x1", Seconds: 10}
		return s
	}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Alpha" || got.Year != 2001 {
		t.Fatalf("rich metadata clobbered: %+v", got)
	}
	if !got.State["bob"].Favorite || got.State["bob"].Updated == 0 {
		t.Fatalf("bob's state/timestamp wrong: %+v", got.State["bob"])
	}
	if p := got.State["alice"].Progress; p == nil || p.File != "1x1" || got.State["alice"].Updated == 0 {
		t.Fatalf("alice's state/timestamp wrong: %+v", got.State["alice"])
	}
}

// TestUpdateConcurrentStateAndMerge hammers a folder with state writes while an
// enrich-style merge writes the same folder, and asserts neither the OMDb fields nor
// any user's state is ever lost.
func TestUpdateConcurrentStateAndMerge(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager()
	if err := WriteMeta(dir, StubMeta("Alpha", 2001)); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = mgr.UpdateState(dir, "alice", func(s state.UserState) state.UserState {
				s.Watched = true
				return s
			})
		}()
		go func() {
			defer wg.Done()
			_, _ = mgr.Update(dir, func(cur Meta) Meta {
				cur.Description = "omdb plot"
				cur.Metadata = mergeStringMap(cur.Metadata, map[string]string{"imdbID": "tt1"})
				cur.Enriched = true
				return cur
			})
		}()
	}
	wg.Wait()

	got, err := ReadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "omdb plot" || got.Metadata["imdbID"] != "tt1" || !got.Enriched {
		t.Fatalf("OMDb fields lost under concurrency: %+v", got)
	}
	if !got.State["alice"].Watched {
		t.Fatalf("user state lost under concurrency: %+v", got.State)
	}
}
