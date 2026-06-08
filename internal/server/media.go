package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"filefin/internal/db"
	"filefin/internal/ffprobe"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/state"
	"filefin/internal/subtitle"
	"filefin/internal/thumbnail"
)

// userFrom returns the authenticated username stashed by the auth middleware.
func userFrom(r *http.Request) string {
	u, _ := r.Context().Value(userKey{}).(string)
	return u
}

// API DTOs. JSON tags define the wire shape consumed by the frontend.

type subtitleInfo struct {
	Index int    `json:"index"`
	Lang  string `json:"lang"`
	Label string `json:"label"`
}

type fileInfo struct {
	Index     int            `json:"index"`
	Name      string         `json:"name"`
	Season    int            `json:"season"`
	Episode   int            `json:"episode"`
	Ext       string         `json:"ext"`
	Transcode bool           `json:"transcode"` // true if the browser cannot direct-play it
	Watched   bool           `json:"watched"`
	Subtitles []subtitleInfo `json:"subtitles"`
}

type pair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type mediaDetail struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Year            int        `json:"year"`
	Description     string     `json:"description"`
	Plot            string     `json:"plot"`
	HasPoster       bool       `json:"hasPoster"`
	Files           []fileInfo `json:"files"`
	Metadata        []pair     `json:"metadata"`
	Ratings         []pair     `json:"ratings"`
	Technical       []pair     `json:"technical"`
	Actors          []string   `json:"actors"`
	Tags            []string   `json:"tags"`
	Watched         bool       `json:"watched"`
	Favorite        bool       `json:"favorite"`
	ContinueIndex   int        `json:"continueIndex"`
	ContinueSeconds int        `json:"continueSeconds"`
}

// metaOrder fixes the display order of the camelCase meta.json keys (the order the
// importer writes them) and maps each to a human label. Keys not listed fall through in
// sorted order with their raw name.
var metadataLabels = []struct{ key, label string }{
	{"release", "Released"},
	{"runtime", "Runtime"},
	{"language", "Language"},
	{"origin", "Country"},
	{"directedBy", "Directed by"},
	{"writtenBy", "Written by"},
	{"contentRating", "Rated"},
	{"awards", "Awards"},
	{"boxOffice", "Box office"},
	{"imdbID", "IMDb ID"},
}

var ratingLabels = []struct{ key, label string }{
	{"imdb", "IMDb"},
	{"rottenTomatoes", "Rotten Tomatoes"},
	{"metacritic", "Metacritic"},
}

// orderedPairs renders a meta map as ordered key/value pairs: the known keys first (in
// the given order, with their labels), then any leftover keys sorted alphabetically.
func orderedPairs(m map[string]string, order []struct{ key, label string }) []pair {
	out := []pair{}
	seen := map[string]bool{}
	for _, o := range order {
		if v, ok := m[o.key]; ok && v != "" {
			out = append(out, pair{Key: o.label, Value: v})
			seen[o.key] = true
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		if m[k] != "" {
			out = append(out, pair{Key: k, Value: m[k]})
		}
	}
	return out
}

// technicalPairs renders the ffprobe technical block as ordered display pairs.
func technicalPairs(t *ffprobe.Technical) []pair {
	out := []pair{}
	if t == nil {
		return out
	}
	if t.Width > 0 && t.Height > 0 {
		out = append(out, pair{Key: "Resolution", Value: strconv.Itoa(t.Width) + "x" + strconv.Itoa(t.Height)})
	}
	if t.VideoCodec != "" {
		out = append(out, pair{Key: "Video", Value: t.VideoCodec})
	}
	if t.AudioCodec != "" {
		out = append(out, pair{Key: "Audio", Value: t.AudioCodec})
	}
	if t.Container != "" {
		out = append(out, pair{Key: "Container", Value: t.Container})
	}
	if t.Duration > 0 {
		out = append(out, pair{Key: "Duration", Value: clockHMS(t.Duration)})
	}
	return out
}

// clockHMS formats whole seconds as H:MM:SS or M:SS.
func clockHMS(sec int) string {
	h, m, s := sec/3600, (sec%3600)/60, sec%60
	if h > 0 {
		return strconv.Itoa(h) + ":" + pad2(m) + ":" + pad2(s)
	}
	return strconv.Itoa(m) + ":" + pad2(s)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// userPool returns the cache pool for an end-user request, building it on the fly. A
// 503 is written and false returned when the cache is unavailable.
func (s *Server) userPool(w http.ResponseWriter, r *http.Request) (*sql.DB, bool) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	return pool, true
}

// handleCategoryMedia lists a category's media with the user's per-folder watched flag.
func (s *Server) handleCategoryMedia(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad category id", http.StatusBadRequest)
		return
	}
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	media, err := db.ListMediaByCategory(r.Context(), pool, id)
	if err != nil {
		http.Error(w, "could not list media", http.StatusInternalServerError)
		return
	}
	user := userFrom(r)
	for i := range media {
		if all, err := importer.LoadState(media[i].FolderPath); err == nil {
			media[i].Watched = all[user].Watched
		}
	}
	writeJSON(w, media)
}

