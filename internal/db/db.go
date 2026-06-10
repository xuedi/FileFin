// Package db is the disposable SQLite cache. It is built entirely from a filesystem
// scan and can be deleted and rebuilt at any time with no data loss. The driver is
// modernc sqlite (pure Go, no cgo); there is no other backend.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Path is the per-user cache file. The cache is disposable, so it lives under the
// OS cache directory rather than in the data dir.
func Path() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("user cache dir: %w", err)
	}
	return filepath.Join(dir, "filefin", "cache.db"), nil
}

// RemoveCache deletes the cache database and its WAL/SHM sidecars, if present. A fresh
// install calls this so a leftover cache from a previous install never carries over; the
// cache is disposable and is rebuilt from the data dir on demand. Missing files are not
// an error.
func RemoveCache() error {
	path, err := Path()
	if err != nil {
		return err
	}
	var firstErr error
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Open opens (creating if needed) the cache database. WAL keeps readers (the Progress
// poll, browse) from blocking the import writer; busy_timeout absorbs brief contention.
// MaxOpenConns(1) serializes all writes onto one connection so the background importer
// and admin requests can never race into SQLITE_BUSY - correct and ample for a
// single-user cache.
func Open() (*sql.DB, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	pool, err := sql.Open("sqlite",
		"file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("open cache db: %w", err)
	}
	pool.SetMaxOpenConns(1)
	return pool, nil
}
