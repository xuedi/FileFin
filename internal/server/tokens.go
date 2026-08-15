package server

import (
	"net/http"

	"filefin/internal/config"
)

// tokenView is the wire shape of one personal access token, never carrying the secret.
type tokenView struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt,omitempty"`
}

func tokenViewOf(t config.PersonalToken) tokenView {
	return tokenView{ID: t.ID, Label: t.Label, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
}

// handleListTokens lists the caller's own personal access tokens (never the secret) - a
// per-user profile view, so auth-gated rather than admin-gated.
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	s.mu.RLock()
	u, ok := s.cfg.Users[user]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	views := make([]tokenView, 0, len(u.Tokens))
	for _, t := range u.Tokens {
		views = append(views, tokenViewOf(t))
	}
	writeJSON(w, views)
}

// handleCreateToken mints a personal access token for the caller and returns the raw secret -
// the only response that ever carries it. After this, only the label/created/last-used are
// visible, since the config stores just the token's hash.
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Label string `json:"label"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	secret, rec, err := config.NewPersonalToken(req.Label)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
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
	u.Tokens = append(u.Tokens, rec)
	s.cfg.Users[user] = u
	saveErr := config.Save(s.cfg)
	s.mu.Unlock()
	if saveErr != nil {
		http.Error(w, "could not write config", http.StatusInternalServerError)
		return
	}

	writeJSON(w, struct {
		tokenView
		Token string `json:"token"`
	}{tokenView: tokenViewOf(rec), Token: secret})
}

// handleRevokeToken removes one of the caller's own tokens by id; an id that isn't among the
// caller's tokens (including another user's) 404s rather than revealing anything about it.
func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user := userFrom(r)

	s.mu.Lock()
	u, ok := s.cfg.Users[user]
	if !ok {
		s.mu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	idx := -1
	for i, t := range u.Tokens {
		if t.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		s.mu.Unlock()
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	u.Tokens = append(u.Tokens[:idx], u.Tokens[idx+1:]...)
	s.cfg.Users[user] = u
	saveErr := config.Save(s.cfg)
	s.mu.Unlock()
	if saveErr != nil {
		http.Error(w, "could not write config", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
