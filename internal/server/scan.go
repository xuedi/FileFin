package server

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/library"
	"filefin/internal/logging"
	"filefin/internal/transcode"
)

// The scanner is the shared candidacy logic behind the three manual "scan" buttons and
// the background discovery agent (see discovery.go). Each refill walks the media cache,
// upserts a pending task per item that needs work, and prunes pending/error tasks for
// items now complete - all idempotent (INSERT OR IGNORE; prune never touches an active
// row), so a manual press and an agent tick can run concurrently without conflict.

// refillEnrich queues an enrichment task for every media folder still carrying stub
// metadata (skipping other-media categories, which never match OMDb) and drops tasks for
// folders enriched in the meantime. It returns how many candidates were queued.
func (s *Server) refillEnrich(ctx context.Context, pool *sql.DB) (int, error) {
	media, err := db.UnenrichedMedia(ctx, pool)
	if err != nil {
		return 0, err
	}
	flags, err := db.CategoryFlags(ctx, pool)
	if err != nil {
		return 0, err
	}
	queued, failed := 0, 0
	for _, m := range media {
		if flags[m.CategoryID] {
			continue
		}
		if err := db.UpsertPendingEnrich(ctx, pool, m.ID); err != nil {
			failed++
			continue
		}
		queued++
	}
	if failed > 0 {
		s.elog().Error("some enrichment tasks could not be queued", logging.Fields{"failed": failed})
	}
	s.bestEffort(db.PruneEnrichedTasks(ctx, pool), "prune enriched tasks")
	return queued, nil
}

// refillThumbnail queues a task for every folder whose sized posters are missing or stale
// (or, for an other-media folder with no poster, that needs a frame-derived one) and
// prunes tasks for folders now complete. It returns the candidate count.
func (s *Server) refillThumbnail(ctx context.Context, pool *sql.DB) (int, error) {
	media, err := db.AllMediaPosters(ctx, pool)
	if err != nil {
		return 0, err
	}
	flags, err := db.CategoryFlags(ctx, pool)
	if err != nil {
		return 0, err
	}
	candidates, failed := 0, 0
	for _, m := range media {
		if s.thumbnailCandidate(m, flags[m.CategoryID]) {
			if err := db.UpsertPendingThumbnail(ctx, pool, m.ID, flags[m.CategoryID]); err != nil {
				failed++
				continue
			}
			candidates++
		} else {
			s.bestEffort(db.PruneThumbnail(ctx, pool, m.ID), "prune thumbnail task")
		}
	}
	if failed > 0 {
		s.tlog().Error("some thumbnail tasks could not be queued", logging.Fields{"failed": failed})
	}
	return candidates, nil
}

// refillProbe queues a probe task for every media item whose cache format columns are
// missing on any file (e.g. after a rebuild), or whose meta.json lacks a complete
// technical block, and prunes tasks for items now fully probed. It returns the candidate
// count. A rebuild leaves the format columns empty, so this is what makes the cache
// self-heal its probed format on the next sweep.
func (s *Server) refillProbe(ctx context.Context, pool *sql.DB) (int, error) {
	media, err := db.AllMedia(ctx, pool)
	if err != nil {
		return 0, err
	}
	missing, err := db.MediaMissingFormat(ctx, pool)
	if err != nil {
		return 0, err
	}
	missingSet := make(map[string]bool, len(missing))
	for _, id := range missing {
		missingSet[id] = true
	}
	queued, failed := 0, 0
	for _, m := range media {
		if missingSet[m.ID] || technicalIncomplete(m.FolderPath) {
			if err := db.UpsertPendingProbe(ctx, pool, m.ID); err != nil {
				failed++
				continue
			}
			queued++
		} else {
			s.bestEffort(db.PruneProbe(ctx, pool, m.ID), "prune probe task")
		}
	}
	if failed > 0 {
		s.plog().Error("some probe tasks could not be queued", logging.Fields{"failed": failed})
	}
	return queued, nil
}

// technicalIncomplete reports whether a folder's meta.json is present but lacks a complete
// technical block (no block, or no container/video codec), so the probe agent should
// backfill it. A missing or unparseable meta.json is the health agent's concern, not the
// probe queue's, so it reads as complete here.
func technicalIncomplete(dir string) bool {
	m, err := importer.ReadMeta(dir)
	if err != nil {
		return false
	}
	t := m.Technical
	return t == nil || t.Container == "" || t.VideoCodec == ""
}

