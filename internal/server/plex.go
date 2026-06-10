package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/library"
	"filefin/internal/logging"
	"filefin/internal/plex"
)

// plexSampleSize is how many files per library the resolver probes.
const plexSampleSize = 10

// plexDefaultDBPaths are the standard Plex database locations probed by /plex/default.
func plexDefaultDBPaths() []string {
	const dbRel = "Plug-in Support/Databases/com.plexapp.plugins.library.db"
	paths := []string{
		filepath.Join("/var/lib/plex/Plex Media Server", dbRel),
		filepath.Join("/var/lib/plexmediaserver/Library/Application Support/Plex Media Server", dbRel),
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths,
			filepath.Join(home, ".local/share/Plex Media Server", dbRel),
			filepath.Join(home, "Library/Application Support/Plex Media Server", dbRel),
		)
	}
	return paths
}

// handlePlexDefault returns the first standard Plex database path that exists, or "".
func (s *Server) handlePlexDefault(w http.ResponseWriter, r *http.Request) {
	for _, p := range plexDefaultDBPaths() {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			writeJSON(w, map[string]string{"dbPath": p})
			return
		}
	}
	writeJSON(w, map[string]string{"dbPath": ""})
}

// metadataDirOr returns the request's metadata dir, defaulting to the derived one.
func metadataDirOr(dbPath, metadataDir string) string {
	if strings.TrimSpace(metadataDir) != "" {
		return metadataDir
	}
	return plex.DeriveMetadataDir(dbPath)
}

// handlePlexCheck opens the Plex database read-only and returns its libraries. It
// stages nothing.
func (s *Server) handlePlexCheck(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		DBPath      string `json:"dbPath"`
		MetadataDir string `json:"metadataDir"`
	}](w, r)
	if err != nil || strings.TrimSpace(req.DBPath) == "" {
		http.Error(w, "a Plex database path is required", http.StatusBadRequest)
		return
	}
	d, err := plex.Open(req.DBPath, metadataDirOr(req.DBPath, req.MetadataDir))
	if err != nil {
		http.Error(w, "could not open the Plex database", http.StatusBadRequest)
		return
	}
	defer d.Close()
	secs, err := d.Sections(r.Context())
	if err != nil {
		http.Error(w, "could not read the Plex libraries", http.StatusInternalServerError)
		return
	}
	if secs == nil {
		secs = []plex.Section{}
	}
	writeJSON(w, secs)
}

// plexResolution is one library's path-resolution result for the frontend.
type plexResolution struct {
	Section string `json:"section"`
	Status  string `json:"status"`
	From    string `json:"from"`
	To      string `json:"to"`
	Found   int    `json:"found"`
	Total   int    `json:"total"`
}

// handlePlexResolve probes each selected library and reports how its DB paths map
// onto the filesystem. With no searchBase only the as-is check runs (a co-located
// install goes green with zero input); with one it auto-detects a remap and verifies.
func (s *Server) handlePlexResolve(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		DBPath      string   `json:"dbPath"`
		MetadataDir string   `json:"metadataDir"`
		Sections    []string `json:"sections"`
		SearchBase  string   `json:"searchBase"`
	}](w, r)
	if err != nil || strings.TrimSpace(req.DBPath) == "" {
		http.Error(w, "a Plex database path is required", http.StatusBadRequest)
		return
	}
	d, err := plex.Open(req.DBPath, "")
	if err != nil {
		http.Error(w, "could not open the Plex database", http.StatusBadRequest)
		return
	}
	defer d.Close()
	out := []plexResolution{}
	for _, sec := range req.Sections {
		samples, err := d.SampleFiles(r.Context(), []string{sec}, plexSampleSize)
		if err != nil {
			http.Error(w, "could not sample the Plex library", http.StatusInternalServerError)
			return
		}
		res := plex.Resolve(samples, strings.TrimSpace(req.SearchBase))
		out = append(out, plexResolution{
			Section: sec, Status: res.Status,
			From: res.Remap.From, To: res.Remap.To, Found: res.Found, Total: res.Total,
		})
	}
	writeJSON(w, out)
}

// plexPrepareReq is the staging request, copied into the background goroutine.
type plexPrepareReq struct {
	DBPath      string `json:"dbPath"`
	MetadataDir string `json:"metadataDir"`
	Selections  []struct {
		Section    string `json:"section"`
		CategoryID int64  `json:"categoryId"`
		Create     bool   `json:"create"`
	} `json:"selections"`
	Remaps []struct {
		Section string `json:"section"`
		From    string `json:"from"`
		To      string `json:"to"`
	} `json:"remaps"`
}

// handlePlexPrepare starts the single background staging job and returns immediately.
// The job walks the selected libraries, applies the resolved remap, and writes a
// preCheck row per locatable file; the frontend polls /plex/progress and redirects
// to the preCheck page when it finishes.
func (s *Server) handlePlexPrepare(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[plexPrepareReq](w, r)
	if err != nil || strings.TrimSpace(req.DBPath) == "" {
		http.Error(w, "a Plex database path is required", http.StatusBadRequest)
		return
	}
	if len(req.Selections) == 0 {
		http.Error(w, "select at least one library", http.StatusBadRequest)
		return
	}
	if !s.plexStaging.begin() {
		http.Error(w, "a Plex import is already in progress", http.StatusConflict)
		return
	}

	go s.runPlexStaging(req)
	w.WriteHeader(http.StatusAccepted)
}

