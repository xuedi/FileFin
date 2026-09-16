package server

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"filefin/internal/db"
	"filefin/internal/mediafmt"
)

// A media folder is named once, at import. When a later metadata edit or a re-match changes
// the title or the year, the folder and its files keep the old name and nothing corrects
// them. This is the report side of that drift: what each folder and file should be called
// under the configured naming format. It is derived from the cache rather than stored, so it
// is right the moment a metadata edit is saved; the rename that acts on it is in rename.go.

// nameChange is one name a rename would replace.
type nameChange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// renamePlan is everything one item's rename would do: the folder itself (From == To when
// only the files inside are wrong) and every file that changes name.
type renamePlan struct {
	Folder nameChange   `json:"folder"`
	Files  []nameChange `json:"files"`
}

// misnamedMedia is one item whose names contradict its metadata, with the plan that fixes it.
type misnamedMedia struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Year     int        `json:"year"`
	Category string     `json:"category"`
	Plan     renamePlan `json:"plan"`
}

// handleMisnamed lists every media item whose folder or files disagree with its metadata.
func (s *Server) handleMisnamed(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	items, err := misnamedItems(r.Context(), pool, s.mediaFormat())
	if err != nil {
		http.Error(w, "could not list misnamed media", http.StatusInternalServerError)
		return
	}
	writeJSON(w, struct {
		Items []misnamedMedia `json:"items"`
	}{items})
}

// misnamedItems is the report itself, shared by the handler and the dashboard's count.
func misnamedItems(ctx context.Context, pool *sql.DB, format string) ([]misnamedMedia, error) {
	media, err := db.ListMediaWithCategory(ctx, pool)
	if err != nil {
		return nil, err
	}
	files, err := db.AllFiles(ctx, pool)
	if err != nil {
		return nil, err
	}
	byMedia := map[string][]db.MediaFile{}
	for _, f := range files {
		byMedia[f.MediaID] = append(byMedia[f.MediaID], f)
	}
	items := []misnamedMedia{}
	for _, m := range media {
		plan, ok := plannedRename(m.Media, byMedia[m.ID], format)
		if !ok {
			continue
		}
		items = append(items, misnamedMedia{
			ID: m.ID, Title: m.Title, Year: m.Year, Category: m.Category, Plan: plan,
		})
	}
	return items, nil
}

// plannedRename works out what an item's folder and files should be called, returning the
// changes and whether there are any. ok is false when the item is already correctly named,
// when there is nothing to name it after, or when the rename could not be carried out
// safely - so a plan that exists is always one that can be applied as a whole.
func plannedRename(m db.Media, files []db.MediaFile, format string) (renamePlan, bool) {
	// An item with no year would only ever be renamed to "(0) Title", which is the defect
	// rather than the fix; it belongs on the no-metadata list until the year is known.
	if m.Title == "" || m.Year == 0 || len(files) == 0 {
		return renamePlan{}, false
	}
	plan := renamePlan{Folder: nameChange{
		From: filepath.Base(m.Path),
		To:   mediafmt.FolderName(format, m.Year, m.Title),
	}}

	// Only the video files are re-derived. Every other file in the folder follows the base
	// name of the video it belongs to (see below), so the optimized copies, the subtitle
	// sidecars and the VobSub pairs are carried along without naming each convention here.
	bases := map[string]string{} // video base name without extension: old -> new
	for _, f := range files {
		part := mediafmt.PartFromName(f.Name)
		if part == 0 && len(files) > 1 && f.Season == 0 && f.Episode == 0 {
			part = f.Idx + 1 // numbered parts of one film, which the cache does not record
		}
		to := mediafmt.FileName(format, m.Year, m.Title, f.Season, f.Episode, part, f.Ext)
		bases[trimExt(f.Name)] = trimExt(to)
		if to == f.Name {
			continue
		}
		// A file the cache lists but disk no longer has is a health issue, not a rename. The
		// same guard makes a rename interrupted part way through resumable: the files already
		// moved simply drop out of the plan instead of colliding with their own new names.
		if _, err := os.Lstat(filepath.Join(m.Path, f.Name)); err != nil {
			continue
		}
		plan.Files = append(plan.Files, nameChange{From: f.Name, To: to})
	}
	if plan.Folder.From == plan.Folder.To && len(plan.Files) == 0 {
		return renamePlan{}, false
	}

	entries, err := os.ReadDir(m.Path)
	if err != nil {
		return renamePlan{}, false
	}
	isVideo := make(map[string]bool, len(files))
	for _, f := range files {
		isVideo[f.Name] = true
	}
	// Longest base first: "X - 1x1" is a prefix of "X - 1x10", so a shorter base must never
	// claim a longer one's sidecars. The trailing dot the match requires rules that out too.
	oldBases := make([]string, 0, len(bases))
	for b := range bases {
		oldBases = append(oldBases, b)
	}
	sort.Slice(oldBases, func(i, j int) bool { return len(oldBases[i]) > len(oldBases[j]) })
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || isVideo[name] {
			continue
		}
		for _, b := range oldBases {
			if !strings.HasPrefix(name, b+".") {
				continue
			}
			if to := bases[b] + strings.TrimPrefix(name, b); to != name {
				plan.Files = append(plan.Files, nameChange{From: name, To: to})
			}
			break
		}
	}

	// Refuse rather than clobber: every target must be free, and no two may be the same.
	// Checking here means an applied plan can never be left half done.
	taken := map[string]bool{}
	for _, c := range plan.Files {
		if taken[c.To] {
			return renamePlan{}, false
		}
		taken[c.To] = true
		if _, err := os.Lstat(filepath.Join(m.Path, c.To)); err == nil {
			return renamePlan{}, false
		}
	}
	if plan.Folder.From != plan.Folder.To {
		if _, err := os.Lstat(filepath.Join(filepath.Dir(m.Path), plan.Folder.To)); err == nil {
			return renamePlan{}, false
		}
	}
	sort.Slice(plan.Files, func(i, j int) bool { return naturalLess(plan.Files[i].From, plan.Files[j].From) })
	return plan, true
}

// trimExt drops a file name's extension, leaving the base the sidecars are named after.
func trimExt(name string) string { return strings.TrimSuffix(name, filepath.Ext(name)) }
