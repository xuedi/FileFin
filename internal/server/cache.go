package server

import (
	"context"
	"database/sql"
	"errors"

	"filefin/internal/db"
	"filefin/internal/library"
	"filefin/internal/logging"
)

// cacheDataVersion tracks one-time data backfills (not structural migrations, which migrate()
// handles). Bumping it triggers backfillCache once on the next cache open.
//
//	1: denormalized facets (media.{language,country,director,writer} + media_facets) and the
//	   user_state mirror, all re-derived from meta.json
const cacheDataVersion = 1

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
	v, err := db.SchemaVersion(ctx, pool)
	if err != nil || v >= cacheDataVersion {
		return
	}
	cats, err := library.List(dataDir)
	if err != nil {
		return
	}
	n := 0
	for _, c := range cats {
		for _, sm := range scanCategoryMedia(dataDir, c) {
			s.bestEffort(db.SetMediaFacets(ctx, pool, sm.media.ID,
				sm.media.Language, sm.media.Country, sm.media.Director, sm.media.Writer), "backfill media facets")
			s.bestEffort(db.ReplaceMediaFacets(ctx, pool, sm.media.ID, sm.actors, sm.tags), "backfill media facets")
			s.bestEffort(db.ReplaceUserStateForMedia(ctx, pool, sm.media.ID, sm.userState), "backfill user state")
			n++
		}
	}
	if err := db.SetSchemaVersion(ctx, pool, cacheDataVersion); err != nil {
		s.bestEffort(err, "record cache data version")
		return
	}
	s.logger().For(logging.Backend).Info("backfilled search facets and playback state into cache",
		logging.Fields{"media": n, "version": cacheDataVersion})
}
