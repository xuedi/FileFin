// Package library manages categories on the filesystem (the source of truth): a folder
// is a category exactly when it contains a config.json, holding its id and alias.
// Categories nest to any depth - a category's child folders that themselves contain a
// config.json are sub-categories; other video-bearing children are media folders. The
// database is only a cache built from these files.
package library

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const configName = "config.json"

// Category is one category folder. Name is its path relative to the data directory
// ("Movies", "Movies/Action"); Parent is the enclosing category's relpath ("" for a
// top-level category); Leaf is the bare folder name (the display/uniqueness key). Empty
// reports whether the folder holds nothing but its config.json (no media folders and no
// sub-categories), so the caller knows it is safe to delete. ID is the stable id stored
// in config.json. OtherMedia is the flag stored in this folder's config.json; it is
// authoritative only on a top-level category (sub-categories inherit the root's flag).
type Category struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Parent     string `json:"parent"`
	Leaf       string `json:"leaf"`
	Alias      string `json:"alias"`
	OtherMedia bool   `json:"otherMedia"`
	Empty      bool   `json:"empty"`
}

// categoryConfig is the on-disk config.json inside a category folder - the
// authoritative record of a category's id, alias, and (top-level only) other-media flag.
type categoryConfig struct {
	ID         int64  `json:"id"`
	Alias      string `json:"alias"`
	OtherMedia bool   `json:"otherMedia"`
}

// ValidName checks that name is usable as a single Linux directory name (one leaf, not a
// path). Parentage is passed separately.
func ValidName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("a folder name is required")
	case len(name) > 255:
		return fmt.Errorf("folder name must be 255 bytes or fewer")
	case name == "." || name == "..":
		return fmt.Errorf("folder name must not be %q", name)
	case strings.ContainsRune(name, '/'):
		return fmt.Errorf("folder name must not contain a slash")
	}
	for _, r := range name {
		if r == 0 || r < 0x20 {
			return fmt.Errorf("folder name must not contain control characters")
		}
	}
	return nil
}

// List returns every category under dataDir at any depth, sorted by relpath. A folder is
// a category exactly when it contains config.json; the walk descends only into
// categories, so media folders (and any stray folders) are never treated as categories.
func List(dataDir string) ([]Category, error) {
	cats := []Category{}
	if err := walkCategories(dataDir, "", &cats); err != nil {
		return nil, err
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i].Name < cats[j].Name })
	return cats, nil
}

// walkCategories appends every category folder directly under parentRel (relative to
// dataDir), recursing into each. parentRel is "" at the top level.
func walkCategories(dataDir, parentRel string, cats *[]Category) error {
	dir := dataDir
	if parentRel != "" {
		dir = filepath.Join(dataDir, parentRel)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		childDir := filepath.Join(dir, e.Name())
		if !hasConfig(childDir) {
			continue // a media folder or stray dir, not a category
		}
		rel := e.Name()
		if parentRel != "" {
			rel = filepath.Join(parentRel, e.Name())
		}
		empty, err := isEmpty(childDir)
		if err != nil {
			return err
		}
		id, alias, other := readConfig(childDir, e.Name())
		*cats = append(*cats, Category{
			ID: id, Name: rel, Parent: parentRel, Leaf: e.Name(),
			Alias: alias, OtherMedia: other, Empty: empty,
		})
		if err := walkCategories(dataDir, rel, cats); err != nil {
			return err
		}
	}
	return nil
}

// hasConfig reports whether dir is a category folder (contains config.json).
func hasConfig(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, configName))
	return err == nil && !fi.IsDir()
}

// Exists reports whether a category folder already exists at the given relpath under
// dataDir.
func Exists(dataDir, relpath string) bool {
	return hasConfig(filepath.Join(dataDir, relpath))
}

// Create makes a new category folder <parentRel>/<leaf> under dataDir and writes its
// config.json with the given id. parentRel is "" for a top-level category; a non-empty
// parentRel must already be a category. A blank alias defaults to the leaf name. New
// categories are never other-media at creation (the flag is toggled afterward, and only
// on a top-level category). It fails if the folder already exists.
func Create(dataDir, parentRel, leaf, alias string, id int64) (Category, error) {
	if err := ValidName(leaf); err != nil {
		return Category{}, err
	}
	if parentRel != "" && !hasConfig(filepath.Join(dataDir, parentRel)) {
		return Category{}, fmt.Errorf("no parent category %q", parentRel)
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		alias = leaf
	}
	name := leaf
	if parentRel != "" {
		name = filepath.Join(parentRel, leaf)
	}
	dir := filepath.Join(dataDir, name)
	if _, err := os.Stat(dir); err == nil {
		return Category{}, fmt.Errorf("a category named %q already exists", name)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return Category{}, err
	}
	if err := writeConfig(dir, id, alias, false); err != nil {
		return Category{}, err
	}
	return Category{ID: id, Name: name, Parent: parentRel, Leaf: leaf, Alias: alias, Empty: true}, nil
}

// SetAlias rewrites a category's config.json with a new alias and other-media flag,
// preserving its id. relpath addresses the category. A blank alias falls back to the leaf
// name. The caller is responsible for forcing otherMedia false on a sub-category (the
// flag is inherited from the root, never stored below it).
func SetAlias(dataDir, relpath, alias string, otherMedia bool) error {
	if !Exists(dataDir, relpath) {
		return fmt.Errorf("no category named %q", relpath)
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		alias = filepath.Base(relpath)
	}
	dir := filepath.Join(dataDir, relpath)
	id, _, _ := readConfig(dir, filepath.Base(relpath))
	return writeConfig(dir, id, alias, otherMedia)
}

// Delete removes a category folder, but only when it is empty (no media folders and no
// sub-categories - nothing but its config.json), so existing media is never destroyed and
// a parent cannot be removed before its children.
func Delete(dataDir, relpath string) error {
	if err := ValidName(filepath.Base(relpath)); err != nil {
		return err
	}
	dir := filepath.Join(dataDir, relpath)
	if !hasConfig(dir) {
		return fmt.Errorf("no category named %q", relpath)
	}
	empty, err := isEmpty(dir)
	if err != nil {
		return err
	}
	if !empty {
		return fmt.Errorf("category %q is not empty", relpath)
	}
	// Empty means nothing but config.json; remove it first so the dir is removable.
	if err := os.Remove(filepath.Join(dir, configName)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Remove(dir)
}

// isEmpty reports whether dir contains no entries other than config.json.
func isEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() != configName {
			return false, nil
		}
	}
	return true, nil
}

// readConfig returns the id, alias, and other-media flag from the folder's config.json.
// The alias falls back to leaf when the file is missing, unreadable, or has a blank
// alias; a missing id reads as 0 and a missing flag as false.
func readConfig(dir, leaf string) (int64, string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, configName))
	if err != nil {
		return 0, leaf, false
	}
	var c categoryConfig
	if json.Unmarshal(data, &c) != nil {
		return 0, leaf, false
	}
	alias := c.Alias
	if strings.TrimSpace(alias) == "" {
		alias = leaf
	}
	return c.ID, alias, c.OtherMedia
}

func writeConfig(dir string, id int64, alias string, otherMedia bool) error {
	data, err := json.MarshalIndent(categoryConfig{ID: id, Alias: alias, OtherMedia: otherMedia}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, configName), data, 0o644)
}
