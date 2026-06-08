package server

import (
	"context"
	"database/sql"
	"errors"

	"filefin/internal/db"
	"filefin/internal/library"
)

// ensureDB returns the cache pool, building it on the fly when needed: it opens the
// SQLite cache once, runs the schema (idempotent), and - when the categories table is
// empty - reconciles it from the filesystem (the source of truth). It is called
// whenever an admin page is entered. The pool is cached on the Server.
func (s *Server) ensureDB(ctx context.Context) (*sql.DB, error) {
	s.mu.RLock()
	pool, cfg := s.db, s.cfg
	s.mu.RUnlock()
	if cfg == nil {
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
	return pool, nil
}
