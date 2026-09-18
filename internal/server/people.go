package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/people"
	"filefin/internal/sources"
	"filefin/internal/tmdb"
)

const (
	peopleAgentName    = "TMDB"
	peopleRestInterval = 1 * time.Second // pause between items; the client paces each request too
	peopleIdlePoll     = 5 * time.Second // re-check interval when the queue is empty or idle

	// castLimit keeps the top billed people of an item. A long-running series credits
	// hundreds; past the first twenty they are guest roles nobody browses by.
	castLimit = 20

	// photoMaxAge lets browsers keep a photo for a month: a stored person is never fetched
	// again, so the file behind an id does not change.
	photoMaxAge = "private, max-age=2592000"
)

// pplog returns the people-scoped logger.
func (s *Server) pplog() *logging.Scoped { return s.logger().For(logging.People) }

// tmdbClient returns a TMDb client when a key is configured, else nil.
func (s *Server) tmdbClient() *tmdb.Client {
	s.mu.RLock()
	key := ""
	if s.cfg != nil {
		key = s.cfg.TMDBKey
	}
	s.mu.RUnlock()
	if key == "" {
		return nil
	}
	return s.newTMDb(key)
}

// peopleStore returns the shared people store of the configured data dir.
func (s *Server) peopleStore() people.Store { return people.New(s.dataDir()) }

// needsPeople reports whether an item has cast work left: it has something to resolve the
// cast by (an IMDb id, or a TMDb match), and either no current cast yet or a cast member the
// people store does not hold.
func needsPeople(meta importer.Meta, store people.Store) bool {
	if meta.Metadata["imdbID"] == "" && !meta.Sources[sources.TMDb].Usable() {
		return false
	}
	if !meta.CastCurrent() {
		return true
	}
	for _, c := range meta.Cast.Members {
		if !store.Has(c.ID) {
			return true
		}
	}
	return false
}

// handlePeopleScan is the manual queue refill: it queues a people task for every item with
// cast work left, prunes tasks for items now complete, and reports the counts.
func (s *Server) handlePeopleScan(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.tmdbClient() == nil {
		http.Error(w, "set a TMDb API key first", http.StatusConflict)
		return
	}
	ctx := r.Context()
	queued, err := s.refillPeople(ctx, pool)
	if err != nil {
		http.Error(w, "could not scan for cast work", http.StatusInternalServerError)
		return
	}
	pending, err := db.CountPendingPeople(ctx, pool)
	if err != nil {
		http.Error(w, "could not count people tasks", http.StatusInternalServerError)
		return
	}
	s.pplog().Info("cast scan queued work", logging.Fields{"candidates": queued, "pending": pending})
	writeJSON(w, scanResult{Candidates: queued, Pending: pending})
}

// handleActivePeople returns the in-flight cast resolves and the count still waiting.
func (s *Server) handleActivePeople(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	active, err := db.ListActivePeople(ctx, pool)
	if err != nil {
		http.Error(w, "could not list people tasks", http.StatusInternalServerError)
		return
	}
	pending, err := db.CountPendingPeople(ctx, pool)
	if err != nil {
		http.Error(w, "could not count people tasks", http.StatusInternalServerError)
		return
	}
	writeJSON(w, queueStatus[db.ActivePeople]{Active: active, Pending: pending})
}

// handlePersonPhoto serves a stored person's photo from the people store.
func (s *Server) handlePersonPhoto(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	p := s.peopleStore().PhotoPath(id)
	if p == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", photoMaxAge)
	http.ServeFile(w, r, p)
}

// startPeopleAgent launches the single people agent once per process.
func (s *Server) startPeopleAgent() {
	s.peopleStart.Do(func() { go s.peopleLoop() })
}

// peopleLoop drains the people queue one item at a time, resting between items and idling
// when there is no work, no config, or no TMDb key. A single agent runs for the process
// lifetime; a restart re-queues any interrupted task.
func (s *Server) peopleLoop() {
	ctx := context.Background()
	recovered := false
	for {
		s.mu.RLock()
		installed := s.cfg != nil && s.cfg.SetupComplete()
		s.mu.RUnlock()
		client := s.tmdbClient()
		if !installed || client == nil {
			time.Sleep(peopleIdlePoll)
			continue
		}
		pool, err := s.ensureDB(ctx)
		if err != nil {
			time.Sleep(peopleIdlePoll)
			continue
		}
		if !recovered {
			_ = db.ResetResolvingToPending(ctx, pool) // retry tasks interrupted by a restart
			recovered = true
		}
		task, ok, err := db.ClaimNextPeople(ctx, pool, peopleAgentName)
		if err != nil || !ok {
			time.Sleep(peopleIdlePoll)
			continue
		}
		s.peopleOne(ctx, pool, client, task)
		time.Sleep(peopleRestInterval)
	}
}

