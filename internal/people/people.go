// Package people owns the shared per-person store in the data dir: one folder per TMDb
// person id under <dataDir>/.people, holding person.json and the person's photo. Keying by
// id rather than by name keeps two actors who share a name apart, and a renamed actor in
// place. The store lives in the data dir, not the cache, so a rebuild or a move to a new
// machine keeps every photo.
package people

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"filefin/internal/fsutil"
	"filefin/internal/thumbnail"
)

// DirName is the store's folder at the data dir root. It starts with a dot so every walk
// of the data dir that looks for categories passes it by.
const DirName = ".people"

const (
	recordName = "person.json"
	photoWebP  = "profile.webp"
	photoJPEG  = "profile.jpg"
)

// Record is one person's person.json.
type Record struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Department  string `json:"department,omitempty"`
	Birthday    string `json:"birthday,omitempty"`
	Deathday    string `json:"deathday,omitempty"`
	ProfilePath string `json:"profilePath,omitempty"`
	Fetched     int64  `json:"fetched"`
}

// Store reads and writes the per-person folders under one data dir.
type Store struct{ root string }

// New returns the store for a data dir.
func New(dataDir string) Store { return Store{root: filepath.Join(dataDir, DirName)} }

func (s Store) dir(id int) string { return filepath.Join(s.root, strconv.Itoa(id)) }

// Has reports whether a person is already stored. A stored person is never fetched again.
func (s Store) Has(id int) bool {
	_, err := os.Stat(filepath.Join(s.dir(id), recordName))
	return err == nil
}

// Read returns a stored person's record.
func (s Store) Read(id int) (Record, error) {
	var r Record
	data, err := os.ReadFile(filepath.Join(s.dir(id), recordName))
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(data, &r)
}

// Write stores a person's record atomically.
func (s Store) Write(r Record) error {
	if r.ID <= 0 {
		return errors.New("people: record without an id")
	}
	if err := os.MkdirAll(s.dir(r.ID), 0o755); err != nil {
		return fmt.Errorf("people: create %d: %w", r.ID, err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("people: marshal %d: %w", r.ID, err)
	}
	return fsutil.WriteFileAtomic(filepath.Join(s.dir(r.ID), recordName), data, 0o644)
}

// SavePhoto stores a downloaded photo as a small WebP. When ffmpeg cannot encode it, the
// original JPEG is kept instead, so a missing encoder costs size, not the photo.
func (s Store) SavePhoto(ctx context.Context, ffmpeg string, id int, jpeg []byte) error {
	dir := s.dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("people: create %d: %w", id, err)
	}
	src := filepath.Join(dir, photoJPEG)
	if err := fsutil.WriteFileAtomic(src, jpeg, 0o644); err != nil {
		return fmt.Errorf("people: write photo %d: %w", id, err)
	}
	if err := thumbnail.Profile(ctx, ffmpeg, src, filepath.Join(dir, photoWebP)); err != nil {
		return nil
	}
	_ = os.Remove(src)
	return nil
}

// PhotoPath returns the stored photo for a person, or "" when there is none.
func (s Store) PhotoPath(id int) string {
	for _, name := range []string{photoWebP, photoJPEG} {
		p := filepath.Join(s.dir(id), name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Count returns how many people are stored.
func (s Store) Count() int {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			if _, err := strconv.Atoi(e.Name()); err == nil {
				n++
			}
		}
	}
	return n
}

// NameKey folds a person's name into a key two spellings of the same name share: letters
// lowercased and stripped of accents, hyphens and apostrophes dropped, and the tokens put in
// order. So "Xiwei Tian" and "Tian Xiwei" (given and family name swapped between sources),
// "Marie Laforêt" and "Marie Laforet", or "Choi Ji-woo" and "Choi Jiwoo" all agree.
func NameKey(name string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(name) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r == '-' || r == '\'' || r == '’' || r == '.':
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(' ')
		}
	}
	tokens := strings.Fields(b.String())
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}
