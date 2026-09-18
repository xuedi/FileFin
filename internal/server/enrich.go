package server

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/omdb"
	"filefin/internal/sources"
	"filefin/internal/thumbnail"
)

const (
	enrichAgentName     = "OMDB"
	enrichRestInterval  = 2 * time.Second     // pause between lookups so the API is not hammered
	enrichIdlePoll      = 5 * time.Second     // re-check interval when the queue is empty or idle
	enrichRetryInterval = 14 * 24 * time.Hour // discovery re-queues a failed match this long after its last try
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
		installed := s.cfg != nil && s.cfg.SetupComplete()
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
		_ = db.FailEnrich(ctx, pool, task.ID, "media not found", time.Now().Unix())
		return
	}
	// Belt-and-suspenders: an other-media item must never reach OMDb even if a stale task
	// slipped past the scan filter. Finish it without a lookup.
	if other, _ := db.CategoryOtherMedia(ctx, pool, m.CategoryID); other {
		_ = db.FinishEnrich(ctx, pool, task.ID)
		return
	}
	if m.Enriched {
		s.snapshotOMDb(ctx, pool, client, task, m)
		return
	}
	// An IMDb id another source already found (TMDb) is a sharper lookup than the title.
	var mv *omdb.Movie
	known, _ := importer.ReadMeta(m.Path)
	if imdb := known.Metadata["imdbID"]; imdb != "" {
		mv, err = client.LookupByID(ctx, imdb)
	} else {
		mv, err = client.Lookup(ctx, m.Title, m.Year)
	}
	if err != nil {
		_ = db.FailEnrich(ctx, pool, task.ID, err.Error(), time.Now().Unix())
		s.elog().Info("omdb lookup failed for "+m.Title,
			logging.Fields{"title": m.Title, "year": m.Year, "error": err.Error()})
		return
	}
	// Additive: OMDb only fills gaps, keeping the folder's own title/year and any poster.
	if err := s.applyOmdbResult(ctx, pool, m, client, mv, m.Title, m.Year, false); err != nil {
		_ = db.FailEnrich(ctx, pool, task.ID, "write meta: "+err.Error(), time.Now().Unix())
		return
	}
	_ = db.FinishEnrich(ctx, pool, task.ID)
	s.elog().Info("enriched "+m.Title, logging.Fields{"title": m.Title, "year": m.Year, "imdbID": mv.ImdbID})
}

// applyOmdbResult writes an OMDb result onto a media folder and its cache row - the shared
// write path behind both the enrich agent and the admin manual re-match. In additive mode
// (the agent) the existing meta.json wins field by field and an existing poster is kept; in
// replace mode (a re-match) the chosen record wins, preserving only the base-owned technical
// (ffprobe) and per-user state blocks, and the poster is refreshed. The write goes through
// the shared per-folder lock so a concurrent playback event is never dropped.
func (s *Server) applyOmdbResult(ctx context.Context, pool *sql.DB, m db.Media, client *omdb.Client, mv *omdb.Movie, title string, year int, replace bool) error {
	snap := sources.FromOMDb(mv, time.Now().Unix())
	meta, err := s.metaMgr.Update(m.Path, func(cur importer.Meta) importer.Meta {
		fresh := importer.MetaFromOMDb(mv, title, year)
		out := importer.MergeMeta(cur, fresh)
		if replace {
			out = fresh
			out.Technical, out.State, out.Tags, out.Added = cur.Technical, cur.State, cur.Tags, cur.Added
			out.Cast = cur.Cast // the people agent sees the new IMDb id and refreshes it
			out.Sources = cur.Sources
			// A re-match is a fresh start: only what was typed by hand stays pinned, and another
			// source's record of the old match is dropped until it is looked up again.
			out.Choices = manualChoices(cur.Choices)
			if t := out.Sources[sources.TMDb]; t != nil && t.ImdbID != snap.ImdbID {
				delete(out.Sources, sources.TMDb)
			}
		}
		out.Title, out.Year = title, year
		out.Enriched = true
		out.Sources = withSnapshot(out.Sources, sources.OMDb, snap)
		out, _ = s.resolveMeta(out)
		return out
	})
	if err != nil {
		return err
	}

	posterRel := folderPoster(m.Path)
	if replace {
		posterRel = s.replacePoster(ctx, m.Path, client, mv)
	} else if posterRel == "" {
		posterRel = downloadPoster(ctx, m.Path, client, mv)
	}

	return s.writeMediaCacheRow(ctx, pool, m.ID, title, year, meta, posterRel)
}

