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
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Parent     string  `json:"parent"`
	Leaf       string  `json:"leaf"`
	Alias      string  `json:"alias"`
	OtherMedia bool    `json:"otherMedia"`
	Position   int     `json:"position"`
	Empty      bool    `json:"empty"`
	Markers    Markers `json:"markers"`
}

// The kinds of media a category takes. A category that states neither reads as both, so a
// library that never touches markers behaves exactly as before.
const (
	KindBoth  = "both"
	KindFilms = "films"
	KindShows = "shows"
)

// MaxLearned caps how many learned markers one category keeps. config.json stays small
// and hand-editable; the weakest evidence is dropped first, so what survives is what the
// category has actually been fed repeatedly.
const MaxLearned = 50

// Markers describe what belongs in a category. Kind, Languages, Countries and Keywords are
// declared by the admin; Learned is written by imports, mapping a namespaced marker
// ("grp:JKCT", "tag:LostYears", "plat:IQIYI", "script:han") to how often media carrying it
// landed here.
type Markers struct {
	Kind      string         `json:"kind,omitempty"`
	Languages []string       `json:"languages,omitempty"`
	Countries []string       `json:"countries,omitempty"`
	Keywords  []string       `json:"keywords,omitempty"`
	Learned   map[string]int `json:"learned,omitempty"`
}

// Accepts reports whether a category takes media of the given verdict. An unstated (or
// unknown) kind takes everything.
func (m Markers) Accepts(isShow bool) bool {
	switch m.Kind {
	case KindFilms:
		return !isShow
	case KindShows:
		return isShow
	default:
		return true
	}
}

// Empty reports whether a category declares and has learned nothing, in which case its
// config.json carries no markers section at all.
func (m Markers) Empty() bool {
	return m.Kind == "" && len(m.Languages) == 0 && len(m.Countries) == 0 &&
		len(m.Keywords) == 0 && len(m.Learned) == 0
}

// clean normalises admin input: blank entries dropped, an unknown kind reset to unstated,
// zero or negative learned counts dropped, and the learned map capped.
func (m Markers) clean() Markers {
	if m.Kind != KindFilms && m.Kind != KindShows {
		m.Kind = ""
	}
	m.Languages = cleanList(m.Languages)
	m.Countries = cleanList(m.Countries)
	m.Keywords = cleanList(m.Keywords)
	learned := map[string]int{}
	for k, n := range m.Learned {
		if k = strings.TrimSpace(k); k != "" && n > 0 {
			learned[k] = n
		}
	}
	m.Learned = capLearned(learned)
	if len(m.Learned) == 0 {
		m.Learned = nil
	}
	return m
}

