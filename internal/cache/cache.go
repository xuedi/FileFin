// Package cache is the disposable SQLite index. It is built entirely from a
// filesystem scan and can be deleted and rebuilt at any time with no data loss.
package cache

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver, no cgo

	"filefin/internal/model"
)

// Store wraps the SQLite cache database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the cache database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// busy_timeout lets a reader wait out the brief write lock a live Rebuild (reload) holds,
	// instead of failing with SQLITE_BUSY.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

const schema = `
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS media;
DROP TABLE IF EXISTS media_files;
DROP TABLE IF EXISTS media_meta;
DROP TABLE IF EXISTS tags;
CREATE TABLE categories (name TEXT PRIMARY KEY, path TEXT);
CREATE TABLE media (
  id TEXT PRIMARY KEY, category TEXT, folder TEXT, path TEXT,
  year INTEGER, title TEXT, description TEXT, plot TEXT,
  poster TEXT
);
CREATE TABLE media_files (media_id TEXT, idx INTEGER, path TEXT, name TEXT, season INTEGER, episode INTEGER, ext TEXT);
CREATE TABLE media_meta (media_id TEXT, section TEXT, ord INTEGER, k TEXT, v TEXT);
CREATE TABLE tags (media_id TEXT, tag TEXT);
CREATE INDEX idx_media_category ON media(category);
CREATE INDEX idx_files_media ON media_files(media_id);
CREATE INDEX idx_meta_media ON media_meta(media_id);
CREATE INDEX idx_tags_media ON tags(media_id);
`

// Rebuild wipes and repopulates the cache from a scan, in a single transaction.
// Deterministic: the same scan always yields the same catalog.
func (s *Store) Rebuild(scan *model.Scan) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	for _, cat := range scan.Categories {
		if _, err := tx.Exec(`INSERT INTO categories(name,path) VALUES(?,?)`, cat.Name, cat.Path); err != nil {
			return err
		}
		for _, m := range cat.Media {
			desc, plot := "", ""
			if m.Meta != nil {
				desc, plot = m.Meta.Description, m.Meta.Plot
			}
			if _, err := tx.Exec(
				`INSERT INTO media(id,category,folder,path,year,title,description,plot,poster)
				 VALUES(?,?,?,?,?,?,?,?,?)`,
				m.ID, m.Category, m.Folder, m.Path, m.Year, m.Title, desc, plot, m.Poster,
			); err != nil {
				return err
			}
			for i, f := range m.Files {
				if _, err := tx.Exec(
					`INSERT INTO media_files(media_id,idx,path,name,season,episode,ext) VALUES(?,?,?,?,?,?,?)`,
					m.ID, i, f.Path, f.Name, f.Season, f.Episode, f.Ext,
				); err != nil {
					return err
				}
			}
			if m.Meta != nil {
				for i, kv := range m.Meta.Metadata {
					if err := insertMeta(tx, m.ID, "metadata", i, kv.Key, kv.Value); err != nil {
						return err
					}
				}
				for i, kv := range m.Meta.Ratings {
					if err := insertMeta(tx, m.ID, "ratings", i, kv.Key, kv.Value); err != nil {
						return err
					}
				}
				for i, kv := range m.Meta.Technical {
					if err := insertMeta(tx, m.ID, "technical", i, kv.Key, kv.Value); err != nil {
						return err
					}
				}
				for i, a := range m.Meta.Actors {
					if err := insertMeta(tx, m.ID, "actor", i, a, ""); err != nil {
						return err
					}
				}
				for _, t := range m.Meta.Tags {
					if _, err := tx.Exec(`INSERT INTO tags(media_id,tag) VALUES(?,?)`, m.ID, t); err != nil {
						return err
					}
				}
			}
		}
	}
	return tx.Commit()
}

func insertMeta(tx *sql.Tx, id, section string, ord int, k, v string) error {
	_, err := tx.Exec(`INSERT INTO media_meta(media_id,section,ord,k,v) VALUES(?,?,?,?,?)`, id, section, ord, k, v)
	return err
}
