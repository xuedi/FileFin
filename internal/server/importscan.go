package server

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/library"
	"filefin/internal/logging"
	"filefin/internal/recognize"
)

// importItem is one recognisable media sitting in the import folder: every video file of one
// entry that belongs to the same media, folded into a single line for the import page. It is
// derived from the filesystem on every read and never stored - the page is a preview of what
// an import would create, and nothing is written until the admin presses Import.
type importItem struct {
	ID         string   `json:"id"`    // stable across scans: derived from entry + recognised title/year
	Entry      string   `json:"entry"` // the top-level entry it lives in
	Dir        bool     `json:"dir"`
	Title      string   `json:"title"`
	Year       int      `json:"year"`
	IsShow     bool     `json:"isShow"`
	Confidence string   `json:"confidence"` // high, medium or low: how much to trust this row
	Doubts     []string `json:"doubts"`     // the sanity checks this media failed, in words
	Files      int      `json:"files"`
	Bytes      int64    `json:"bytes"`
	SubCount   int      `json:"subCount"`
	HasPoster  bool     `json:"hasPoster"`
	Duplicate  string   `json:"duplicate"` // the library item this would import a second time
	// CategoryID is the category this row's markers point at, 0 when nothing earned the
	// guess; CategoryReason says why, in the same spirit as the confidence tooltip.
	CategoryID     int64  `json:"categoryId"`
	CategoryReason string `json:"categoryReason"`
	paths          []string
	probes         []db.Import
}

// handleImportFolder previews the import folder: the configured path plus one item per
// recognised media, with the duplicate check already applied. An unconfigured import folder
// is an empty listing, not an error - the page explains the gap itself.
func (s *Server) handleImportFolder(w http.ResponseWriter, r *http.Request) {
	folder := s.importFolder()
	out := struct {
		Folder string       `json:"folder"`
		Items  []importItem `json:"items"`
	}{Folder: folder, Items: []importItem{}}
	if folder == "" {
		writeJSON(w, out)
		return
	}
	items, err := scanImportFolder(folder)
	if err != nil {
		http.Error(w, "could not read the import folder", http.StatusInternalServerError)
		return
	}
	if pool, err := s.ensureDB(r.Context()); err == nil {
		s.markDuplicateItems(r.Context(), pool, items)
	}
	s.predictCategories(items)
	out.Items = items
	writeJSON(w, out)
}

// handleImportFolderStart imports the picked media in one step: it re-scans the folder,
// matches each requested item by id, and stages its files straight as import rows (skipping
// preCheck - the page the admin just reviewed *is* the check) so the poller copies them on
// its next tick. An id that no longer resolves is skipped rather than failing the batch: the
// folder can have changed since the page was drawn.
func (s *Server) handleImportFolderStart(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		DeleteAfter bool `json:"deleteAfter"`
		PurgeFolder bool `json:"purgeFolder"`
		Items       []struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			Year       int    `json:"year"`
			CategoryID int64  `json:"categoryId"`
		} `json:"items"`
	}](w, r)
	if err != nil || len(req.Items) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	folder := s.importFolder()
	if folder == "" {
		http.Error(w, "no import folder configured", http.StatusBadRequest)
		return
	}
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	items, err := scanImportFolder(folder)
	if err != nil {
		http.Error(w, "could not read the import folder", http.StatusInternalServerError)
		return
	}
	byID := map[string]importItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	ctx := r.Context()
	staged, skipped := 0, 0
	for _, want := range req.Items {
		it, ok := byID[want.ID]
		if !ok {
			skipped++
			continue
		}
		cat, ok := s.categoryByID(want.CategoryID)
		if !ok {
			skipped++
			continue
		}
		title := strings.TrimSpace(want.Title)
		if title == "" {
			title = it.Title
		}
		n := s.stageItem(ctx, pool, it, cat.ID, title, want.Year, req.DeleteAfter)
		staged += n
		// The source name is about to be replaced by the canonical one, so this is the last
		// moment its markers exist. Learn once per media, not per file, or a long show would
		// drown every other signal; a row that staged nothing teaches nothing.
		if n > 0 {
			s.bestEffort(library.Learn(s.dataDir(), cat.Name, itemMarkers(it)), "learn category markers")
		}
	}
	if skipped > 0 {
		s.logger().For(logging.Import).Error("some media could not be staged for import",
			logging.Fields{"skipped": skipped, "requested": len(req.Items)})
	}
	// Emptying the folder only makes sense once the sources have been consumed, so it rides
	// on delete-after; the poller runs it when this batch has finished copying.
	if req.PurgeFolder && req.DeleteAfter && staged > 0 {
		s.purgeArmed.Store(true)
	}
	s.logger().For(logging.Import).Info("queued "+strconv.Itoa(staged)+" media file(s) for import",
		logging.Fields{"files": staged, "media": len(req.Items) - skipped})
	writeJSON(w, struct {
		Started int `json:"started"`
		Skipped int `json:"skipped"`
	}{staged, skipped})
}

