package server

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/library"
	"filefin/internal/logging"
	"filefin/internal/recognize"
)

// rebuildState is a cache rebuild's live progress, polled by the maintenance page. running is
// not serialized; it guards against starting a second rebuild while one is in flight.
type rebuildState struct {
	Total      int    `json:"total"`
	Done       int    `json:"done"`
	Categories int    `json:"categories"`
	Media      int    `json:"media"`
	Finished   bool   `json:"finished"`
	Error      string `json:"error"`
	running    bool
}

// rebuildTracker owns the rebuild progress behind its own mutex (the stagingTracker pattern).
type rebuildTracker struct {
	mu sync.Mutex
	st rebuildState
}

// begin marks a fresh rebuild running with its item denominator; ok is false when one is
// already in flight.
func (t *rebuildTracker) begin(total int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.st.running {
		return false
	}
	t.st = rebuildState{running: true, Total: total}
	return true
}

func (t *rebuildTracker) advance() {
	t.mu.Lock()
	t.st.Done++
	t.mu.Unlock()
}

func (t *rebuildTracker) finish(categories, media int) {
	t.mu.Lock()
	t.st.Categories, t.st.Media = categories, media
	t.st.Finished, t.st.running = true, false
	t.mu.Unlock()
}

func (t *rebuildTracker) fail(msg string) {
	t.mu.Lock()
	t.st.Error, t.st.Finished, t.st.running = msg, true, false
	t.mu.Unlock()
}

func (t *rebuildTracker) snapshot() rebuildState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.st
}

// handleRebuild starts a background rebuild of the cache from the data folder and returns at
// once; the maintenance page polls handleRebuildProgress for the bar. Running off the request
// keeps a large library from hanging the POST. The denominator is the cheap name-level folder
// count, so the bar has a total before the (slower) meta.json scan begins.
func (s *Server) handleRebuild(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	dataDir := s.dataDir()
	refs, _ := onDiskMediaRefs(dataDir)
	if !s.rebuildJob.begin(len(refs)) {
		http.Error(w, "rebuild already running", http.StatusConflict)
		return
	}
	go s.runRebuild(context.Background(), pool, dataDir)
	writeJSON(w, s.rebuildJob.snapshot())
}

// handleRebuildProgress returns the live rebuild progress for the polling maintenance page.
func (s *Server) handleRebuildProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.rebuildJob.snapshot())
}

// runRebuild flushes the cache and rebuilds it from the data folder: categories first (the
// source of truth on disk), then the media folders inside each, reporting progress per folder.
// Imports and the transient queues are dropped (they cannot be reconstructed). It serializes
// against a discovery tick via maintMu so the two never mutate the cache concurrently. This
// realizes the architecture's "cache is fully rebuildable from the filesystem" promise.
func (s *Server) runRebuild(ctx context.Context, pool *sql.DB, dataDir string) {
	s.maintMu.Lock()
	defer s.maintMu.Unlock()

	cats, err := library.List(dataDir)
	if err != nil {
		s.rebuildJob.fail("could not read categories")
		return
	}

	// Categories first (with parent links + effective other-media propagation).
	if err := s.mirrorCategories(ctx, pool); err != nil {
		s.rebuildJob.fail("could not rebuild categories")
		return
	}
	if err := db.ClearImportsAll(ctx, pool); err != nil {
		s.rebuildJob.fail("could not clear imports")
		return
	}
	if err := db.ClearMedia(ctx, pool); err != nil {
		s.rebuildJob.fail("could not clear media")
		return
	}
	if err := db.ClearOptimizeTasksAll(ctx, pool); err != nil {
		s.rebuildJob.fail("could not clear optimize tasks")
		return
	}
	if err := db.ClearEnrichTasksAll(ctx, pool); err != nil {
		s.rebuildJob.fail("could not clear enrich tasks")
		return
	}
	if err := db.ClearThumbnailTasksAll(ctx, pool); err != nil {
		s.rebuildJob.fail("could not clear thumbnail tasks")
		return
	}
	if err := db.ClearProbeTasksAll(ctx, pool); err != nil {
		s.rebuildJob.fail("could not clear probe tasks")
		return
	}
	if err := db.ClearHealthAll(ctx, pool); err != nil {
		s.rebuildJob.fail("could not clear media health")
		return
	}

	// Then the media folders within each category, advancing the bar per folder.
	mediaCount := 0
	for _, c := range cats {
		for _, m := range scanCategoryMedia(dataDir, c) {
			if err := db.InsertMedia(ctx, pool, m.media); err == nil {
				for _, f := range m.files {
					_ = db.InsertMediaFile(ctx, pool, f)
				}
				_ = db.ReplaceMediaFacets(ctx, pool, m.media.ID, m.actors, m.genres, m.tags)
				_ = db.ReplaceUserStateForMedia(ctx, pool, m.media.ID, m.userState)
				mediaCount++
			}
			s.rebuildJob.advance()
		}
	}

	s.logger().For(logging.Backend).Info("cache rebuilt from disk",
		logging.Fields{"categories": len(cats), "media": mediaCount})
	s.rebuildJob.finish(len(cats), mediaCount)
}