// mediaWhere returns every media item whose per-user state satisfies keep, newest-first
// by the per-user `updated` timestamp in meta.json. State is read live, so a folder
// with no entry for the user is skipped (keep is never called for it).
func (s *Server) mediaWhere(ctx context.Context, pool *sql.DB, user string, keep func(state.UserState) bool) ([]db.MediaSummary, error) {
	media, err := db.AllMedia(ctx, pool)
	if err != nil {
		return nil, err
	}
	type scored struct {
		ms      db.MediaSummary
		updated int64
		watched bool
	}
	var hits []scored
	for _, m := range media {
		all, err := importer.LoadState(m.FolderPath)
		if err != nil {
			continue
		}
		us, ok := all[user]
		if !ok || !keep(us) {
			continue
		}
		hits = append(hits, scored{ms: m, updated: us.Updated, watched: us.Watched})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].updated > hits[j].updated })
	out := make([]db.MediaSummary, len(hits))
	for i, h := range hits {
		h.ms.Watched = h.watched
		out[i] = h.ms
	}
	return out, nil
}

// handleHome returns the user's continue/favorites/completed rows in one call.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	user := userFrom(r)
	ctx := r.Context()
	cont, err := s.mediaWhere(ctx, pool, user, func(us state.UserState) bool { return us.Progress != nil && !us.Watched })
	if err != nil {
		http.Error(w, "could not load home", http.StatusInternalServerError)
		return
	}
	favs, err := s.mediaWhere(ctx, pool, user, func(us state.UserState) bool { return us.Favorite })
	if err != nil {
		http.Error(w, "could not load home", http.StatusInternalServerError)
		return
	}
	done, err := s.mediaWhere(ctx, pool, user, func(us state.UserState) bool { return us.Watched })
	if err != nil {
		http.Error(w, "could not load home", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"continue": cont, "favorites": favs, "completed": done})
}

// fileKeys builds the ordered state file keys for a media item's files.
func fileKeys(files []db.MediaFile) []state.FileKey {
	keys := make([]state.FileKey, len(files))
	for i, f := range files {
		keys[i] = state.FileKey{Season: f.Season, Episode: f.Episode}
	}
	return keys
}

