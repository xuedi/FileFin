package server

import (
	"context"
	"database/sql"
	"errors"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/library"
	"filefin/internal/logging"
)

// cacheDataVersion tracks one-time data backfills (not structural migrations, which migrate()
// handles). Bumping it triggers backfillCache once on the next cache open.
//
//	1: denormalized facets (media.{language,country,director,writer} + media_facets) and the
//	   user_state mirror, all re-derived from meta.json
//	2: the facet kinds split - genres moved from media_facets kind "tag" to "genre", leaving
//	   "tag" to the curated tags, and every meta.json was normalised to version 2
//	3: media.added, seeded from each folder's mtime and settled into its meta.json, so the
//	   "recently added" ordering has a stable value on a library that predates the field
//	4: media.added re-derived from the oldest media-file mtime. Version 3 seeded it from the
//	   folder mtime, which the thumbnailer, subtitle repair and optimizer all reset long after
//	   an item arrived, so nearly every folder claimed the same recent date
const cacheDataVersion = 4

// ensureDB returns the cache pool, building it on the fly when needed: it opens the
// SQLite cache once, runs the schema (idempotent), and - when the categories table is
// empty - reconciles it from the filesystem (the source of truth). It is called
// whenever an admin page is entered. The pool is cached on the Server.
func (s *Server) ensureDB(ctx context.Context) (*sql.DB, error) {
	s.mu.RLock()
	pool, cfg := s.db, s.cfg
	s.mu.RUnlock()
	if cfg == nil || !cfg.SetupComplete() {
		return nil, errors.New("not installed")
	}

	if pool == nil {
		p, err := db.Open()
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		// Another request may have opened it first; keep that one.
		if s.db == nil {
			s.db = p
		} else {
			_ = p.Close()
		}
		pool = s.db
		s.mu.Unlock()
	}

	if err := db.Build(ctx, pool); err != nil {
		return nil, err
	}
	n, err := db.CountCategories(ctx, pool)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		if cats, err := library.List(cfg.DataDir); err == nil && len(cats) > 0 {
			rows := make([]db.Category, 0, len(cats))
			for _, c := range cats {
				rows = append(rows, db.Category{ID: c.ID, Name: c.Name, Alias: c.Alias})
			}
			if err := db.ReplaceCategories(ctx, pool, rows); err != nil {
				return nil, err
			}
		}
	}
	if err := s.reconcileUsers(ctx, pool); err != nil {
		return nil, err
	}
	s.backfillCache(ctx, pool, cfg.DataDir)
	return pool, nil
}

// backfillCache runs the one-time data backfills an in-place schema migration leaves empty
// (the migrate() ALTERs add the columns; this fills them from meta.json without a manual
// rebuild). It is version-gated so it runs once per upgrade, then records the new version.
// A fresh cache is already populated by import/rebuild, so the pass finds nothing and just
// stamps the version.
func (s *Server) backfillCache(ctx context.Context, pool *sql.DB, dataDir string) {
	if v, err := db.SchemaVersion(ctx, pool); err != nil || v >= cacheDataVersion {
		return
	}
	s.backfillMu.Lock()
	defer s.backfillMu.Unlock()
	// Re-check under the lock: whoever held it may have just finished the pass.
	v, err := db.SchemaVersion(ctx, pool)
	if err != nil || v >= cacheDataVersion {
		return
	}
	cats, err := library.List(dataDir)
	if err != nil {
		return
	}
	n, upgraded := 0, 0
	for _, c := range cats {
		for _, sm := range scanCategoryMedia(dataDir, c) {
			s.bestEffort(db.SetMediaFacets(ctx, pool, sm.media.ID,
				sm.media.Language, sm.media.Country, sm.media.Director, sm.media.Writer), "backfill media facets")
			s.bestEffort(db.ReplaceMediaFacets(ctx, pool, sm.media.ID, sm.actors, sm.genres, sm.tags), "backfill media facets")
			s.bestEffort(db.ReplaceUserStateForMedia(ctx, pool, sm.media.ID, sm.userState), "backfill user state")
			if s.settleMetaFile(sm.media.Path, addedFromFiles(sm)) {
				upgraded++
			}
			n++
		}
	}
	if err := db.SetSchemaVersion(ctx, pool, cacheDataVersion); err != nil {
		s.bestEffort(err, "record cache data version")
		return
	}
	s.logger().For(logging.Backend).Info("backfilled search facets and playback state into cache",
		logging.Fields{"media": n, "metaUpgraded": upgraded, "version": cacheDataVersion})
}

// addedFromFiles re-derives a folder's "entered the library" time from its media files alone,
// ignoring what meta.json says. The backfill needs that: the value a version-3 cache wrote is
// the folder mtime, which later agent writes had already spoiled, so the pass has to replace
// it rather than preserve it. An item imported by this version carries a real stamp, and the
// file copy time it is replaced with is the same moment, so nothing is lost either way.
func addedFromFiles(sm scannedMedia) int64 {
	paths := make([]string, 0, len(sm.files))
	for _, f := range sm.files {
		paths = append(paths, f.Path)
	}
	return oldestFileTime(paths, sm.media.Path)
}

// settleMetaFile writes one folder's meta.json back in the current shape, in a single pass: a
// pre-version-2 file (genres under the old "tags" key) is folded by ReadMeta on the way in,
// and the added date is written to what the caller re-derived from disk, so that value stops
// drifting with the next write inside the folder. It reports whether the file needed the
// version fold, which is what the backfill counts; a failure is harmless and simply leaves
// the file as it was.
func (s *Server) settleMetaFile(folder string, added int64) bool {
	upgrade := importer.NeedsUpgrade(folder)
	if !upgrade && added == 0 {
		return false
	}
	_, err := s.metaMgr.Update(folder, func(m importer.Meta) importer.Meta {
		if added != 0 {
			m.Added = added
		}
		return m
	})
	if err != nil {
		s.bestEffort(err, "settle meta.json")
		return false
	}
	return upgrade
}
