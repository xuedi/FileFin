package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
)

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.store.Categories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, cats)
}

func (s *Server) handleCategoryMedia(w http.ResponseWriter, r *http.Request) {
	media, err := s.store.MediaByCategory(r.PathValue("cat"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, media)
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.MediaDetail(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, d)
}

func (s *Server) handlePoster(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.PosterPath(r.PathValue("id"))
	if err != nil || p == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, p)
}

func (s *Server) handleBanner(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.BannerPath(r.PathValue("id"))
	if err != nil || p == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, p)
}

// handleStream serves a media file with byte-range support (direct play).
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.Error(w, "bad file index", http.StatusBadRequest)
		return
	}
	p, err := s.store.FilePath(r.PathValue("id"), n)
	if err != nil || p == "" {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}
