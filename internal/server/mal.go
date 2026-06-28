package server

import (
	"errors"
	"net/http"
	"strings"

	"filefin/internal/config"
	"filefin/internal/logging"
	"filefin/internal/mal"
	"filefin/internal/watchlist"
)

// handleMALProfile saves the calling user's own MyAnimeList username (a per-user profile
// field, so this is auth-gated, not admin-gated).
func (s *Server) handleMALProfile(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		MALUsername string `json:"malUsername"`
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
	u.MALUsername = strings.TrimSpace(req.MALUsername)
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

// malMatch is one proposed import row in a MyAnimeList preview.
type malMatch struct {
	MediaID         string `json:"mediaId"`
	LibraryTitle    string `json:"libraryTitle"`
	LibraryYear     int    `json:"libraryYear"`
	SourceTitle     string `json:"sourceTitle"`
	Year            int    `json:"year"`
	Rating          int    `json:"rating"`
	WillMarkWatched bool   `json:"willMarkWatched"`
	Exact           bool   `json:"exact"`
}

// malPreview is the preview response: matched proposals plus the titles with no library item.
type malPreview struct {
	Matched   []malMatch `json:"matched"`
	Unmatched []string   `json:"unmatched"`
	Total     int        `json:"total"`
}

// handleMALPreview fetches the user's public MyAnimeList list through the official API and
// matches it against the library (optionally scoped to a category subtree). It writes nothing.
func (s *Server) handleMALPreview(w http.ResponseWriter, r *http.Request) {
	categoryID, ok := previewCategoryID(w, r)
	if !ok {
		return
	}
	user := userFrom(r)
	s.mu.RLock()
	u := s.cfg.Users[user]
	clientID := s.cfg.MALClientID
	s.mu.RUnlock()
	name := strings.TrimSpace(u.MALUsername)
	if name == "" {
		http.Error(w, "no MyAnimeList username set", http.StatusBadRequest)
		return
	}
	if clientID == "" {
		http.Error(w, "MyAnimeList client ID is not configured", http.StatusBadRequest)
		return
	}
	lib, ok := s.watchlistLibrary(w, r, categoryID)
	if !ok {
		return
	}

	entries, err := mal.New(clientID).GetUserList(r.Context(), name)
	if err != nil {
		switch {
		case errors.Is(err, mal.ErrNotFound):
			http.Error(w, "MyAnimeList user not found", http.StatusNotFound)
		case errors.Is(err, mal.ErrEmpty):
			http.Error(w, "that MyAnimeList list is empty or private", http.StatusNotFound)
		case errors.Is(err, mal.ErrUnauthorized):
			http.Error(w, "MyAnimeList rejected the client ID", http.StatusBadGateway)
		case errors.Is(err, mal.ErrNotConfigured):
			http.Error(w, "MyAnimeList client ID is not configured", http.StatusBadRequest)
		default:
			s.logger().For(logging.Frontend).Error("mal preview for "+user+" failed",
				logging.Fields{"malUser": name, "error": err.Error()})
			http.Error(w, "could not reach MyAnimeList", http.StatusBadGateway)
		}
		return
	}

	out := malPreview{Matched: []malMatch{}, Unmatched: []string{}, Total: len(entries)}
	for _, m := range watchlist.MatchLibrary(entries, lib) {
		if m.Item == nil {
			out.Unmatched = append(out.Unmatched, m.Entry.Title)
			continue
		}
		out.Matched = append(out.Matched, malMatch{
			MediaID:         m.Item.ID,
			LibraryTitle:    m.Item.Title,
			LibraryYear:     m.LibraryYear,
			SourceTitle:     m.Entry.Title,
			Year:            m.Entry.Year,
			Rating:          m.Entry.Rating,
			WillMarkWatched: m.Entry.Watched,
			Exact:           m.Exact,
		})
	}
	writeJSON(w, out)
}

// handleMALApply writes the confirmed subset of a MyAnimeList preview onto each item's
// per-user state, through the same shared path as every other watch-history importer.
func (s *Server) handleMALApply(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Items []watchApplyItem `json:"items"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.applyWatchImport(w, r, "MyAnimeList", req.Items)
}
