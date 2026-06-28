package server

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"filefin/internal/config"
	"filefin/internal/db"
	"filefin/internal/logging"
	"filefin/internal/mdl"
	"filefin/internal/state"
	"filefin/internal/watchlist"
)

// handleMDLProfile saves the calling user's own MyDramaList username (it is a per-user
// profile field, so this is auth-gated, not admin-gated).
func (s *Server) handleMDLProfile(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		MDLUsername string `json:"mdlUsername"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	user := userFrom(r)
	s.mu.Lock()
	u, ok := s.cfg.Users[user]
	if !ok {
		s.mu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	u.MDLUsername = strings.TrimSpace(req.MDLUsername)
	s.cfg.Users[user] = u
	malConfigured := s.cfg.MALClientID != ""
	saveErr := config.Save(s.cfg)
	s.mu.Unlock()
	if saveErr != nil {
		http.Error(w, "could not write config", http.StatusInternalServerError)
		return
	}
	writeJSON(w, authResultOf(user, u, malConfigured))
}

// mdlMatch is one proposed import row in a preview.
type mdlMatch struct {
	MediaID         string `json:"mediaId"`
	LibraryTitle    string `json:"libraryTitle"`
	LibraryYear     int    `json:"libraryYear"`
	MDLTitle        string `json:"mdlTitle"`
	Year            int    `json:"year"`
	Rating          int    `json:"rating"`
	WillMarkWatched bool   `json:"willMarkWatched"`
	Exact           bool   `json:"exact"`
}

// mdlPreview is the preview response: the matched proposals plus the MDL titles that found
// no library item.
type mdlPreview struct {
	Matched   []mdlMatch `json:"matched"`
	Unmatched []string   `json:"unmatched"`
	Total     int        `json:"total"`
}

// handleMDLPreview scrapes the user's public MyDramaList list and matches it against the
// library (optionally scoped to a category subtree), returning a reviewable proposal. It
// writes nothing.
func (s *Server) handleMDLPreview(w http.ResponseWriter, r *http.Request) {
	categoryID, ok := previewCategoryID(w, r)
	if !ok {
		return
	}
	user := userFrom(r)
	s.mu.RLock()
	u := s.cfg.Users[user]
	s.mu.RUnlock()
	name := strings.TrimSpace(u.MDLUsername)
	if name == "" {
		http.Error(w, "no MyDramaList username set", http.StatusBadRequest)
		return
	}
	lib, ok := s.watchlistLibrary(w, r, categoryID)
	if !ok {
		return
	}

	entries, err := mdl.New().GetUserList(r.Context(), name)
	if err != nil {
		switch {
		case errors.Is(err, mdl.ErrNotFound):
			http.Error(w, "MyDramaList user not found", http.StatusNotFound)
		case errors.Is(err, mdl.ErrEmpty):
			http.Error(w, "that MyDramaList list is empty or private", http.StatusNotFound)
		default:
			s.logger().For(logging.Frontend).Error("mdl preview for "+user+" failed",
				logging.Fields{"mdlUser": name, "error": err.Error()})
			http.Error(w, "could not reach MyDramaList", http.StatusBadGateway)
		}
		return
	}

	wl := make([]watchlist.Entry, len(entries))
	for i, e := range entries {
		wl[i] = e.ToWatchlist()
	}
	out := mdlPreview{Matched: []mdlMatch{}, Unmatched: []string{}, Total: len(wl)}
	for _, m := range watchlist.MatchLibrary(wl, lib) {
		if m.Item == nil {
			out.Unmatched = append(out.Unmatched, m.Entry.Title)
			continue
		}
		out.Matched = append(out.Matched, mdlMatch{
			MediaID:         m.Item.ID,
			LibraryTitle:    m.Item.Title,
			LibraryYear:     m.LibraryYear,
			MDLTitle:        m.Entry.Title,
			Year:            m.Entry.Year,
			Rating:          m.Entry.Rating,
			WillMarkWatched: m.Entry.Watched,
			Exact:           m.Exact,
		})
	}
	writeJSON(w, out)
}

// handleMDLApply writes the confirmed subset of a preview onto each item's per-user state.
func (s *Server) handleMDLApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Items []watchApplyItem `json:"items"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.applyWatchImport(w, r, "MyDramaList", req.Items)
}

// watchApplyItem is one confirmed import row shared by every watch-history importer.
type watchApplyItem struct {
	MediaID     string `json:"mediaId"`
	Rating      int    `json:"rating"`
	MarkWatched bool   `json:"markWatched"`
}

// previewCategoryID reads the optional category scope from a preview request body. An
// empty body (no scope) yields 0; a malformed body is a 400.
func previewCategoryID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	req, err := decodeJSON[struct {
		CategoryID int64 `json:"categoryId"`
	}](w, r)
	if err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return 0, false
	}
	return req.CategoryID, true
}

// watchlistLibrary returns the library items a preview matches against: a category subtree
// when categoryID > 0, otherwise the whole library.
func (s *Server) watchlistLibrary(w http.ResponseWriter, r *http.Request, categoryID int64) ([]watchlist.LibraryItem, bool) {
	pool, ok := s.userPool(w, r)
	if !ok {
		return nil, false
	}
	var (
		media []db.MediaSummary
		err   error
	)
	if categoryID > 0 {
		media, err = db.ListMediaInCategorySubtree(r.Context(), pool, categoryID)
	} else {
		media, err = db.AllMedia(r.Context(), pool)
	}
	if err != nil {
		http.Error(w, "could not read library", http.StatusInternalServerError)
		return nil, false
	}
	lib := make([]watchlist.LibraryItem, len(media))
	for i, m := range media {
		lib[i] = watchlist.LibraryItem{ID: m.ID, Title: m.Title, Year: m.Year}
	}
	return lib, true
}

// applyWatchImport writes the confirmed rows of any watch-history preview: a 1-10 rating
// and, when asked, the watched flag, through the same per-folder meta.json path every
// other state writer uses. Re-running is idempotent.
func (s *Server) applyWatchImport(w http.ResponseWriter, r *http.Request, source string, items []watchApplyItem) {
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	user := userFrom(r)
	applied, failed := 0, 0
	for _, it := range items {
		if it.Rating < 0 || it.Rating > 10 {
			failed++
			continue
		}
		folder, err := db.FolderPath(ctx, pool, it.MediaID)
		if err != nil || folder == "" {
			failed++
			continue
		}
		err = s.metaMgr.UpdateState(folder, user, func(us state.UserState) state.UserState {
			if it.Rating > 0 {
				us.Rating = it.Rating
			}
			if it.MarkWatched {
				us.Watched = true
			}
			return us
		})
		if err != nil {
			failed++
			continue
		}
		applied++
	}
	s.logger().For(logging.Frontend).Info(user+" imported "+source+" ratings",
		logging.Fields{"applied": applied, "failed": failed})
	writeJSON(w, struct {
		Applied int `json:"applied"`
		Failed  int `json:"failed"`
	}{applied, failed})
}