// scannedMedia pairs a media row with its file rows, its multivalued facets (actors, genres,
// curated tags), and the per-user playback state from meta.json. The scalar facets ride on
// media; the multivalued ones go to media_facets; userState re-derives the user_state mirror.
type scannedMedia struct {
	media     db.Media
	files     []db.MediaFile
	actors    []string
	genres    []string
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
	var actors, genres, tags []string
	userState := map[string]db.UserStateRow{}
	if m, err := importer.ReadMeta(dir); err == nil {
		title, year, desc, plot = m.Title, m.Year, m.Description, m.Plot
		// A folder enriched before this flag existed has no Enriched=true but does
		// carry an imdbID; treat either as enriched so it is not needlessly requeued.
		enriched = m.Enriched || m.Metadata["imdbID"] != ""
		language, country = m.Metadata["language"], m.Metadata["origin"]
		director, writer = m.Metadata["directedBy"], m.Metadata["writtenBy"]
		actors, genres, tags = m.Actors, m.Genres, m.Tags
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
		genres:    genres,
		tags:      tags,
		userState: userState,
	}
	for _, vf := range videos {
		base := filepath.Base(vf)
		p := recognize.ParseName(base, false)
		sm.files = append(sm.files, db.MediaFile{
			MediaID: id, Path: vf, Name: base,
			Season: p.Season, Episode: p.Episode, Ext: strings.ToLower(filepath.Ext(base)),
		})
	}
	// Idx is the episode progression the resume engine walks (marking every file before
	// the pointer watched), so order it by (season, episode) - the same order the detail
	// view shows - with a natural-name tiebreak for unnumbered files. A lexical sort would
	// place E10 before E2 and E37 before E4, so watching E37 would skip E4-E9.
	sort.Slice(sm.files, func(i, j int) bool {
		a, b := sm.files[i], sm.files[j]
		if a.Season != b.Season {
			return a.Season < b.Season
		}
		if a.Episode != b.Episode {
			return a.Episode < b.Episode
		}
		return naturalLess(a.Name, b.Name)
	})
	for i := range sm.files {
		sm.files[i].Idx = i
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

// naturalLess orders two names so embedded numbers compare by value, not lexically
// ("E4" before "E37", "Part 2" before "Part 10"). Digit runs compare numerically
// (leading zeros ignored); every other byte compares as before, so non-numeric names
// keep their plain-sort order.
func naturalLess(a, b string) bool {
	for len(a) > 0 && len(b) > 0 {
		if isDigit(a[0]) && isDigit(b[0]) {
			ai, bi := digitRun(a), digitRun(b)
			na := strings.TrimLeft(a[:ai], "0")
			nb := strings.TrimLeft(b[:bi], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			a, b = a[ai:], b[bi:]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// digitRun returns the length of the leading run of ASCII digits in s.
func digitRun(s string) int {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i
}