// peopleOne resolves one item's cast on TMDb when it has none for its IMDb id, writes the
// cast block into meta.json through the shared per-folder lock, mirrors the cast names into
// the search facets, and then stores every credited person the people store does not hold
// yet. A title TMDb does not know is recorded as an empty cast with the reason, so it is not
// asked again; an API or network failure fails the task for the next scan to retry.
func (s *Server) peopleOne(ctx context.Context, pool *sql.DB, client *tmdb.Client, task db.PeopleTask) {
	m, err := db.GetMedia(ctx, pool, task.MediaID)
	if err != nil {
		_ = db.FailPeople(ctx, pool, task.ID, "media not found")
		return
	}
	meta, err := importer.ReadMeta(m.Path)
	if err != nil {
		_ = db.FailPeople(ctx, pool, task.ID, "meta.json unreadable")
		return
	}
	imdb := meta.Metadata["imdbID"]
	if !needsPeople(meta, s.peopleStore()) {
		_ = db.FinishPeople(ctx, pool, task.ID)
		return
	}

	cast := meta.Cast
	if !meta.CastCurrent() {
		cast, err = resolveCast(ctx, client, imdb, meta.Sources[sources.TMDb])
		if err != nil {
			_ = db.FailPeople(ctx, pool, task.ID, err.Error())
			s.pplog().Info("cast lookup failed for "+m.Title, logging.Fields{"id": m.ID, "imdbID": imdb, "error": err.Error()})
			return
		}
		written, err := s.metaMgr.Update(m.Path, func(cur importer.Meta) importer.Meta {
			cur.Cast = cast
			return cur
		})
		if err != nil {
			_ = db.FailPeople(ctx, pool, task.ID, "write meta: "+err.Error())
			return
		}
		s.bestEffort(db.ReplaceMediaFacets(ctx, pool, m.ID, written.FacetActors(), written.Genres, written.Tags), "mirror cast facets")
	}

	store := s.peopleStore()
	ffmpeg := s.thumbnailFFmpeg()
	added, failed := 0, 0
	for _, c := range cast.Members {
		if store.Has(c.ID) {
			continue
		}
		if err := storePerson(ctx, client, store, ffmpeg, c); err != nil {
			failed++
			s.pplog().Debug("could not store person "+c.Name, logging.Fields{"person": c.ID, "error": err.Error()})
			continue
		}
		added++
	}
	_ = db.FinishPeople(ctx, pool, task.ID)
	fields := logging.Fields{"id": m.ID, "cast": len(cast.Members), "newPeople": added, "failed": failed}
	if cast.Error != "" {
		fields["reason"] = cast.Error
	}
	s.pplog().Info("resolved cast for "+m.Title, fields)
}

// resolveCast returns an item's top billed cast on TMDb. The item's TMDb match is used when
// it describes the same IMDb id (or the item has none); otherwise the IMDb id is looked up.
// A title TMDb does not know yields a cast block carrying the reason rather than an error.
func resolveCast(ctx context.Context, client *tmdb.Client, imdb string, match *importer.Snapshot) (*importer.Cast, error) {
	cast := &importer.Cast{ImdbID: imdb, Fetched: time.Now().Unix()}
	title, ok := matchedTitle(imdb, match)
	if !ok {
		var err error
		title, err = client.FindByIMDb(ctx, imdb)
		if errors.Is(err, tmdb.ErrNotFound) {
			cast.Error = "not on TMDb"
			return cast, nil
		}
		if err != nil {
			return nil, err
		}
	}
	credits, err := client.Credits(ctx, title)
	if err != nil && !errors.Is(err, tmdb.ErrNotFound) {
		return nil, err
	}
	cast.TMDbID, cast.Kind = title.ID, title.Kind
	at := map[int]int{} // person id -> index in Members; one actor can play two parts
	for _, c := range credits {
		if c.ID <= 0 || c.Name == "" {
			continue
		}
		if i, ok := at[c.ID]; ok {
			if c.Character != "" && !strings.Contains(cast.Members[i].Character, c.Character) {
				cast.Members[i].Character = strings.TrimPrefix(cast.Members[i].Character+" / "+c.Character, " / ")
			}
			continue
		}
		if len(cast.Members) == castLimit {
			continue
		}
		at[c.ID] = len(cast.Members)
		cast.Members = append(cast.Members, importer.CastMember{ID: c.ID, Name: c.Name, Character: c.Character, Order: c.Order})
	}
	if len(cast.Members) == 0 {
		cast.Error = "no cast on TMDb"
	}
	return cast, nil
}

// matchedTitle is the TMDb title an item's TMDb snapshot names, when it describes the item's
// IMDb id (or the item has none).
func matchedTitle(imdb string, match *importer.Snapshot) (tmdb.Title, bool) {
	if !match.Usable() || (imdb != "" && match.ImdbID != imdb) {
		return tmdb.Title{}, false
	}
	id, err := strconv.Atoi(match.ID)
	if err != nil || id <= 0 {
		return tmdb.Title{}, false
	}
	return tmdb.Title{Kind: importer.TMDbKind(match.Kind), ID: id}, true
}

// storePerson fetches one person's details and photo into the people store. The record is
// written last, because a stored record is what marks the person done: a photo that failed
// to download for a passing reason leaves no record, so the next scan tries again. A person
// or photo TMDb no longer has is stored without it.
func storePerson(ctx context.Context, client *tmdb.Client, store people.Store, ffmpeg string, c importer.CastMember) error {
	rec := people.Record{ID: c.ID, Name: c.Name, Fetched: time.Now().Unix()}
	p, err := client.Person(ctx, c.ID)
	switch {
	case err == nil:
		rec.Department, rec.Birthday, rec.Deathday, rec.ProfilePath = p.Department, p.Birthday, p.Deathday, p.ProfilePath
		if p.Name != "" {
			rec.Name = p.Name
		}
	case !errors.Is(err, tmdb.ErrNotFound):
		return err
	}
	if rec.ProfilePath != "" {
		img, err := client.ProfileImage(ctx, rec.ProfilePath)
		switch {
		case err == nil:
			if err := store.SavePhoto(ctx, ffmpeg, c.ID, img); err != nil {
				return err
			}
		case !errors.Is(err, tmdb.ErrNotFound):
			return err
		}
	}
	return store.Write(rec)
}