// stageItem writes one import row per file of a recognised media, already in the import
// status so the poller picks them up without a second confirmation. The admin's title and
// year win over what recognition guessed; season and episode stay per file.
func (s *Server) stageItem(ctx context.Context, pool *sql.DB, it importItem, categoryID int64, title string, year int, deleteAfter bool) int {
	n := 0
	for i, path := range it.paths {
		subsJSON := ""
		if subs := importer.FindSidecarSubtitles(path); len(subs) > 0 {
			if b, err := json.Marshal(subs); err == nil {
				subsJSON = string(b)
			}
		}
		if _, err := db.InsertImport(ctx, pool, db.Import{
			CategoryID: categoryID, SourcePath: path, Filename: filepath.Base(path),
			Title: title, Year: year, Season: it.probes[i].Season, Episode: it.probes[i].Episode,
			Part:      it.probes[i].Part,
			Subtitles: subsJSON, Poster: importer.FindSidecarPoster(path),
			Status: db.StatusImport, DeleteAfter: deleteAfter, Origin: db.OriginFolder,
			Confidence: it.Confidence,
		}); err != nil {
			continue
		}
		n++
	}
	return n
}

// itemMarkers reads one media's learnable signals from the names it arrived under: the entry
// folder the admin dropped there and the first file inside it. Both are used because a
// release signs itself in either place - the folder wears the group, or only the files do.
func itemMarkers(it importItem) []string {
	names := []string{it.Entry}
	if len(it.paths) > 0 {
		names = append(names, filepath.Base(it.paths[0]))
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		for _, m := range recognize.Markers(n) {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

// scanImportFolder turns the import folder into the media it would create. It groups by
// **top-level entry**, because that is what the admin dropped there: a folder is one media
// unless its own subfolders say otherwise, and only loose files at the root are grouped by
// what they parse to. Grouping by parsed title instead would shatter an entry whenever two
// of its files disagree, which is exactly what real release names do.
func scanImportFolder(folder string) ([]importItem, error) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}
	items := []importItem{}
	var loose []videoFile
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(folder, name)
		if e.IsDir() {
			if recognize.SkipDir(name) {
				continue
			}
			items = append(items, itemsFromEntry(path, name)...)
			continue
		}
		if !videoExts[strings.ToLower(filepath.Ext(name))] ||
			isOptimizedSibling(name) || recognize.SkipFile(name) {
			continue
		}
		f := videoFile{path: path}
		if info, err := e.Info(); err == nil {
			f.size = info.Size()
		}
		loose = append(loose, f)
	}
	return append(items, itemsFromLooseFiles(loose)...), nil
}

// fileGroup is the files of one immediate subfolder of an entry (or of the entry root),
// already recognised, with the verdict that decides what the folder holds.
type fileGroup struct {
	dir     string // "" for files sitting directly in the entry
	files   []videoFile
	parsed  []recognize.Parsed
	isShow  bool // any file carries a season or episode marker
	ordinal bool // the names count films in a series ("Movie - 01"), not episodes
}

// itemsFromEntry turns one top-level directory into the media it holds. Its immediate
// subfolders are classified on their own: those carrying episodes fold into a single show,
// while a folder of films yields one media per file. An entry with no episodes anywhere is a
// film - one media, unless its subfolders each name a different film.
func itemsFromEntry(path, entry string) []importItem {
	groups := groupEntryFiles(path)
	if len(groups) == 0 {
		return nil
	}
	var shows, films []fileGroup
	for _, g := range groups {
		if g.isShow {
			shows = append(shows, g)
		} else {
			films = append(films, g)
		}
	}
	folderCand := recognize.Candidate{
		Source: recognize.FromFolder,
		Parsed: entryTitle(entry, groups),
	}
	var items []importItem
	if len(shows) > 0 {
		items = append(items, showItem(entry, folderCand, shows))
	}
	for _, g := range films {
		items = append(items, filmItems(entry, folderCand, g, len(shows) > 0)...)
	}
	return items
}