// Health-issue codes recorded in media_health. These are conditions the discovery agent
// cannot fix automatically (unlike the refill candidacies above, which it just enqueues);
// they are surfaced to the admin instead.
const (
	healthMetaMissing     = "meta_missing"     // no meta.json in the folder
	healthMetaInvalid     = "meta_invalid"     // meta.json present but unparseable
	healthNoVideo         = "no_video"         // a media folder with zero video files
	healthFileMissing     = "file_missing"     // a listed video file is gone
	healthFileEmpty       = "file_empty"       // a listed video file is zero-byte
	healthPosterMissing   = "poster_missing"   // the referenced base poster file is gone
	healthOrphanOptimized = "orphan_optimized" // an .optimized.mp4 whose source is gone
	healthOrphanPoster    = "orphan_poster"    // a sized poster_<W>.webp with no base poster
)

// Issue is one health finding: a stable code plus a human-readable detail.
type Issue struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// HealthReport is the result of checking one media item: whether it is healthy and the
// list of issues found.
type HealthReport struct {
	OK     bool
	Issues []Issue
}

// issuesJSON marshals the issue list for the media_health.issues column ("[]" when none).
func (h HealthReport) issuesJSON() string {
	if len(h.Issues) == 0 {
		return "[]"
	}
	b, err := json.Marshal(h.Issues)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// folderFingerprint summarizes a media folder cheaply so the rolling sweep can skip an
// unchanged item: the meta.json mtime plus the sorted names of its video files and poster.
// It reads directory entries but never parses meta.json.
func folderFingerprint(dir string) string {
	var parts []string
	if fi, err := os.Stat(filepath.Join(dir, "meta.json")); err == nil {
		parts = append(parts, fmt.Sprintf("meta:%d", fi.ModTime().UnixNano()))
	}
	videos, poster := scanFolderFiles(dir)
	names := make([]string, 0, len(videos)+1)
	for _, v := range videos {
		if fi, err := os.Stat(v); err == nil {
			names = append(names, fmt.Sprintf("%s:%d", filepath.Base(v), fi.Size()))
		} else {
			names = append(names, filepath.Base(v)+":?")
		}
	}
	if poster != "" {
		names = append(names, "poster:"+poster)
	}
	sort.Strings(names)
	parts = append(parts, names...)
	sum := sha1.Sum([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])[:16]
}

// checkHealth runs the per-item integrity checks against the folder on disk. Actionable
// states (needs enrich/thumbnail/optimize) are not issues - those go to the queues; only
// the conditions the agent cannot fix are reported here.
func checkHealth(m db.Media, files []db.MediaFile) HealthReport {
	var issues []Issue
	add := func(code, detail string) { issues = append(issues, Issue{Code: code, Detail: detail}) }

	// meta.json present and parseable.
	metaPath := filepath.Join(m.Path, "meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		add(healthMetaMissing, "meta.json is missing")
	} else if _, err := importer.ReadMeta(m.Path); err != nil {
		add(healthMetaInvalid, "meta.json could not be parsed")
	}

	// At least one video file, and each listed one present and non-empty.
	if len(files) == 0 {
		add(healthNoVideo, "folder has no video file")
	}
	for _, f := range files {
		fi, err := os.Stat(f.Path)
		if err != nil {
			add(healthFileMissing, f.Name+" is missing")
			continue
		}
		if fi.Size() == 0 {
			add(healthFileEmpty, f.Name+" is zero-byte")
		}
	}

	// The referenced base poster must exist.
	if m.Poster != "" {
		if _, err := os.Stat(filepath.Join(m.Path, m.Poster)); err != nil {
			add(healthPosterMissing, m.Poster+" is missing")
		}
	}

	// Orphaned derived artifacts: an optimized copy whose source is gone, or a sized
	// poster variant with no base poster to derive from.
	checkOrphans(m, files, add)

	return HealthReport{OK: len(issues) == 0, Issues: issues}
}

// onDiskRef locates a media folder on disk: its owning category, bare folder name, and
// absolute path. The discovery reconcile builds these once per tick (a cheap name-level
// walk, no meta.json parse) to diff the filesystem against the cache.
type onDiskRef struct {
	cat    library.Category
	folder string
	dir    string
}

// onDiskMediaRefs enumerates every media folder under dataDir keyed by its media id. It
// applies the same "skip sub-categories, skip folders with no video" gating as the
// rebuild, but does not read meta.json - so a whole-tree pass is cheap enough to run every
// tick.
func onDiskMediaRefs(dataDir string) (map[string]onDiskRef, error) {
	cats, err := library.List(dataDir)
	if err != nil {
		return nil, err
	}
	refs := map[string]onDiskRef{}
	for _, c := range cats {
		entries, err := os.ReadDir(filepath.Join(dataDir, c.Name))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(dataDir, c.Name, e.Name())
			if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
				continue // a sub-category, not a media item
			}
			if videos, _ := scanFolderFiles(dir); len(videos) == 0 {
				continue // a folder with no media is not a media item
			}
			refs[mediaID(c.Name, e.Name())] = onDiskRef{cat: c, folder: e.Name(), dir: dir}
		}
	}
	return refs, nil
}