// snapshotOMDb records OMDb's own record of an item that is already matched, so the merge has
// it to compare with another source; the item's merged fields change only where the merge says
// so. A record OMDb no longer has is kept as a failed snapshot, so it is not asked again soon.
func (s *Server) snapshotOMDb(ctx context.Context, pool *sql.DB, client *omdb.Client, task db.EnrichTask, m db.Media) {
	meta, err := importer.ReadMeta(m.Path)
	imdb := meta.Metadata["imdbID"]
	if err != nil || imdb == "" {
		_ = db.FinishEnrich(ctx, pool, task.ID)
		return
	}
	s.spendOMDbBackfill()
	now := time.Now().Unix()
	mv, err := client.LookupByID(ctx, imdb)
	var snap *importer.Snapshot
	switch {
	case err == nil:
		snap = sources.FromOMDb(mv, now)
	case strings.HasPrefix(err.Error(), "omdb: "): // OMDb answered: it has no such record
		snap = &importer.Snapshot{ImdbID: imdb, Fetched: now, Error: strings.TrimPrefix(err.Error(), "omdb: ")}
	default:
		_ = db.FailEnrich(ctx, pool, task.ID, err.Error(), now)
		return
	}
	written, err := s.metaMgr.Update(m.Path, func(cur importer.Meta) importer.Meta {
		cur.Sources = withSnapshot(cur.Sources, sources.OMDb, snap)
		out, _ := s.resolveMeta(cur)
		return out
	})
	if err != nil {
		_ = db.FailEnrich(ctx, pool, task.ID, "write meta: "+err.Error(), now)
		return
	}
	s.bestEffort(s.writeMediaCacheRow(ctx, pool, m.ID, written.Title, written.Year, written, folderPoster(m.Path)), "mirror omdb snapshot")
	_ = db.FinishEnrich(ctx, pool, task.ID)
	s.elog().Info("recorded OMDb snapshot for "+m.Title, logging.Fields{"id": m.ID, "imdbID": imdb, "error": snap.Error})
}

// writeMediaCacheRow projects a written meta.json onto its media cache row - the title/year,
// the enriched description/plot/poster, and the derived search facets. Shared by the enrich
// agent, the admin OMDb re-match, and the admin metadata editor so the cache never drifts
// from meta.json. Only the title/year write is load-bearing (it renames nothing but drives
// browsing/search); the rest are best-effort mirrors a rebuild can re-derive.
func (s *Server) writeMediaCacheRow(ctx context.Context, pool *sql.DB, id, title string, year int, meta importer.Meta, posterRel string) error {
	if err := db.SetMediaTitleYear(ctx, pool, id, title, year); err != nil {
		return err
	}
	_ = db.SetMediaEnriched(ctx, pool, id, meta.Description, meta.Plot, posterRel)
	_ = db.SetMediaFacets(ctx, pool, id,
		meta.Metadata["language"], meta.Metadata["origin"], meta.Metadata["directedBy"], meta.Metadata["writtenBy"])
	_ = db.ReplaceMediaFacets(ctx, pool, id, meta.FacetActors(), meta.Genres, meta.Tags)
	return nil
}

// downloadPoster fetches the OMDb poster into dir and returns its basename, or "" when there
// is nothing to fetch or the download fails.
func downloadPoster(ctx context.Context, dir string, client *omdb.Client, mv *omdb.Movie) string {
	if mv.ImdbID == "" || mv.ImdbID == "N/A" || mv.Poster == "" || mv.Poster == "N/A" {
		return ""
	}
	img, ct, err := client.Poster(ctx, mv.ImdbID, 600)
	if err != nil || len(img) == 0 {
		return ""
	}
	name := "poster" + omdb.PosterExt(ct)
	if os.WriteFile(filepath.Join(dir, name), img, 0o644) != nil {
		return ""
	}
	return name
}

// replacePoster refreshes a folder's poster for a re-match: on a successful download it
// removes the previous base poster and its stale sized variants (so the thumbnail agent
// rebuilds them from the new base), and returns the new basename. A failed download keeps
// whatever poster was already there.
func (s *Server) replacePoster(ctx context.Context, dir string, client *omdb.Client, mv *omdb.Movie) string {
	old := folderPoster(dir)
	name := downloadPoster(ctx, dir, client, mv)
	if name == "" {
		return old
	}
	if old != "" && old != name {
		_ = os.Remove(filepath.Join(dir, old))
	}
	_ = os.Remove(filepath.Join(dir, thumbnail.DetailName()))
	_ = os.Remove(filepath.Join(dir, thumbnail.TileName()))
	return name
}
