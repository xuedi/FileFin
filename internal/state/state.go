// Package state reads and writes the per-user state.md sidecar stored in each media
// folder. It records per-user playback state (a resume pointer and a permanent
// "watched" flag) on disk so it survives a cache rebuild: the filesystem stays the
// single source of truth.
//
// state.md is a deliberate, documented exception to the read-only-data-dir rule: it is
// the only per-user writer into a media folder, alongside setup, the importers, and the
// optimizer. The neutral name leaves room for future per-user fields (rating, favorite);
// this package handles only the watch fields today.
//
// Grammar (forgiving on read, stable on write):
//
//	## <username>
//	- progress: <ref> @ <seconds>s   (ref omitted for a single-file folder: "- progress: 843s")
//	- watched: true                  (only written when true; permanent once set)
//	- <unknown-key>: <value>         (preserved verbatim on rewrite)
//
// <ref> is "SxE" for a numbered episode, "#N" (1-based) for a non-numbered multi-file
// folder, and empty for a single-file folder. Unknown keys and unknown users are
// preserved so future fields and other accounts survive a rewrite by this version.
package state

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// FileName is the per-folder per-user state sidecar.
const FileName = "state.md"

// Pointer is a resume position: a file reference and a whole-second offset into it.
type Pointer struct {
	File    string // "SxE", "#N", or "" for a single-file folder
	Seconds int
}

// UserState is one user's state for a media folder. Extra holds unknown bullets verbatim
// so a rewrite by this version does not drop fields written by a newer one.
type UserState struct {
	Progress *Pointer
	Watched  bool
	Favorite bool
	Extra    map[string]string
}

// Parse reads state.md content into a per-user map. Forgiving: malformed bullets are
// skipped, unknown keys are preserved in Extra.
func Parse(s string) map[string]UserState {
	out := map[string]UserState{}
	user := ""
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "## ") {
			user = strings.TrimSpace(line[3:])
			if user != "" {
				if _, ok := out[user]; !ok {
					out[user] = UserState{Extra: map[string]string{}}
				}
			}
			continue
		}
		if user == "" || !strings.HasPrefix(line, "-") {
			continue
		}
		k, v := splitKV(strings.TrimSpace(line[1:]))
		if k == "" {
			continue
		}
		us := out[user]
		switch k {
		case "progress":
			us.Progress = parseProgress(v)
		case "watched":
			us.Watched = strings.EqualFold(v, "true")
		case "favorite":
			us.Favorite = strings.EqualFold(v, "true")
		default:
			us.Extra[k] = v
		}
		out[user] = us
	}
	return out
}

// Serialize renders the per-user map back to state.md content: users sorted, known keys
// first, then Extra sorted. The progress bullet is emitted only when set, watched only
// when true.
func Serialize(m map[string]UserState) string {
	users := make([]string, 0, len(m))
	for u := range m {
		users = append(users, u)
	}
	sort.Strings(users)
	var b strings.Builder
	for i, u := range users {
		if i > 0 {
			b.WriteByte('\n')
		}
		us := m[u]
		fmt.Fprintf(&b, "## %s\n", u)
		if us.Progress != nil {
			fmt.Fprintf(&b, "- progress: %s\n", formatProgress(us.Progress))
		}
		if us.Watched {
			b.WriteString("- watched: true\n")
		}
		if us.Favorite {
			b.WriteString("- favorite: true\n")
		}
		extra := make([]string, 0, len(us.Extra))
		for k := range us.Extra {
			extra = append(extra, k)
		}
		sort.Strings(extra)
		for _, k := range extra {
			fmt.Fprintf(&b, "- %s: %s\n", k, us.Extra[k])
		}
	}
	return b.String()
}

// Load reads the state.md in folder. A missing file yields an empty map and no error.
func Load(folder string) (map[string]UserState, error) {
	data, err := os.ReadFile(filepath.Join(folder, FileName))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]UserState{}, nil
		}
		return nil, err
	}
	return Parse(string(data)), nil
}

// Manager serializes concurrent state.md writes per folder so simultaneous progress
// events for the same folder do not clobber each other.
type Manager struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewManager constructs a Manager.
func NewManager() *Manager { return &Manager{locks: map[string]*sync.Mutex{}} }

func (m *Manager) lockFor(folder string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[folder]
	if !ok {
		l = &sync.Mutex{}
		m.locks[folder] = l
	}
	return l
}

// Update applies fn to user's current state in folder and writes the result atomically,
// under a per-folder lock. The whole read-modify-write is serialized so the furthest
// pointer never regresses under concurrent events.
func (m *Manager) Update(folder, user string, fn func(UserState) UserState) error {
	l := m.lockFor(folder)
	l.Lock()
	defer l.Unlock()

	all, err := Load(folder)
	if err != nil {
		return err
	}
	cur, ok := all[user]
	if !ok || cur.Extra == nil {
		cur.Extra = map[string]string{}
	}
	all[user] = fn(cur)
	return writeAtomic(folder, Serialize(all))
}

// writeAtomic writes content to folder/state.md via a temp file and rename.
func writeAtomic(folder, content string) error {
	dst := filepath.Join(folder, FileName)
	tmp, err := os.CreateTemp(folder, FileName+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func splitKV(s string) (string, string) {
	idx := strings.Index(s, ":")
	if idx < 0 {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:])
}

// parseProgress reads "<ref> @ <sec>s" or "<sec>s" (single-file). Returns nil on garbage.
func parseProgress(v string) *Pointer {
	v = strings.TrimSpace(v)
	ref, secPart := "", v
	if i := strings.Index(v, "@"); i >= 0 {
		ref = strings.TrimSpace(v[:i])
		secPart = strings.TrimSpace(v[i+1:])
	}
	secPart = strings.TrimSpace(strings.TrimSuffix(secPart, "s"))
	sec, err := strconv.Atoi(secPart)
	if err != nil || sec < 0 {
		return nil
	}
	return &Pointer{File: ref, Seconds: sec}
}

func formatProgress(p *Pointer) string {
	if p.File == "" {
		return fmt.Sprintf("%ds", p.Seconds)
	}
	return fmt.Sprintf("%s @ %ds", p.File, p.Seconds)
}
