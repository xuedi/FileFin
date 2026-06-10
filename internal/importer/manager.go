package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
	"time"

	"filefin/internal/fsutil"
	"filefin/internal/state"
)

// Manager serializes meta.json writes per folder and preserves the sections each
// writer does not own. The importer, the OMDb enricher, and the playback-state writer
// all go through it, so a progress event can never drop the OMDb fields and an enrich
// can never drop anyone's state.
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

// Update loads folder's meta.json, applies fn, and writes the result atomically under
// the folder lock, returning the written Meta. A missing meta.json starts from an empty
// Meta. The whole read-modify-write is serialized so concurrent writers can never drop
// a section.
func (m *Manager) Update(folder string, fn func(Meta) Meta) (Meta, error) {
	l := m.lockFor(folder)
	l.Lock()
	defer l.Unlock()

	meta, err := ReadMeta(folder)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return Meta{}, err
		}
		meta = Meta{}
	}
	out := fn(meta)
	if err := writeMetaAtomic(folder, out); err != nil {
		return Meta{}, err
	}
	return out, nil
}

// UpdateState folds fn into one user's playback state inside meta.json, stamping the
// change time. The clock lives here so the pure engine stays clock-free.
func (m *Manager) UpdateState(folder, user string, fn func(state.UserState) state.UserState) error {
	_, err := m.Update(folder, func(meta Meta) Meta {
		if meta.State == nil {
			meta.State = map[string]state.UserState{}
		}
		us := fn(meta.State[user])
		us.Updated = time.Now().Unix()
		meta.State[user] = us
		return meta
	})
	return err
}

// LoadState returns a folder's per-user state from meta.json. A missing meta.json (or
// one with no state object) yields a nil map and no error.
func LoadState(folder string) (map[string]state.UserState, error) {
	meta, err := ReadMeta(folder)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return meta.State, nil
}

// writeMetaAtomic marshals meta to folder/meta.json via a temp file plus rename so a
// concurrent reader never observes a half-written file.
func writeMetaAtomic(folder string, meta Meta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	return fsutil.WriteFileAtomic(filepath.Join(folder, "meta.json"), data, 0o644)
}
