package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/library"
	"filefin/internal/logging"
	"filefin/internal/recognize"
)

// handleRebuild flushes the cache and rebuilds it from the data folder: categories
// first (the source of truth on disk), then the media folders inside each. Imports are
// transient and cannot be reconstructed, so they are simply dropped. This realizes the
// architecture's "cache is fully rebuildable from the filesystem" promise.
func (s *Server) handleRebuild(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	dataDir := s.dataDir()

	// Serialize against a discovery tick so the two never mutate the cache concurrently.
	s.maintMu.Lock()
	defer s.maintMu.Unlock()

	cats, err := library.List(dataDir)
	if err != nil {
		http.Error(w, "could not read categories", http.StatusInternalServerError)
		return
	}

	// Categories first (with parent links + effective other-media propagation).
	if err := s.mirrorCategories(ctx, pool); err != nil {
		http.Error(w, "could not rebuild categories", http.StatusInternalServerError)
		return
	}
	if err := db.ClearImportsAll(ctx, pool); err != nil {
		http.Error(w, "could not clear imports", http.StatusInternalServerError)
		return
	}
	if err := db.ClearMedia(ctx, pool); err != nil {
		http.Error(w, "could not clear media", http.StatusInternalServerError)
		return
	}
	if err := db.ClearOptimizeTasksAll(ctx, pool); err != nil {
		http.Error(w, "could not clear optimize tasks", http.StatusInternalServerError)
		return
	}
	if err := db.ClearEnrichTasksAll(ctx, pool); err != nil {
		http.Error(w, "could not clear enrich tasks", http.StatusInternalServerError)
		return
	}
	if err := db.ClearThumbnailTasksAll(ctx, pool); err != nil {
		http.Error(w, "could not clear thumbnail tasks", http.StatusInternalServerError)
		return
	}
	if err := db.ClearProbeTasksAll(ctx, pool); err != nil {
		http.Error(w, "could not clear probe tasks", http.StatusInternalServerError)
		return
	}
	if err := db.ClearHealthAll(ctx, pool); err != nil {
		http.Error(w, "could not clear media health", http.StatusInternalServerError)
		return
	}

	// Then the media folders within each category.
	mediaCount := 0
	for _, c := range cats {
		for _, m := range scanCategoryMedia(dataDir, c) {
			if err := db.InsertMedia(ctx, pool, m.media); err != nil {
				continue
			}
			for _, f := range m.files {
				_ = db.InsertMediaFile(ctx, pool, f)
			}
			_ = db.ReplaceMediaFacets(ctx, pool, m.media.ID, m.actors, m.tags)
			_ = db.ReplaceUserStateForMedia(ctx, pool, m.media.ID, m.userState)
			mediaCount++
		}
	}

	s.logger().For(logging.Backend).Info("cache rebuilt from disk",
		logging.Fields{"categories": len(cats), "media": mediaCount})
	writeJSON(w, struct {
		Categories int `json:"categories"`
		Media      int `json:"media"`
	}{len(cats), mediaCount})
}

// scannedMedia pairs a media row with its file rows, its multivalued facets (actors, genres),
// and the per-user playback state from meta.json. The scalar facets ride on media; actors/tags
// go to media_facets; userState re-derives the user_state mirror.
type scannedMedia struct {
	media     db.Media
	files     []db.MediaFile
	actors    []string
	tags      []string
	userState map[string]db.UserStateRow
}

// scanCategoryMedia reads every media folder under one category and reconstructs its
// cache rows from meta.json (falling back to the folder name) and the files on disk.
func scanCategoryMedia(dataDir string, c library.Category) []scannedMedia {
	var out []scannedMedia
	catDir := filepath.Join(dataDir, c.Name)
	entries, err := os.ReadDir(catDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if sm, ok := readMediaFolder(dataDir, c, e.Name()); ok {
			out = append(out, sm)
		}
	}
	return out
}

// readMediaFolder reconstructs one media folder's cache rows from meta.json (falling back
// to parsing the folder name) and the video files on disk. ok is false when the folder is
// a sub-category (has config.json) or holds no video files - neither is a media item. It
// is the shared per-folder read used by both the full rebuild and the incremental
// discovery reconcile.
func readMediaFolder(dataDir string, c library.Category, folder string) (scannedMedia, bool) {
	dir := filepath.Join(dataDir, c.Name, folder)
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
		return scannedMedia{}, false // a sub-category, scanned as its own category
	}
	videos, poster := scanFolderFiles(dir)
	if len(videos) == 0 {
		return scannedMedia{}, false // a folder with no media is not a media item
	}

	// Prefer the written meta.json; fall back to parsing the folder name.
	title, year, desc, plot := "", 0, "", ""
	enriched := false
	var language, country, director, writer string
	var actors, tags []string
	userState := map[string]db.UserStateRow{}
	if m, err := importer.ReadMeta(dir); err == nil {
		title, year, desc, plot = m.Title, m.Year, m.Description, m.Plot
		// A folder enriched before this flag existed has no Enriched=true but does
		// carry an imdbID; treat either as enriched so it is not needlessly requeued.
		enriched = m.Enriched || m.Metadata["imdbID"] != ""
		language, country = m.Metadata["language"], m.Metadata["origin"]
		director, writer = m.Metadata["directedBy"], m.Metadata["writtenBy"]
		actors, tags = m.Actors, m.Tags
		for u, st := range m.State {
			userState[u] = userStateRow(st)
		}
	}
	if title == "" {
		p := recognize.ParseName(folder, false)
		title, year = p.Title, p.Year
		if title == "" {
			title = folder
		}
	}

	id := mediaID(c.Name, folder)
	sm := scannedMedia{
		media: db.Media{
			ID: id, CategoryID: c.ID, Path: dir,
			Year: year, Title: title, Description: desc, Plot: plot, Poster: poster, Enriched: enriched,
			Language: language, Country: country, Director: director, Writer: writer,
		},
		actors:    actors,
		tags:      tags,
		userState: userState,
	}
	for i, vf := range videos {
		base := filepath.Base(vf)
		p := recognize.ParseName(base, false)
		sm.files = append(sm.files, db.MediaFile{
			MediaID: id, Idx: i, Path: vf, Name: base,
			Season: p.Season, Episode: p.Episode, Ext: strings.ToLower(filepath.Ext(base)),
		})
	}
	return sm, true
}

// scanFolderFiles returns the sorted video files in a media folder plus the base name
// of its poster (poster.*), if any.
func scanFolderFiles(dir string) ([]string, string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ""
	}
	var videos []string
	poster := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if isOptimizedSibling(name) {
			continue // a derived direct-play copy is never a media file of its own
		}
		if videoExts[strings.ToLower(filepath.Ext(name))] {
			videos = append(videos, filepath.Join(dir, name))
		} else if strings.HasPrefix(strings.ToLower(name), "poster.") {
			poster = name
		}
	}
	sort.Strings(videos)
	return videos, poster
}
