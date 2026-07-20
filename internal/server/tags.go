package server

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strings"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/logging"
)

// Curated tags: the hand-written classification that sits beside the API-supplied genres.
// They live in meta.json (the source of truth) and are mirrored into media_facets under
// kind "tag" so the sidebar list and the tag-scoped search are indexed queries. Every user
// can read the vocabulary and filter by it; only an admin writes one.

// maxTagsPerMedia caps how many tags one item carries, so a hostile or fat-fingered payload
// cannot bloat a meta.json.
const maxTagsPerMedia = 50

// maxTagLen caps a single tag's length. Tags are labels, not sentences.
const maxTagLen = 60

// normalizeTags trims, lowercases, and deduplicates a tag list, dropping blanks and
// over-long entries and preserving first-seen order. It is the single normaliser behind
// every write path, so "Rewatch", " rewatch " and "rewatch" can never coexist.
func normalizeTags(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || len(t) > maxTagLen || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) == maxTagsPerMedia {
			break
		}
	}
	return out
}

// handleListTags returns the whole curated tag vocabulary with per-tag item counts, for the
// library sidebar and the detail page's type-ahead.
func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	tags, err := db.ListTags(r.Context(), pool)
	if err != nil {
		http.Error(w, "could not list tags", http.StatusInternalServerError)
		return
	}
	writeJSON(w, tags)
}

// handleSetMediaTags replaces one item's curated tags. meta.json is written through the
// folder lock so the genres, technical block, and per-user state are all preserved, then the
// facet mirror is refreshed. It returns the normalised list so the UI renders exactly what
// was stored.
func (s *Server) handleSetMediaTags(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	m, err := db.GetMedia(r.Context(), pool, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	req, err := decodeJSON[struct {
		Tags []string `json:"tags"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	tags := normalizeTags(req.Tags)
	if err := s.setMediaTags(r.Context(), pool, m.Path, id, tags); err != nil {
		http.Error(w, "could not save the tags", http.StatusInternalServerError)
		return
	}
	s.elog().Info(userFrom(r)+" tagged "+m.Title, logging.Fields{"id": id, "tags": strings.Join(tags, ", ")})
	writeJSON(w, struct {
		Tags []string `json:"tags"`
	}{tags})
}

// setMediaTags writes a normalised tag list into a folder's meta.json and re-mirrors that
// item's facets. It is the one write path, shared by the per-item endpoint and the
// library-wide rename/delete.
func (s *Server) setMediaTags(ctx context.Context, pool *sql.DB, folder, id string, tags []string) error {
	meta, err := s.metaMgr.Update(folder, func(cur importer.Meta) importer.Meta {
		cur.Tags = tags
		return cur
	})
	if err != nil {
		return err
	}
	s.bestEffort(db.ReplaceMediaFacets(ctx, pool, id, meta.Actors, meta.Genres, meta.Tags), "mirror media tags")
	return nil
}

// handleRenameTag renames a tag across the whole library. Renaming onto an existing tag is
// how two tags are merged: the per-item list is deduplicated, so an item carrying both ends
// up with one.
func (s *Server) handleRenameTag(w http.ResponseWriter, r *http.Request) {
	pool, ok := s.adminPool(w, r)
	if !ok {
		return
	}
	req, err := decodeJSON[struct {
		Tag string `json:"tag"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	from := strings.ToLower(strings.TrimSpace(r.PathValue("tag")))
	to := normalizeTags([]string{req.Tag})
	if from == "" || len(to) == 0 {
		http.Error(w, "a tag name is required", http.StatusBadRequest)
		return
	}
	n, err := s.retagLibrary(r.Context(), pool, from, to[0])
	if err != nil {
		http.Error(w, "could not rename the tag", http.StatusInternalServerError)
		return
	}
	s.elog().Info(userFrom(r)+" renamed tag "+from+" to "+to[0], logging.Fields{"media": n})
	writeJSON(w, struct {
		Changed int `json:"changed"`
	}{n})
}

// handleDeleteTag strips a tag from every item that carries it.
func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	pool, ok := s.adminPool(w, r)
	if !ok {
		return
	}
	from := strings.ToLower(strings.TrimSpace(r.PathValue("tag")))
	if from == "" {
		http.Error(w, "a tag name is required", http.StatusBadRequest)
		return
	}
	n, err := s.retagLibrary(r.Context(), pool, from, "")
	if err != nil {
		http.Error(w, "could not delete the tag", http.StatusInternalServerError)
		return
	}
	s.elog().Info(userFrom(r)+" deleted tag "+from, logging.Fields{"media": n})
	writeJSON(w, struct {
		Changed int `json:"changed"`
	}{n})
}

// retagLibrary rewrites one tag across every item carrying it: to "" it is removed, otherwise
// it is replaced (merging when the target is already present). It returns how many items
// changed. Each folder is an independent meta.json write, so a failure on one leaves the rest
// applied rather than half-rolling-back a filesystem.
func (s *Server) retagLibrary(ctx context.Context, pool *sql.DB, from, to string) (int, error) {
	ids, err := db.MediaIDsWithTag(ctx, pool, from)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, id := range ids {
		m, err := db.GetMedia(ctx, pool, id)
		if err != nil {
			continue
		}
		meta, err := importer.ReadMeta(m.Path)
		if err != nil {
			continue
		}
		next := make([]string, 0, len(meta.Tags))
		for _, t := range meta.Tags {
			if t == from {
				if to != "" {
					next = append(next, to)
				}
				continue
			}
			next = append(next, t)
		}
		if err := s.setMediaTags(ctx, pool, m.Path, id, normalizeTags(next)); err != nil {
			continue
		}
		changed++
	}
	return changed, nil
}

// handleAdminTags is the admin tag manager's listing: the same vocabulary the library
// sidebar shows, but sorted by name so a table is scannable for the tag being cleaned up.
func (s *Server) handleAdminTags(w http.ResponseWriter, r *http.Request) {
	pool, ok := s.adminPool(w, r)
	if !ok {
		return
	}
	tags, err := db.ListTags(r.Context(), pool)
	if err != nil {
		http.Error(w, "could not list tags", http.StatusInternalServerError)
		return
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].Tag < tags[j].Tag })
	writeJSON(w, tags)
}

// adminPool returns the cache pool for an admin request, writing 503 when it is unavailable.
func (s *Server) adminPool(w http.ResponseWriter, r *http.Request) (*sql.DB, bool) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	return pool, true
}