// reconcileDiff brings the cache's media set in line with disk: it inserts a full row for
// every folder newly appeared on disk and deletes the cache rows (plus health and queued
// tasks) for every id whose folder has vanished. It returns the counts for logging.
func (s *Server) reconcileDiff(ctx context.Context, pool *sql.DB, dataDir string, refs map[string]onDiskRef) (added, removed int) {
	cachedIDs, err := db.AllMediaIDs(ctx, pool)
	if err != nil {
		s.dlog().Error("discovery could not list cached media", logging.Fields{"error": err.Error()})
		return 0, 0
	}
	cached := make(map[string]bool, len(cachedIDs))
	for _, id := range cachedIDs {
		cached[id] = true
	}
	for id, ref := range refs {
		if cached[id] {
			continue
		}
		if sm, ok := readMediaFolder(dataDir, ref.cat, ref.folder); ok {
			if err := db.InsertMedia(ctx, pool, sm.media); err != nil {
				continue
			}
			for _, f := range sm.files {
				_ = db.InsertMediaFile(ctx, pool, f)
			}
			_ = db.ReplaceMediaFacets(ctx, pool, id, sm.actors, sm.genres, sm.tags)
			_ = db.ReplaceUserStateForMedia(ctx, pool, id, sm.userState)
			added++
		}
	}
	for _, id := range cachedIDs {
		if _, ok := refs[id]; ok {
			continue
		}
		s.bestEffort(db.DeleteMedia(ctx, pool, id), "delete vanished media")
		s.bestEffort(db.PruneHealth(ctx, pool, id), "prune vanished health")
		s.bestEffort(db.PruneEnrich(ctx, pool, id), "prune vanished enrich task")
		s.bestEffort(db.PruneThumbnail(ctx, pool, id), "prune vanished thumbnail task")
		s.bestEffort(db.PruneOptimizeForMedia(ctx, pool, id), "prune vanished optimize task")
		s.bestEffort(db.PruneProbe(ctx, pool, id), "prune vanished probe task")
		removed++
	}
	return added, removed
}

// reconcileItem processes one media item in the rolling pass: if its folder fingerprint
// changed since the last check it re-reads meta.json and the file list into the cache,
// then runs the health checks and records the result (stamping the check time). It is the
// per-item body of a discovery tick.
func (s *Server) reconcileItem(ctx context.Context, pool *sql.DB, dataDir string, id string, ref onDiskRef, now int64) {
	cur := folderFingerprint(ref.dir)
	stored, _ := db.HealthFingerprint(ctx, pool, id)
	if cur != stored {
		if sm, ok := readMediaFolder(dataDir, ref.cat, ref.folder); ok {
			s.bestEffort(db.InsertMedia(ctx, pool, sm.media), "reconcile media row")
			s.bestEffort(db.ReplaceMediaFiles(ctx, pool, id, sm.files), "reconcile media files")
			s.bestEffort(db.ReplaceMediaFacets(ctx, pool, id, sm.actors, sm.genres, sm.tags), "reconcile media facets")
			s.bestEffort(db.ReplaceUserStateForMedia(ctx, pool, id, sm.userState), "reconcile user state")
		}
	}
	m, err := db.GetMedia(ctx, pool, id)
	if err != nil {
		return
	}
	files, err := db.MediaFiles(ctx, pool, id)
	if err != nil {
		return
	}
	report := checkHealth(m, files)
	s.bestEffort(db.UpsertHealth(ctx, pool, id, cur, report.OK, report.issuesJSON(), now), "upsert health")
}

// checkOrphans reports derived files left behind after their source disappeared.
func checkOrphans(m db.Media, files []db.MediaFile, add func(code, detail string)) {
	entries, err := os.ReadDir(m.Path)
	if err != nil {
		return
	}
	sources := map[string]bool{} // base name (no ext) of each present video file
	for _, f := range files {
		base := filepath.Base(f.Path)
		sources[strings.TrimSuffix(base, filepath.Ext(base))] = true
	}
	hasBasePoster := m.Poster != ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, transcode.OptimizedExt) {
			if base := strings.TrimSuffix(name, transcode.OptimizedExt); !sources[base] {
				add(healthOrphanOptimized, name+" has no source")
			}
		} else if strings.HasPrefix(lower, "poster_") && strings.HasSuffix(lower, ".webp") && !hasBasePoster {
			add(healthOrphanPoster, name+" has no base poster")
		}
	}
}
