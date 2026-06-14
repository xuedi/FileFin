package server

import (
	"errors"
	"net/http"
	"strings"

	"filefin/internal/config"
	"filefin/internal/db"
	"filefin/internal/logging"
	"filefin/internal/mdl"
	"filefin/internal/state"
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
	saveErr := config.Save(s.cfg)
	s.mu.Unlock()
	if saveErr != nil {
		http.Error(w, "could not write config", http.StatusInternalServerError)
		return
	}
	writeJSON(w, authResult{User: user, Admin: u.Admin, Alias: u.Alias, MDLUsername: u.MDLUsername})
}

// mdlMatch is one proposed import row in a preview.
type mdlMatch struct {
	MediaID         string `json:"mediaId"`
	LibraryTitle    string `json:"libraryTitle"`
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
// library, returning a reviewable proposal. It writes nothing.
func (s *Server) handleMDLPreview(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	s.mu.RLock()
	u := s.cfg.Users[user]
	s.mu.RUnlock()
	name := strings.TrimSpace(u.MDLUsername)
	if name == "" {
		http.Error(w, "no MyDramaList username set", http.StatusBadRequest)
		return
	}
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	media, err := db.AllMedia(ctx, pool)
	if err != nil {
		http.Error(w, "could not read library", http.StatusInternalServerError)
		return
	}
	lib := make([]mdl.LibraryItem, len(media))
	for i, m := range media {
		lib[i] = mdl.LibraryItem{ID: m.ID, Title: m.Title, Year: m.Year}
	}

	entries, err := mdl.New().GetUserList(ctx, name)
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

	out := mdlPreview{Matched: []mdlMatch{}, Unmatched: []string{}, Total: len(entries)}
	for _, m := range mdl.MatchLibrary(entries, lib) {
		if m.Item == nil {
			out.Unmatched = append(out.Unmatched, m.Entry.Title)
			continue
		}
		out.Matched = append(out.Matched, mdlMatch{
			MediaID:         m.Item.ID,
			LibraryTitle:    m.Item.Title,
			MDLTitle:        m.Entry.Title,
			Year:            m.Entry.Year,
			Rating:          m.Entry.Rating,
			WillMarkWatched: m.Entry.Watched(),
			Exact:           m.Exact,
		})
	}
	writeJSON(w, out)
}

// handleMDLApply writes the confirmed subset of a preview: a 1-10 rating and, when asked,
// the watched flag, onto each item's per-user state.
func (s *Server) handleMDLApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Items []struct {
			MediaID     string `json:"mediaId"`
			Rating      int    `json:"rating"`
			MarkWatched bool   `json:"markWatched"`
		} `json:"items"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	pool, ok := s.userPool(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	user := userFrom(r)
	applied, failed := 0, 0
	for _, it := range req.Items {
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
	s.logger().For(logging.Frontend).Info(user+" imported MyDramaList ratings",
		logging.Fields{"applied": applied, "failed": failed})
	writeJSON(w, struct {
		Applied int `json:"applied"`
		Failed  int `json:"failed"`
	}{applied, failed})
}
