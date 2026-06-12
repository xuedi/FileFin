package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidName(t *testing.T) {
	good := []string{"Movies", "TV Shows", "kids-stuff", "a.b"}
	for _, n := range good {
		if err := ValidName(n); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{"", ".", "..", "a/b", "with\x00nul", "ctrl\tchar"}
	for _, n := range bad {
		if err := ValidName(n); err == nil {
			t.Errorf("ValidName(%q) = nil, want error", n)
		}
	}
}

func TestCreateListRoundTrip(t *testing.T) {
	dir := t.TempDir()

	cat, err := Create(dir, "", "Movies", "Films", 1, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cat.Alias != "Films" || cat.ID != 1 || !cat.Empty {
		t.Fatalf("unexpected category: %+v", cat)
	}

	// Blank alias defaults to the folder name.
	if _, err := Create(dir, "", "Shows", "  ", 2, 1); err != nil {
		t.Fatalf("create shows: %v", err)
	}

	cats, err := List(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(cats) != 2 {
		t.Fatalf("got %d categories, want 2: %+v", len(cats), cats)
	}
	// Sorted by name: Movies, Shows. Ids are read back from config.json.
	if cats[0].Name != "Movies" || cats[0].Alias != "Films" || cats[0].ID != 1 {
		t.Errorf("cats[0] = %+v", cats[0])
	}
	if cats[1].Name != "Shows" || cats[1].Alias != "Shows" || cats[1].ID != 2 {
		t.Errorf("cats[1] = %+v, want alias defaulting to folder name", cats[1])
	}

	// SetAlias updates the alias and other-media flag but keeps the id.
	if err := SetAlias(dir, "Movies", "Cinema", true); err != nil {
		t.Fatalf("set alias: %v", err)
	}
	cats, _ = List(dir)
	if cats[0].Alias != "Cinema" || cats[0].ID != 1 || !cats[0].OtherMedia {
		t.Errorf("after SetAlias cats[0] = %+v", cats[0])
	}
}

func TestPositionOrderingAndReorder(t *testing.T) {
	dir := t.TempDir()
	// Append three siblings; NextPosition should hand out 0, 1, 2 in turn.
	for i, name := range []string{"Movies", "Shows", "Music"} {
		pos, err := NextPosition(dir, "")
		if err != nil {
			t.Fatalf("next position %s: %v", name, err)
		}
		if pos != i {
			t.Fatalf("NextPosition for %s = %d, want %d", name, pos, i)
		}
		if _, err := Create(dir, "", name, name, int64(i+1), pos); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	// List orders by position, not by name: creation order, not alphabetical.
	cats, _ := List(dir)
	if got := []string{cats[0].Leaf, cats[1].Leaf, cats[2].Leaf}; got[0] != "Movies" || got[1] != "Shows" || got[2] != "Music" {
		t.Fatalf("order by position = %v, want [Movies Shows Music]", got)
	}
	// Reorder to Music, Movies, Shows by renumbering each config.json.
	for pos, name := range []string{"Music", "Movies", "Shows"} {
		if err := SetPosition(dir, name, pos); err != nil {
			t.Fatalf("set position %s: %v", name, err)
		}
	}
	cats, _ = List(dir)
	if got := []string{cats[0].Leaf, cats[1].Leaf, cats[2].Leaf}; got[0] != "Music" || got[1] != "Movies" || got[2] != "Shows" {
		t.Fatalf("order after reorder = %v, want [Music Movies Shows]", got)
	}
	// SetPosition preserves id and alias.
	if cats[0].ID != 3 || cats[0].Alias != "Music" {
		t.Errorf("after reorder cats[0] = %+v, want id 3 alias Music", cats[0])
	}
}

func TestSubCategoryNesting(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "", "Movies", "Films", 1, 0); err != nil {
		t.Fatal(err)
	}
	sub, err := Create(dir, "Movies", "Action", "Action films", 2, 0)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	if sub.Name != "Movies/Action" || sub.Parent != "Movies" || sub.Leaf != "Action" {
		t.Fatalf("unexpected sub category: %+v", sub)
	}
	cats, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 2 {
		t.Fatalf("got %d categories, want 2: %+v", len(cats), cats)
	}
	// A parent with a sub-category is not empty (cannot be deleted before its children).
	if cats[0].Name != "Movies" || cats[0].Empty {
		t.Errorf("parent should be non-empty: %+v", cats[0])
	}
	if cats[1].Name != "Movies/Action" || cats[1].Parent != "Movies" {
		t.Errorf("sub category = %+v", cats[1])
	}
	if err := Delete(dir, "Movies"); err == nil {
		t.Fatal("expected error deleting a parent with a sub-category")
	}
	if err := Delete(dir, "Movies/Action"); err != nil {
		t.Fatalf("delete leaf sub-category: %v", err)
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "", "Movies", "", 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, "", "Movies", "", 1, 0); err == nil {
		t.Fatal("expected error creating duplicate category")
	}
}

func TestEmptyDetectionIgnoresConfig(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "", "Movies", "", 1, 0); err != nil {
		t.Fatal(err)
	}
	cats, _ := List(dir)
	if !cats[0].Empty {
		t.Fatal("category with only config.json should be empty")
	}

	// Add real content -> no longer empty.
	if err := os.WriteFile(filepath.Join(dir, "Movies", "film.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cats, _ = List(dir)
	if cats[0].Empty {
		t.Fatal("category with content should not be empty")
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "", "Movies", "", 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir, "Movies"); err != nil {
		t.Fatalf("delete empty: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Movies")); !os.IsNotExist(err) {
		t.Fatal("folder should be gone after delete")
	}

	// Non-empty folder must not be deletable.
	if _, err := Create(dir, "", "Shows", "", 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Shows", "ep.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir, "Shows"); err == nil {
		t.Fatal("expected error deleting non-empty category")
	}
	if _, err := os.Stat(filepath.Join(dir, "Shows")); err != nil {
		t.Fatal("non-empty folder should still exist after refused delete")
	}
}

func TestDeleteMissing(t *testing.T) {
	dir := t.TempDir()
	if err := Delete(dir, "Nope"); err == nil {
		t.Fatal("expected error deleting missing category")
	}
}