// groupEntryFiles walks an entry and buckets its videos by the immediate subfolder they sit
// in, recognising each file on the way. Files deeper than one level are attributed to their
// top subfolder: a release that nests further is still one run of episodes.
func groupEntryFiles(path string) []fileGroup {
	files, err := scanVideoFiles(path)
	if err != nil {
		return nil
	}
	byDir := map[string]*fileGroup{}
	var order []string
	for _, f := range files {
		rel, err := filepath.Rel(path, f.path)
		if err != nil {
			continue
		}
		dir := ""
		if i := strings.IndexRune(rel, filepath.Separator); i >= 0 {
			dir = rel[:i]
		}
		g, ok := byDir[dir]
		if !ok {
			g = &fileGroup{dir: dir}
			byDir[dir] = g
			order = append(order, dir)
		}
		p := recognize.FromPath(rel)
		g.files = append(g.files, f)
		g.parsed = append(g.parsed, p)
		g.isShow = g.isShow || p.IsShow
		g.ordinal = g.ordinal || recognize.IsMovieOrdinal(filepath.Base(rel)) ||
			recognize.IsMovieOrdinal(dir)
	}
	sort.Strings(order)
	out := make([]fileGroup, 0, len(order))
	for _, dir := range order {
		g := byDir[dir]
		if g.ordinal { // "InuYasha Movies 1-4" counts films, so its numbers are not episodes
			g.isShow = false
		}
		out = append(out, *g)
	}
	return out
}

// showItem folds every episode-bearing group of an entry into one media. Groups that are not
// season folders are numbered as consecutive seasons - the plainest folder name first, since
// a later season names itself with something extra ("InuYasha", then "InuYasha Kanketsu-hen").
// Files with no episode number of their own become specials in season 0.
func showItem(entry string, folder recognize.Candidate, groups []fileGroup) importItem {
	if len(groups) > 1 {
		sort.SliceStable(groups, func(i, j int) bool {
			ti, tj := groupTitle(groups[i]), groupTitle(groups[j])
			if len(ti) != len(tj) {
				return len(ti) < len(tj)
			}
			return groups[i].dir < groups[j].dir
		})
	}
	guessedSeasons := false
	special := 0
	var files []videoFile
	var parsed []recognize.Parsed
	for i, g := range groups {
		for j := range g.parsed {
			p := g.parsed[j]
			if len(groups) > 1 && !recognize.HasExplicitSeason(nameOf(g.files[j])) &&
				!recognize.IsSeasonDir(g.dir) {
				p.Season = i + 1
				guessedSeasons = true
			}
			if p.Episode == 0 { // an opening, an ending, an extra: a special, numbered in order
				special++
				p.Season, p.Episode = 0, special
			}
			files = append(files, g.files[j])
			parsed = append(parsed, p)
		}
	}
	cands := []recognize.Candidate{folder}
	cands = append(cands, fileCandidates(parsed)...)
	res := recognize.Best(cands, parsed, true)
	if guessedSeasons && res.Confidence == recognize.High {
		res.Confidence = recognize.Medium
		res.Failed = append(res.Failed, recognize.CheckSeasonGuess)
	}
	return buildItem(entry, true, res, files, parsed)
}

// filmItems turns a group with no episodes into media. Several files with no part numbers are
// several films when they sit in a folder of films, and one film split over discs when they
// carry "CD1"/"CD2"; a subfolder that names its own film titles it.
func filmItems(entry string, folder recognize.Candidate, g fileGroup, entryIsShow bool) []importItem {
	if g.ordinal || (g.dir != "" && len(g.files) > 1 && !sameFilm(g.parsed)) {
		out := make([]importItem, 0, len(g.files))
		for i := range g.files {
			res := recognize.Best([]recognize.Candidate{{Source: recognize.FromFile, Parsed: g.parsed[i]}},
				g.parsed[i:i+1], false)
			out = append(out, buildItem(entry, false, res, g.files[i:i+1], g.parsed[i:i+1]))
		}
		return out
	}
	cands := []recognize.Candidate{folder}
	if g.dir != "" {
		cands = append([]recognize.Candidate{{Source: recognize.FromSubfolder,
			Parsed: recognize.ParseFolder(g.dir)}}, cands...)
	}
	cands = append(cands, fileCandidates(g.parsed)...)
	// A film group living beside episodes cannot borrow the show's folder title.
	if entryIsShow && g.dir != "" {
		cands = cands[:1]
	}
	res := recognize.Best(cands, g.parsed, false)
	return []importItem{buildItem(entry, false, res, g.files, g.parsed)}
}

// itemsFromLooseFiles groups the videos lying directly in the import root by what they
// recognise to, so two files of one show merge into one media.
func itemsFromLooseFiles(files []videoFile) []importItem {
	idx := map[string]int{}
	var keys []string
	byKey := map[string][]int{}
	parsed := make([]recognize.Parsed, len(files))
	for i, f := range files {
		p := recognize.FromPath(filepath.Base(f.path))
		parsed[i] = p
		key := strings.ToLower(p.Title) + "\x00" + strconv.Itoa(p.Year)
		if _, ok := idx[key]; !ok {
			idx[key] = len(keys)
			keys = append(keys, key)
		}
		byKey[key] = append(byKey[key], i)
	}
	out := make([]importItem, 0, len(keys))
	for _, key := range keys {
		var group []videoFile
		var gp []recognize.Parsed
		isShow := false
		for _, i := range byKey[key] {
			group = append(group, files[i])
			gp = append(gp, parsed[i])
			isShow = isShow || parsed[i].IsShow
		}
		res := recognize.Best(fileCandidates(gp), gp, isShow)
		it := buildItem(filepath.Base(group[0].path), false, res, group, gp)
		it.IsShow = isShow
		out = append(out, it)
	}
	return out
}