// handlePlexProgress returns the live staging job state for the polling frontend.
func (s *Server) handlePlexProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.plexStaging.snapshot())
}

// runPlexStaging is the background staging walk. It resolves target categories (creating
// them from the Plex library name when asked), applies each library's path remap and
// resolves subtitles into source-neutral staged items, then hands them to the shared
// stageImport driver.
func (s *Server) runPlexStaging(req plexPrepareReq) {
	ctx := context.Background()
	finishErr := func(msg string) {
		s.plexStaging.fail(msg)
		s.logger().For(logging.Import).Error("Plex import staging failed", logging.Fields{"error": msg})
	}

	pool, err := s.ensureDB(ctx)
	if err != nil {
		finishErr("cache unavailable")
		return
	}

	remaps := map[string]plex.Remap{}
	for _, rm := range req.Remaps {
		remaps[rm.Section] = plex.Remap{From: rm.From, To: rm.To}
	}

	// Resolve a target category per selected library (create from the Plex name on ask).
	cats := map[string]library.Category{}
	for _, sel := range req.Selections {
		if sel.Create {
			cat, err := s.createCategoryFromName(ctx, pool, sel.Section)
			if err != nil {
				finishErr("could not create category for " + sel.Section + ": " + err.Error())
				return
			}
			cats[sel.Section] = cat
		} else {
			cat, ok := s.categoryByID(sel.CategoryID)
			if !ok {
				finishErr("unknown category for " + sel.Section)
				return
			}
			cats[sel.Section] = cat
		}
	}

	d, err := plex.Open(req.DBPath, metadataDirOr(req.DBPath, req.MetadataDir))
	if err != nil {
		finishErr("could not open the Plex database")
		return
	}
	defer d.Close()

	// Build the source-neutral item list: one entry per file, with the remap and
	// subtitle resolution (Plex's own responsibilities) already applied.
	var items []stagedItem
	for _, sel := range req.Selections {
		plexItems, err := d.Items(ctx, sel.Section)
		if err != nil {
			finishErr("could not read the Plex library " + sel.Section)
			return
		}
		cat, remap := cats[sel.Section], remaps[sel.Section]
		for _, it := range plexItems {
			blob := ""
			if b, err := json.Marshal(importer.MetaFromPlex(it)); err == nil {
				blob = string(b)
			}
			for _, f := range it.Files {
				items = append(items, stagedItem{
					categoryID: cat.ID,
					sourcePath: remap.Apply(f.Path),
					title:      it.Title, year: it.Year, season: f.Season, episode: f.Episode,
					subtitles: plexSubsJSON(f.Subtitles, remap), poster: it.PosterPath,
					metaBlob: blob,
				})
			}
		}
	}

	s.stageImport(ctx, pool, &s.plexStaging, items, db.OriginPlex, "Plex")
}

// plexSubsJSON applies the remap to each external subtitle, keeps the ones that
// exist on disk, and encodes them as the importer's Subtitle list (codec -> ext).
func plexSubsJSON(subs []plex.Subtitle, remap plex.Remap) string {
	var out []importer.Subtitle
	for _, sub := range subs {
		p := remap.Apply(sub.Path)
		if !fileExists(p) {
			continue
		}
		ext := ""
		if c := strings.ToLower(strings.TrimSpace(sub.Codec)); c != "" {
			ext = "." + strings.TrimPrefix(c, ".")
		} else {
			ext = strings.ToLower(filepath.Ext(p))
		}
		out = append(out, importer.Subtitle{Path: p, Language: sub.Language, Ext: ext})
	}
	if len(out) == 0 {
		return ""
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}

// createCategoryFromName creates (or reuses) a category folder named after a source
// library/section: a sanitized folder name, the original name as the alias. The id is
// minted by the cache and stored in config.json (the source of truth). Shared by the
// Plex and Jellyfin sources.
func (s *Server) createCategoryFromName(ctx context.Context, pool *sql.DB, section string) (library.Category, error) {
	name := safeFolderName(section)
	if err := library.ValidName(name); err != nil {
		return library.Category{}, err
	}
	dataDir := s.dataDir()
	if library.Exists(dataDir, name) {
		if cat, ok := s.categoryByName(name); ok {
			return cat, nil // idempotent: a re-prepare reuses the folder
		}
	}
	id, err := db.InsertCategory(ctx, pool, name, section, 0)
	if err != nil {
		return library.Category{}, err
	}
	cat, err := library.Create(dataDir, "", name, section, id)
	if err != nil {
		_ = db.DeleteCategory(ctx, pool, name)
		return library.Category{}, err
	}
	return cat, nil
}

// categoryByName returns the category with the given folder name from the filesystem.
func (s *Server) categoryByName(name string) (library.Category, bool) {
	cats, err := library.List(s.dataDir())
	if err != nil {
		return library.Category{}, false
	}
	for _, c := range cats {
		if c.Name == name {
			return c, true
		}
	}
	return library.Category{}, false
}

// safeFolderName turns a Plex library name into a valid single-component folder
// name: path separators become hyphens and control characters are dropped.
func safeFolderName(s string) string {
	s = strings.NewReplacer("/", "-", "\\", "-").Replace(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		if r == 0 || r < 0x20 {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}
