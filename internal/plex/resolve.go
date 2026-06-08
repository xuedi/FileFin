package plex

import (
	"os"
	"path/filepath"
	"strings"
)

// Path resolution statuses for one library.
const (
	PathGreen      = "green"      // a remap (or identity) resolves a clear majority of probes
	PathNeedsInput = "needsInput" // some probes resolve, but not a majority - ask for a media location
	PathUnresolved = "unresolved" // nothing beyond the basename matches - manual map required
)

// Remap is a from->to prefix substitution applied to a DB-recorded path so it
// points at where the file actually lives now. An empty From is the identity
// remap (paths are already correct).
type Remap struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Apply rewrites path's From prefix to To. A path without the From prefix, or the
// identity remap, is returned unchanged. From and To carry trailing separators so
// the join stays a clean prefix swap.
func (r Remap) Apply(path string) string {
	if r.From == "" || !strings.HasPrefix(path, r.From) {
		return path
	}
	return r.To + strings.TrimPrefix(path, r.From)
}

// RemapResult is the outcome of resolving one library's probe set.
type RemapResult struct {
	Remap  Remap
	Status string
	Found  int // probes the chosen remap cleanly resolves
	Total  int // probes considered
}

// Resolve picks a remap that makes the probe paths resolve to real files. With no
// searchBase it only checks the paths as-is (so a co-located install goes green
// with zero input). Otherwise it derives a from->to prefix swap by chopping
// leading components off a missing probe and re-joining the suffix onto searchBase
// (longest suffix first), then verifies that remap across the whole probe set with
// an ambiguity guard, accepting only a clear majority of clean, unambiguous hits.
func Resolve(samples []string, searchBase string) RemapResult {
	total := len(samples)
	if total == 0 {
		return RemapResult{Status: PathUnresolved}
	}

	// As-is: the paths may already be correct (a co-located library).
	if asis := countExisting(samples); isMajority(asis, total) {
		return RemapResult{Remap: Remap{}, Status: PathGreen, Found: asis, Total: total}
	} else if searchBase == "" {
		// Without a search base only the as-is check is possible.
		return RemapResult{Status: PathNeedsInput, Found: asis, Total: total}
	}

	base := strings.TrimRight(searchBase, string(filepath.Separator))
	best := RemapResult{Status: PathUnresolved, Total: total}
	for _, seed := range samples {
		if pathExists(seed) {
			continue // derive only from a probe that is actually missing as-is
		}
		// Candidates are sorted longest-suffix first, so the first that reaches a
		// majority is the longest verifying remap for this seed.
		for _, cand := range deriveCandidates(seed, base) {
			found := verify(samples, cand, base)
			if isMajority(found, total) {
				return RemapResult{Remap: cand, Status: PathGreen, Found: found, Total: total}
			}
			if found > best.Found {
				best = RemapResult{Remap: cand, Status: PathNeedsInput, Found: found, Total: total}
			}
		}
	}
	if best.Found == 0 {
		best.Status = PathUnresolved
	}
	return best
}

// deriveCandidates builds remap candidates from one seed path by joining each of
// its suffixes (longest first) onto base; a join that exists yields a concrete
// from->to. The basename-only suffix is excluded: a single component matches too
// loosely to auto-accept.
func deriveCandidates(seed, base string) []Remap {
	comps := splitPath(seed)
	var out []Remap
	for k := len(comps) - 1; k >= 2; k-- {
		suffix := filepath.Join(comps[len(comps)-k:]...)
		if pathExists(filepath.Join(base, suffix)) {
			from := strings.TrimSuffix(seed, suffix) // keeps the trailing separator
			out = append(out, Remap{From: from, To: base + string(filepath.Separator)})
		}
	}
	return out
}

// verify counts how many probes the remap resolves cleanly and unambiguously: the
// remapped path must exist, and the probe's suffix must match exactly one location
// under base (a suffix that also resolves at another depth is ambiguous and counts
// as a failure, not a hit).
func verify(samples []string, r Remap, base string) int {
	clean := 0
	for _, s := range samples {
		if r.From != "" && !strings.HasPrefix(s, r.From) {
			continue
		}
		if !pathExists(r.Apply(s)) {
			continue
		}
		if countLocations(s, base) == 1 {
			clean++
		}
	}
	return clean
}

// countLocations counts how many of a path's suffixes (basename and longer) exist
// when joined onto base. More than one means the tail is ambiguous under base.
func countLocations(sample, base string) int {
	comps := splitPath(sample)
	n := 0
	for k := len(comps) - 1; k >= 1; k-- {
		if pathExists(filepath.Join(base, filepath.Join(comps[len(comps)-k:]...))) {
			n++
		}
	}
	return n
}

// splitPath returns a path's components with leading/trailing separators dropped.
func splitPath(p string) []string {
	p = strings.Trim(p, string(filepath.Separator))
	if p == "" {
		return nil
	}
	return strings.Split(p, string(filepath.Separator))
}

func countExisting(paths []string) int {
	n := 0
	for _, p := range paths {
		if pathExists(p) {
			n++
		}
	}
	return n
}

// isMajority is the >=70% acceptance bar (>=7 of 10) used for both the as-is check
// and a derived remap.
func isMajority(found, total int) bool {
	return found > 0 && found*10 >= total*7
}

func pathExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}
