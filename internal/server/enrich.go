package server

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/omdb"
)

const (
	enrichAgentName    = "OMDB"
	enrichRestInterval = 2 * time.Second // pause between lookups so the API is not hammered
	enrichIdlePoll     = 5 * time.Second // re-check interval when the queue is empty or idle
)

// elog returns the enrichment-scoped logger.
func (s *Server) elog() *logging.Scoped { return s.logger().For(logging.Enrich) }

// handleEnrichScan is the manual queue refill: it queues an enrichment task for every
// media folder still carrying stub metadata, prunes tasks for folders since enriched,
// and reports how many candidates were found and how many are now pending.
func (s *Server) handleEnrichScan(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	queued, err := s.refillEnrich(ctx, pool)
	if err != nil {
		http.Error(w, "could not scan for enrichment work", http.StatusInternalServerError)
		return
	}
	pending, err := db.CountPendingEnrich(ctx, pool)
	if err != nil {
		http.Error(w, "could not count enrichment tasks", http.StatusInternalServerError)
		return
	}
	s.elog().Info("enrichment scan queued work", logging.Fields{"candidates": queued, "pending": pending})
	writeJSON(w, scanResult{Candidates: queued, Pending: pending})
}

// handleActiveEnrich returns the in-flight enrichments and the count still waiting,
// for the Progress page.
func (s *Server) handleActiveEnrich(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	active, err := db.ListActiveEnrich(ctx, pool)
	if err != nil {
		http.Error(w, "could not list enrichment tasks", http.StatusInternalServerError)
		return
	}
	pending, err := db.CountPendingEnrich(ctx, pool)
	if err != nil {
		http.Error(w, "could not count enrichment tasks", http.StatusInternalServerError)
		return
	}
	writeJSON(w, queueStatus[db.ActiveEnrich]{Active: active, Pending: pending})
}

// startEnrichAgent launches the single OMDb enrichment agent once per process.
func (s *Server) startEnrichAgent() {
	s.enrichStart.Do(func() { go s.enrichLoop() })
}

// enrichLoop is the rate-limited agent: it drains the enrich queue one task at a time,
// resting briefly between lookups so OMDb is not overloaded, and idles when there is no
// work, no config, or no API key. It needs no cancellation - there is a single agent
// for the process lifetime, and a restart re-queues any interrupted task.
func (s *Server) enrichLoop() {
	ctx := context.Background()
	recovered := false
	for {
		s.mu.RLock()
		installed := s.cfg != nil
		s.mu.RUnlock()
		client := s.omdbClient()
		if !installed || client == nil {
			time.Sleep(enrichIdlePoll) // not installed yet, or no OMDb key configured
			continue
		}
		pool, err := s.ensureDB(ctx)
		if err != nil {
			time.Sleep(enrichIdlePoll)
			continue
		}
		if !recovered {
			_ = db.ResetEnrichingToPending(ctx, pool) // retry tasks interrupted by a restart
			recovered = true
		}
		task, ok, err := db.ClaimNextEnrich(ctx, pool, enrichAgentName)
		if err != nil || !ok {
			time.Sleep(enrichIdlePoll)
			continue
		}
		s.enrichOne(ctx, pool, client, task)
		time.Sleep(enrichRestInterval)
	}
}

// enrichOne looks one media folder up on OMDb and writes the result back: it refreshes
// meta.json (now flagged enriched, keeping the import-time technical block), downloads
// the poster into the folder, and updates the media cache row. A not-found or API error
// fails the task (left for the admin to see); a poster failure still enriches.
func (s *Server) enrichOne(ctx context.Context, pool *sql.DB, client *omdb.Client, task db.EnrichTask) {
	m, err := db.GetMedia(ctx, pool, task.MediaID)
	if err != nil {
		_ = db.FailEnrich(ctx, pool, task.ID, "media not found")
		return
	}
	// Belt-and-suspenders: an other-media item must never reach OMDb even if a stale task
	// slipped past the scan filter. Finish it without a lookup.
	if other, _ := db.CategoryOtherMedia(ctx, pool, m.CategoryID); other {
		_ = db.FinishEnrich(ctx, pool, task.ID)
		return
	}
	mv, err := client.Lookup(ctx, m.Title, m.Year)
	if err != nil {
		_ = db.FailEnrich(ctx, pool, task.ID, err.Error())
		s.elog().Info("omdb lookup failed for "+m.Title,
			logging.Fields{"title": m.Title, "year": m.Year, "error": err.Error()})
		return
	}

	// Enrichment is additive: OMDb only fills gaps, never overwriting metadata the
	// import (e.g. Plex) already wrote. The existing meta.json wins field by field, and
	// the write goes through the shared per-folder lock so it preserves anyone's
	// playback state written meanwhile.
	meta, err := s.metaMgr.Update(m.Path, func(cur importer.Meta) importer.Meta {
		merged := importer.MergeMeta(cur, importer.MetaFromOMDb(mv, m.Title, m.Year))
		merged.Title, merged.Year = m.Title, m.Year // always match the folder
		merged.Enriched = true
		return merged
	})
	if err != nil {
		_ = db.FailEnrich(ctx, pool, task.ID, "write meta: "+err.Error())
		return
	}

	// A poster is only downloaded when the folder has none: an existing poster (from
	// the import) is kept, never overwritten.
	posterRel := folderPoster(m.Path)
	if posterRel == "" && mv.ImdbID != "" && mv.ImdbID != "N/A" && mv.Poster != "" && mv.Poster != "N/A" {
		if img, ct, err := client.Poster(ctx, mv.ImdbID, 600); err == nil && len(img) > 0 {
			name := "poster" + omdb.PosterExt(ct)
			if os.WriteFile(filepath.Join(m.Path, name), img, 0o644) == nil {
				posterRel = name
			}
		}
	}

	_ = db.SetMediaEnriched(ctx, pool, task.MediaID, meta.Description, meta.Plot, posterRel)
	_ = db.SetMediaFacets(ctx, pool, task.MediaID,
		meta.Metadata["language"], meta.Metadata["origin"], meta.Metadata["directedBy"], meta.Metadata["writtenBy"])
	_ = db.ReplaceMediaFacets(ctx, pool, task.MediaID, meta.Actors, meta.Tags)
	_ = db.FinishEnrich(ctx, pool, task.ID)
	s.elog().Info("enriched "+m.Title, logging.Fields{"title": m.Title, "year": m.Year, "imdbID": mv.ImdbID})
}
