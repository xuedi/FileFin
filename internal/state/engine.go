package state

import "fmt"

// WatchedThreshold is the fraction of a file that must be played for it to count as
// watched. Crossing it on the furthest file advances the resume pointer.
const WatchedThreshold = 0.90

// FileKey is the season/episode of a media file, used to build its on-disk ref.
type FileKey struct {
	Season  int
	Episode int
}

// Refs builds the on-disk file references for an ordered file list: "SxE" for a numbered
// episode, "" for a single-file folder, "#N" (1-based) otherwise.
func Refs(files []FileKey) []string {
	single := len(files) == 1
	out := make([]string, len(files))
	for i, f := range files {
		switch {
		case f.Season > 0 && f.Episode > 0:
			out[i] = fmt.Sprintf("%dx%d", f.Season, f.Episode)
		case single:
			out[i] = ""
		default:
			out[i] = fmt.Sprintf("#%d", i+1)
		}
	}
	return out
}

// indexOf returns the position of ref in refs, or -1 if absent.
func indexOf(refs []string, ref string) int {
	for i, r := range refs {
		if r == ref {
			return i
		}
	}
	return -1
}

func round(x float64) int {
	if x < 0 {
		return 0
	}
	return int(x + 0.5)
}

// Apply folds a playback report into a user's state. The resume pointer only ever moves
// forward (to a later file, or a later second within the furthest file); rewatching an
// earlier file leaves it untouched. Crossing WatchedThreshold on the furthest file
// advances the pointer to the next file at 0s, and when the last file crosses, the
// folder's permanent watched flag is set. refs is the ordered file list; fileIndex is the
// reported file; position and duration are in seconds.
func Apply(s UserState, refs []string, fileIndex int, position, duration float64) UserState {
	if fileIndex < 0 || fileIndex >= len(refs) {
		return s
	}
	if s.Extra == nil {
		s.Extra = map[string]string{}
	}
	last := len(refs) - 1
	crossed := duration > 0 && position/duration >= WatchedThreshold

	targetIdx := fileIndex
	targetSeconds := round(position)
	if crossed {
		if fileIndex == last {
			s.Watched = true
		} else {
			targetIdx = fileIndex + 1
			targetSeconds = 0
		}
	}

	curIdx := -1
	if s.Progress != nil {
		curIdx = indexOf(refs, s.Progress.File)
	}
	switch {
	case targetIdx > curIdx:
		s.Progress = &Pointer{File: refs[targetIdx], Seconds: targetSeconds}
	case targetIdx == curIdx && !crossed && targetSeconds > s.Progress.Seconds:
		s.Progress = &Pointer{File: refs[targetIdx], Seconds: targetSeconds}
	}
	return s
}

// WatchView is the derived watch state the detail view needs.
type WatchView struct {
	Watched         bool
	ContinueIndex   int
	ContinueSeconds int
	PerFile         []bool // per-file watched, aligned with refs
}

// View derives per-file and folder watch state for the detail view. ContinueIndex is the
// furthest reached file (0 with no progress); PerFile marks files before the pointer (or
// all files when the folder is watched) as watched.
func View(s UserState, refs []string) WatchView {
	v := WatchView{Watched: s.Watched, PerFile: make([]bool, len(refs))}
	ptr := -1
	if s.Progress != nil {
		ptr = indexOf(refs, s.Progress.File)
	}
	if ptr >= 0 {
		v.ContinueIndex = ptr
		v.ContinueSeconds = s.Progress.Seconds
	}
	for i := range v.PerFile {
		v.PerFile[i] = s.Watched || i < ptr
	}
	return v
}
