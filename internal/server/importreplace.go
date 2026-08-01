package server

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"filefin/internal/db"
	"filefin/internal/ffprobe"
	"filefin/internal/logging"
	"filefin/internal/transcode"
)

// subtitleExts are the sidecar formats an import places beside a video. They are removed
// with the file they belong to when a replacement lands under a different name.
var subtitleExts = map[string]bool{
	".srt": true, ".vtt": true, ".ass": true, ".ssa": true, ".sub": true, ".idx": true,
}

// replaceTarget resolves where a replacing row writes. The library item decides everything
// about placement: its own folder, and its own title/year for the file name, so the new file
// sits beside its siblings and the media keeps the id derived from that folder. Only the
// season/episode/part and the extension come from the row.
type replaceTarget struct {
	media db.Media
	dir   string
	title string
	year  int
}

// resolveReplace loads the library item a row replaces. ok is false when the row is an
// ordinary import, or when the item has since left the library - in which case the caller
// imports normally rather than failing the row, since the file is still wanted.
func (s *Server) resolveReplace(ctx context.Context, pool *sql.DB, row db.Import) (replaceTarget, bool) {
	if row.ReplaceMediaID == "" {
		return replaceTarget{}, false
	}
	m, err := db.GetMedia(ctx, pool, row.ReplaceMediaID)
	if err != nil || m.Path == "" {
		s.logger().For(logging.Import).Error("the media to replace is gone, importing "+row.Filename+" as new",
			logging.Fields{"mediaId": row.ReplaceMediaID, "title": row.Title})
		return replaceTarget{}, false
	}
	return replaceTarget{media: m, dir: m.Path, title: m.Title, year: m.Year}, true
}

// supersededFiles picks the library files a freshly written one replaces: same season and
// episode, minus the file just written (a same-name replacement overwrote its predecessor in
// place and leaves nothing to retire). A film split over discs shares season/episode 0, so
// when several candidates remain only an exact base-name match counts; anything still
// ambiguous is reported so the caller can leave it alone and say so.
func supersededFiles(files []db.MediaFile, season, episode int, target string) (old []db.MediaFile, ambiguous bool) {
	var cand []db.MediaFile
	for _, f := range files {
		if f.Season != season || f.Episode != episode || sameFile(f.Path, target) {
			continue
		}
		cand = append(cand, f)
	}
	if len(cand) <= 1 {
		return cand, false
	}
	base := baseNoExt(target)
	var exact []db.MediaFile
	for _, f := range cand {
		if baseNoExt(f.Path) == base {
			exact = append(exact, f)
		}
	}
	if len(exact) == 0 {
		return nil, true
	}
	return exact, false
}

func baseNoExt(path string) string {
	name := filepath.Base(path)
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func sameFile(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

// finishReplace swaps a library item over to the file just written: it retires the file the
// new one supersedes together with everything derived from it, re-derives the item's cache
// rows from disk (the reconcile path, so the media id, meta.json and per-user state are
// untouched), and clears the optimize queue rows so the new payload is judged afresh.
func (s *Server) finishReplace(ctx context.Context, pool *sql.DB, rt replaceTarget, row db.Import, target string, tech ffprobe.Technical) {
	files, err := db.MediaFiles(ctx, pool, rt.media.ID)
	if err != nil {
		s.logger().For(logging.Import).Error("could not read the media being replaced: "+rt.media.Title,
			logging.Fields{"mediaId": rt.media.ID, "error": err.Error()})
		return
	}
	old, ambiguous := supersededFiles(files, row.Season, row.Episode, target)
	if ambiguous {
		s.logger().For(logging.Import).Error("could not tell which file "+row.Filename+" replaces, keeping all of them",
			logging.Fields{"mediaId": rt.media.ID, "title": rt.media.Title, "target": target})
	}
	newBase := baseNoExt(target)
	for _, f := range old {
		if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
			s.logger().For(logging.Import).Error("could not remove the replaced file: "+f.Name,
				logging.Fields{"path": f.Path, "error": err.Error()})
			continue
		}
		removeOptimized(f.Path)
		if baseNoExt(f.Path) != newBase {
			removeSidecars(filepath.Dir(f.Path), baseNoExt(f.Path))
		}
	}
	// The common replace lands on the same name and retires no file at all, so the new
	// file's own pre-transcode is dropped unconditionally: it was made from the old payload.
	// Playback already refuses a sibling older than its source, but a replacement that is
	// direct-playable is never queued for optimizing, so nothing would ever overwrite it.
	removeOptimized(target)
	s.bestEffort(db.PruneOptimizeForMedia(ctx, pool, rt.media.ID), "prune optimize tasks of a replaced media")

	if cat, ok := s.categoryByID(rt.media.CategoryID); ok {
		if sm, ok := readMediaFolder(s.dataDir(), cat, filepath.Base(rt.media.Path)); ok {
			keepProbedFormats(sm.files, files, target, tech)
			s.bestEffort(db.InsertMedia(ctx, pool, sm.media), "refresh replaced media row")
			s.bestEffort(db.ReplaceMediaFiles(ctx, pool, sm.media.ID, sm.files), "refresh replaced media files")
			s.bestEffort(db.ReplaceMediaFacets(ctx, pool, sm.media.ID, sm.actors, sm.genres, sm.tags), "refresh replaced media facets")
		}
	}
	s.logger().For(logging.Import).Info("replaced "+rt.media.Title+" with a new copy",
		logging.Fields{"mediaId": rt.media.ID, "title": rt.media.Title, "target": target,
			"retired": len(old), "bytes": fileSize(target)})
}

// keepProbedFormats carries the probed container and codecs onto file rows just re-derived
// from disk, which cannot know them: the formats the cache already held travel by path, and
// the file just written takes the probe the import ran on it. Without this a replace would
// blind the playback and optimize decisions for every file of the folder - including the
// episodes it did not touch - until the probe agent caught up.
func keepProbedFormats(fresh, known []db.MediaFile, target string, tech ffprobe.Technical) {
	byPath := make(map[string]db.MediaFile, len(known))
	for _, f := range known {
		byPath[f.Path] = f
	}
	for i := range fresh {
		if sameFile(fresh[i].Path, target) {
			if !tech.Empty() {
				fresh[i].Container = tech.Container
				fresh[i].VideoCodec, fresh[i].AudioCodec = tech.VideoCodec, tech.AudioCodec
			}
			continue
		}
		if f, ok := byPath[fresh[i].Path]; ok {
			fresh[i].Container = f.Container
			fresh[i].VideoCodec, fresh[i].AudioCodec = f.VideoCodec, f.AudioCodec
		}
	}
}

// fileStamp identifies a file's content cheaply: its size and modification time. Two equal
// stamps mean nothing rewrote the file in between (a copy always lands with a fresh mtime).
func fileStamp(path string) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(fi.Size(), 10) + "@" + strconv.FormatInt(fi.ModTime().UnixNano(), 10), true
}

// removeOptimized drops a file's pre-transcode copy and any lock left by a crashed encode.
func removeOptimized(path string) {
	sib, _ := transcode.OptimizedSibling(path)
	_ = os.Remove(sib)
	_ = os.Remove(sib + ".tmp")
}

// removeSidecars drops the subtitle files belonging to a retired video, which are named
// after its base and would otherwise linger as orphans once it is gone.
func removeSidecars(dir, base string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, base+".") || !subtitleExts[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}
