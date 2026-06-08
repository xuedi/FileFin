// Package state holds the per-user playback state stored inside each media folder's
// meta.json: a resume pointer, a permanent "watched" flag, and a favorite flag,
// recorded per username so it survives a cache rebuild (the filesystem stays the
// single source of truth). The state object is a deliberate exception to the
// read-only-data-dir rule; a cache rebuild never reads it, so flushing the cache
// cannot regress anyone's watch history.
//
// This package is the pure engine (Apply/View/Refs) and the value types only. The
// per-folder read-modify-write that mutates meta.json lives in the importer
// (importer.Manager), so the dependency points one way: importer imports state,
// never the reverse.
package state

// Pointer is a resume position: a file reference and a whole-second offset into it.
type Pointer struct {
	File    string `json:"file"` // "SxE", "#N", or "" for a single-file folder
	Seconds int    `json:"seconds"`
}

// UserState is one user's state for a media folder.
type UserState struct {
	Progress *Pointer `json:"progress,omitempty"`
	Watched  bool     `json:"watched,omitempty"`
	Favorite bool     `json:"favorite,omitempty"`
	// Updated is the unix-seconds time of the last change, stamped by the writer. It is
	// the ordering key the home buckets sort by (newest-first); meta.json's own mtime is
	// useless for that since the importer and enricher also touch the file.
	Updated int64 `json:"updated,omitempty"`
}