// handleMediaDetail returns the cache row, ordered files (with transcode + sidecar
// subtitle info), the rich meta.json fields, and the folded per-user watch-state.
func (s *Server) handleMediaDetail(w http.ResponseWriter, r *http.Request) {
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()
	m, err := db.GetMedia(ctx, pool, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	files, err := db.MediaFiles(ctx, pool, id)
	if err != nil {
		http.Error(w, "could not load files", http.StatusInternalServerError)
		return
	}

	d := mediaDetail{
		ID: m.ID, Title: m.Title, Year: m.Year,
		Description: m.Description, Plot: m.Plot, HasPoster: m.Poster != "",
		Files: []fileInfo{}, Metadata: []pair{}, Ratings: []pair{}, Technical: []pair{},
		Actors: []string{}, Tags: []string{},
	}

	// One meta.json read yields both the rich fields and the per-user state. The cache
	// row already covers the basics, so a missing meta.json is non-fatal.
	meta, _ := importer.ReadMeta(m.Path)
	d.Metadata = orderedPairs(meta.Metadata, metadataLabels)
	d.Ratings = orderedPairs(meta.Ratings, ratingLabels)
	d.Technical = technicalPairs(meta.Technical)
	if meta.Actors != nil {
		d.Actors = meta.Actors
	}
	if meta.Tags != nil {
		d.Tags = meta.Tags
	}

	// Sidecar subtitles in the media folder: those the importer placed alongside the
	// source, plus any an admin later drops in beside the media.
	folderEntries, _ := folderFileNames(m.Path)
	for _, f := range files {
		_, needsTranscode := playbackTarget(f.Path, f.Ext)
		fi := fileInfo{
			Index: f.Idx, Name: f.Name, Season: f.Season, Episode: f.Episode,
			Ext: f.Ext, Transcode: needsTranscode, Subtitles: []subtitleInfo{},
		}
		base := strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
		for k, sc := range subtitle.Sidecars(folderEntries, base) {
			fi.Subtitles = append(fi.Subtitles, subtitleInfo{Index: k, Lang: sc.Lang, Label: sc.Label})
		}
		d.Files = append(d.Files, fi)
	}

	// Fold the live per-user watch-state (from the same meta read) over the file refs.
	user := userFrom(r)
	us := meta.State[user]
	refs := state.Refs(fileKeys(files))
	v := state.View(us, refs)
	d.Watched = v.Watched
	d.Favorite = us.Favorite
	d.ContinueIndex = v.ContinueIndex
	d.ContinueSeconds = v.ContinueSeconds
	for i := range d.Files {
		if i < len(v.PerFile) {
			d.Files[i].Watched = v.PerFile[i]
		}
	}
	writeJSON(w, d)
}

// folderFileNames returns the (non-directory) entry names in a media folder, sorted.
func folderFileNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// handlePoster serves a media item's poster. ?size=detail|tile serves the pre-built
// sized WebP variant when present, falling back to the base poster.* while the thumbnail
// agent has not produced it yet; no size param serves the base poster.
func (s *Server) handlePoster(w http.ResponseWriter, r *http.Request) {
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	switch r.URL.Query().Get("size") {
	case "detail", "tile":
		name := thumbnail.DetailName()
		if r.URL.Query().Get("size") == "tile" {
			name = thumbnail.TileName()
		}
		if folder, err := db.FolderPath(ctx, pool, id); err == nil && folder != "" {
			variant := filepath.Join(folder, name)
			if _, err := os.Stat(variant); err == nil {
				http.ServeFile(w, r, variant)
				return
			}
		}
	}
	p, err := db.PosterPath(ctx, pool, id)
	if err != nil || p == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, p)
}

// folderFor resolves a media id to its on-disk folder, writing 404 when unknown.
func (s *Server) folderFor(w http.ResponseWriter, r *http.Request) (string, bool) {
	pool, ok := s.userPool(w, r)
	if !ok {
		return "", false
	}
	folder, err := db.FolderPath(r.Context(), pool, r.PathValue("id"))
	if err != nil || folder == "" {
		http.NotFound(w, r)
		return "", false
	}
	return folder, true
}

func (s *Server) handleFavorite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Favorite bool `json:"favorite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	folder, ok := s.folderFor(w, r)
	if !ok {
		return
	}
	if err := s.metaMgr.UpdateState(folder, userFrom(r), func(us state.UserState) state.UserState {
		us.Favorite = req.Favorite
		return us
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClearProgress(w http.ResponseWriter, r *http.Request) {
	folder, ok := s.folderFor(w, r)
	if !ok {
		return
	}
	if err := s.metaMgr.UpdateState(folder, userFrom(r), func(us state.UserState) state.UserState {
		us.Progress = nil
		return us
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClearWatched(w http.ResponseWriter, r *http.Request) {
	folder, ok := s.folderFor(w, r)
	if !ok {
		return
	}
	if err := s.metaMgr.UpdateState(folder, userFrom(r), func(us state.UserState) state.UserState {
		us.Watched = false
		us.Progress = nil // a leftover pointer would bounce the item into "continue"
		return us
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleProgress folds a playback report into the user's state in meta.json, logging a
// watched event on the first crossing.
func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File     int     `json:"file"`
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
		Event    string  `json:"event"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()
	m, err := db.GetMedia(ctx, pool, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	files, err := db.MediaFiles(ctx, pool, id)
	if err != nil {
		http.Error(w, "could not load files", http.StatusInternalServerError)
		return
	}
	refs := state.Refs(fileKeys(files))
	if req.File < 0 || req.File >= len(refs) {
		http.Error(w, "bad file index", http.StatusBadRequest)
		return
	}
	user := userFrom(r)
	var becameWatched bool
	if err := s.metaMgr.UpdateState(m.Path, user, func(us state.UserState) state.UserState {
		before := us.Watched
		out := state.Apply(us, refs, req.File, req.Position, req.Duration)
		becameWatched = !before && out.Watched
		return out
	}); err != nil {
		s.logger().For(logging.Frontend).Error("could not record progress for "+m.Title,
			logging.Fields{"path": m.Path, "error": err.Error()})
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if becameWatched {
		s.logger().For(logging.Frontend).Info(user+" watched "+m.Title, logging.Fields{"id": m.ID})
	}
	w.WriteHeader(http.StatusNoContent)
}