func cleanList(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

// capLearned keeps the MaxLearned strongest markers, dropping the lowest counts first (a
// tie broken by name so the outcome never depends on map order).
func capLearned(learned map[string]int) map[string]int {
	if len(learned) <= MaxLearned {
		return learned
	}
	keys := make([]string, 0, len(learned))
	for k := range learned {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if learned[keys[i]] != learned[keys[j]] {
			return learned[keys[i]] > learned[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make(map[string]int, MaxLearned)
	for _, k := range keys[:MaxLearned] {
		out[k] = learned[k]
	}
	return out
}

// categoryConfig is the on-disk config.json inside a category folder - the authoritative
// record of a category's id, alias, (top-level only) other-media flag, sort position among
// its siblings, and the markers describing what belongs in it. Markers is a pointer so a
// category with none writes no key at all.
type categoryConfig struct {
	ID         int64    `json:"id"`
	Alias      string   `json:"alias"`
	OtherMedia bool     `json:"otherMedia"`
	Position   int      `json:"position"`
	Markers    *Markers `json:"markers,omitempty"`
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

// List returns every category under dataDir at any depth, ordered so siblings (same parent)
// run by their stored position, falling back to leaf name when positions tie (e.g. legacy
// configs with no position). A folder is a category exactly when it contains config.json;
// the walk descends only into categories, so media folders (and any stray folders) are
// never treated as categories.
func List(dataDir string) ([]Category, error) {
	cats := []Category{}
	if err := walkCategories(dataDir, "", &cats); err != nil {
		return nil, err
	}
	sort.Slice(cats, func(i, j int) bool {
		a, b := cats[i], cats[j]
		if a.Parent != b.Parent {
			return a.Parent < b.Parent
		}
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		return a.Leaf < b.Leaf
	})
	return cats, nil
}

// NextPosition returns the position a new category should take to sort last among the
// siblings under parentRel (a parent relpath, "" for top level): one past the highest
// existing sibling position, or 0 when the group is empty.
func NextPosition(dataDir, parentRel string) (int, error) {
	cats, err := List(dataDir)
	if err != nil {
		return 0, err
	}
	next := 0
	for _, c := range cats {
		if c.Parent == parentRel && c.Position >= next {
			next = c.Position + 1
		}
	}
	return next, nil
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
		c := readConfig(childDir, e.Name())
		*cats = append(*cats, Category{
			ID: c.ID, Name: rel, Parent: parentRel, Leaf: e.Name(),
			Alias: c.Alias, OtherMedia: c.OtherMedia, Position: c.Position, Empty: empty,
			Markers: markersOf(c),
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
// config.json with the given id and sibling position. parentRel is "" for a top-level
// category; a non-empty parentRel must already be a category. A blank alias defaults to the
// leaf name. New categories are never other-media at creation (the flag is toggled
// afterward, and only on a top-level category). It fails if the folder already exists.
func Create(dataDir, parentRel, leaf, alias string, id int64, position int) (Category, error) {
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
	if err := writeConfig(dir, categoryConfig{ID: id, Alias: alias, Position: position}); err != nil {
		return Category{}, err
	}
	return Category{ID: id, Name: name, Parent: parentRel, Leaf: leaf, Alias: alias, Position: position, Empty: true}, nil
}

// SetAlias rewrites a category's config.json with a new alias and other-media flag,
// preserving its id and sibling position. relpath addresses the category. A blank alias
// falls back to the leaf name. The caller is responsible for forcing otherMedia false on a
// sub-category (the flag is inherited from the root, never stored below it).
func SetAlias(dataDir, relpath, alias string, otherMedia bool) error {
	if !Exists(dataDir, relpath) {
		return fmt.Errorf("no category named %q", relpath)
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		alias = filepath.Base(relpath)
	}
	dir := filepath.Join(dataDir, relpath)
	c := readConfig(dir, filepath.Base(relpath))
	c.Alias, c.OtherMedia = alias, otherMedia
	return writeConfig(dir, c)
}

// SetMarkers rewrites a category's markers, preserving everything else in its config.json.
// The markers are normalised on the way in (blank entries dropped, an unknown kind reset,
// the learned map capped), so a hand-edited or hostile payload cannot corrupt the file.
func SetMarkers(dataDir, relpath string, m Markers) error {
	if !Exists(dataDir, relpath) {
		return fmt.Errorf("no category named %q", relpath)
	}
	dir := filepath.Join(dataDir, relpath)
	c := readConfig(dir, filepath.Base(relpath))
	m = m.clean()
	c.Markers = nil
	if !m.Empty() {
		c.Markers = &m
	}
	return writeConfig(dir, c)
}

// Learn records that media carrying these markers was imported into a category: each
// marker's count rises by one and the map is capped again. It is the learning half of the
// same mechanism SetMarkers writes the declared half of.
func Learn(dataDir, relpath string, markers []string) error {
	if len(markers) == 0 {
		return nil
	}
	if !Exists(dataDir, relpath) {
		return fmt.Errorf("no category named %q", relpath)
	}
	dir := filepath.Join(dataDir, relpath)
	c := readConfig(dir, filepath.Base(relpath))
	m := markersOf(c)
	if m.Learned == nil {
		m.Learned = map[string]int{}
	}
	for _, k := range markers {
		if k = strings.TrimSpace(k); k != "" {
			m.Learned[k]++
		}
	}
	m.Learned = capLearned(m.Learned)
	c.Markers = &m
	return writeConfig(dir, c)
}

// markersOf reads a config's markers, yielding the zero value when it carries none.
func markersOf(c categoryConfig) Markers {
	if c.Markers == nil {
		return Markers{}
	}
	return *c.Markers
}

// SetPosition rewrites a category's config.json with a new sibling position, preserving its
// id, alias, and other-media flag. relpath addresses the category. The caller is responsible
// for keeping positions consistent within a sibling group (it renumbers the whole group).
func SetPosition(dataDir, relpath string, position int) error {
	if !Exists(dataDir, relpath) {
		return fmt.Errorf("no category named %q", relpath)
	}
	dir := filepath.Join(dataDir, relpath)
	c := readConfig(dir, filepath.Base(relpath))
	c.Position = position
	return writeConfig(dir, c)
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

// readConfig returns the folder's config.json. The alias falls back to leaf when the file is
// missing, unreadable, or has a blank alias; a missing id reads as 0, a missing flag as
// false, a missing position as 0, and missing markers as none.
func readConfig(dir, leaf string) categoryConfig {
	data, err := os.ReadFile(filepath.Join(dir, configName))
	if err != nil {
		return categoryConfig{Alias: leaf}
	}
	var c categoryConfig
	if json.Unmarshal(data, &c) != nil {
		return categoryConfig{Alias: leaf}
	}
	if strings.TrimSpace(c.Alias) == "" {
		c.Alias = leaf
	}
	return c
}

func writeConfig(dir string, c categoryConfig) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, configName), data, 0o644)
}
