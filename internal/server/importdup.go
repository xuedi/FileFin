package server

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"filefin/internal/db"
	"filefin/internal/watchlist"
)

// markDuplicates fills the Duplicate hint on every staged row whose media already sits in
// the library, so the preCheck page can warn before a byte is copied. It only ever labels
// rows - dropping one stays the admin's decision, because a deliberate re-import (a better
// rip of a film that is already there) is legitimate.
//
// Matching reuses the watchlist matcher, so an import inherits the same normalized-title,
// year-strict pairing the MyDramaList/MyAnimeList flows use; its approximate grade is
// ignored here, since a guessed duplicate would train the admin to click past the warning.
func (s *Server) markDuplicates(ctx context.Context, pool *sql.DB, rows []db.Import) {
	if len(rows) == 0 {
		return
	}
	media, err := db.AllMedia(ctx, pool)
	if err != nil || len(media) == 0 {
		return
	}
	lib := make([]watchlist.LibraryItem, len(media))
	byID := make(map[string]db.MediaSummary, len(media))
	for i, m := range media {
		lib[i] = watchlist.LibraryItem{ID: m.ID, Title: m.Title, Year: m.Year}
		byID[m.ID] = m
	}
	entries := make([]watchlist.Entry, len(rows))
	for i, r := range rows {
		entries[i] = watchlist.Entry{Title: r.Title, Year: r.Year}
	}
	dataDir := s.dataDir()
	seen := map[string]map[[2]int]bool{}
	for i, m := range watchlist.MatchLibrary(entries, lib) {
		if m.Item == nil || m.Confidence == watchlist.ConfidenceApproximate {
			continue
		}
		// A show folder matching by title is expected - the episodes of one show share it.
		// Only the very episode already on disk is a duplicate.
		if rows[i].Season > 0 || rows[i].Episode > 0 {
			if !hasEpisode(ctx, pool, seen, m.Item.ID, rows[i].Season, rows[i].Episode) {
				continue
			}
		}
		rows[i].Duplicate = libraryLabel(byID[m.Item.ID], dataDir)
	}
}

// hasEpisode reports whether a library item already holds a given season/episode, reading
// each item's files at most once per call chain.
func hasEpisode(ctx context.Context, pool *sql.DB, seen map[string]map[[2]int]bool, mediaID string, season, episode int) bool {
	have, ok := seen[mediaID]
	if !ok {
		have = map[[2]int]bool{}
		files, err := db.MediaFiles(ctx, pool, mediaID)
		if err != nil {
			return false
		}
		for _, f := range files {
			have[[2]int{f.Season, f.Episode}] = true
		}
		seen[mediaID] = have
	}
	return have[[2]int{season, episode}]
}

// libraryLabel names an existing library item the way the admin sees it in the library:
// its title, year, and the category path it lives in.
func libraryLabel(m db.MediaSummary, dataDir string) string {
	label := m.Title
	if m.Year > 0 {
		label = fmt.Sprintf("%s (%d)", m.Title, m.Year)
	}
	if rel, err := filepath.Rel(dataDir, filepath.Dir(m.FolderPath)); err == nil && rel != "." {
		label += " in " + filepath.ToSlash(rel)
	}
	return label
}