// buildItem assembles the page row: the recognised identity plus what the import would carry
// along - the file count, the bytes, sidecar subtitles and a poster.
func buildItem(entry string, isShow bool, res recognize.Result, files []videoFile, parsed []recognize.Parsed) importItem {
	it := importItem{
		Entry: entry, Dir: len(files) > 0 && filepath.Base(files[0].path) != entry,
		Title: res.Parsed.Title, Year: res.Parsed.Year, IsShow: isShow,
		Confidence: string(res.Confidence),
	}
	for _, c := range res.Failed {
		it.Doubts = append(it.Doubts, string(c))
	}
	for i, f := range files {
		it.Files++
		it.Bytes += f.size
		it.SubCount += len(importer.FindSidecarSubtitles(f.path))
		if !it.HasPoster && importer.FindSidecarPoster(f.path) != "" {
			it.HasPoster = true
		}
		it.paths = append(it.paths, f.path)
		it.probes = append(it.probes, db.Import{Title: it.Title, Year: it.Year,
			Season: parsed[i].Season, Episode: parsed[i].Episode, Part: parsed[i].Part})
	}
	it.ID = groupID(entry + "\x00" + it.Title + "\x00" + strconv.Itoa(it.Year))
	return it
}

// entryTitle reads the entry folder name, then drops any release-group tag its own files
// wear in brackets - "Call of the Night LostYears" beside "[LostYears] Call of the Night ..."
// is the group's name stuck to the folder, not part of the title.
func entryTitle(entry string, groups []fileGroup) recognize.Parsed {
	p := recognize.ParseFolder(entry)
	tags := map[string]bool{}
	for _, g := range groups {
		for _, f := range g.files {
			for _, tag := range recognize.BracketTags(filepath.Base(f.path)) {
				tags[strings.ToLower(tag)] = true
			}
		}
	}
	words := strings.Fields(p.Title)
	for len(words) > 1 && tags[strings.ToLower(words[len(words)-1])] {
		words = words[:len(words)-1]
	}
	p.Title = strings.Join(words, " ")
	return p
}

// fileCandidates offers the titles the files themselves suggest, most common first.
func fileCandidates(parsed []recognize.Parsed) []recognize.Candidate {
	count := map[string]int{}
	first := map[string]recognize.Parsed{}
	var order []string
	for _, p := range parsed {
		if strings.TrimSpace(p.Title) == "" {
			continue
		}
		key := strings.ToLower(p.Title)
		if _, ok := first[key]; !ok {
			first[key] = p
			order = append(order, key)
		}
		count[key]++
	}
	sort.SliceStable(order, func(i, j int) bool { return count[order[i]] > count[order[j]] })
	out := make([]recognize.Candidate, 0, len(order))
	for _, key := range order {
		out = append(out, recognize.Candidate{Source: recognize.FromFile, Parsed: first[key]})
	}
	return out
}

// groupTitle names a group by its folder, falling back to what its files agree on.
func groupTitle(g fileGroup) string {
	if g.dir != "" {
		return recognize.ParseFolder(g.dir).Title
	}
	if len(g.parsed) > 0 {
		return g.parsed[0].Title
	}
	return ""
}

// sameFilm reports whether every file of a group tells the same film: one title, and either
// a single file or numbered parts.
func sameFilm(parsed []recognize.Parsed) bool {
	return recognize.TitlesAgree(parsed)
}

func nameOf(f videoFile) string { return filepath.Base(f.path) }

// groupID is a short stable hash of an item's identity, so the page can hand an item back
// for import without ever sending a filesystem path.
func groupID(key string) string {
	sum := sha1.Sum([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
}

// markDuplicateItems labels an item that the library already holds. A media counts as a
// duplicate only when *every* one of its files is already there, so a season pack whose first
// episodes are imported still reads as new work rather than a re-import.
func (s *Server) markDuplicateItems(ctx context.Context, pool *sql.DB, items []importItem) {
	var probes []db.Import
	for _, it := range items {
		probes = append(probes, it.probes...)
	}
	s.markDuplicates(ctx, pool, probes)
	at := 0
	for i := range items {
		all, label := true, ""
		for _, p := range probes[at : at+len(items[i].probes)] {
			if p.Duplicate == "" {
				all = false
				break
			}
			if label == "" {
				label = p.Duplicate
			}
		}
		if all {
			items[i].Duplicate = label
		}
		at += len(items[i].probes)
	}
}
