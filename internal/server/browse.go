package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Server) installed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg != nil
}

// appDir is the installer's default browse location: the directory the app was
// started from, so the data folder defaults to the current path.
func appDir() string {
	if wd, err := os.Getwd(); err == nil && wd != "" {
		return wd
	}
	return "/"
}

// homeOrRoot is the import browser's default location.
func homeOrRoot() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "/"
}

type browseEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

// handleBrowse lists the subdirectories of a path so the installer can pick a data
// folder. Install-mode only (no auth), like the other /api/install endpoints.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if s.installed() {
		http.Error(w, "already installed", http.StatusConflict)
		return
	}
	writeBrowse(w, r, appDir(), false)
}

// handleAdminBrowse lets an admin walk the server filesystem to pick an import
// source. Files are listed too when ?files=true (file imports); otherwise only
// directories (folder imports).
func (s *Server) handleAdminBrowse(w http.ResponseWriter, r *http.Request) {
	writeBrowse(w, r, homeOrRoot(), r.URL.Query().Get("files") == "true")
}

// writeBrowse resolves the requested path (defaulting to def) and writes the listing.
func writeBrowse(w http.ResponseWriter, r *http.Request, def string, includeFiles bool) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = def
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}
	infos, err := os.ReadDir(path)
	if err != nil {
		// A generic message: the raw error would echo the absolute path and OS state back to
		// the caller (and this route is unauthenticated in install mode).
		http.Error(w, "could not read the folder", http.StatusBadRequest)
		return
	}
	entries := []browseEntry{}
	for _, e := range infos {
		if !e.IsDir() && !includeFiles {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") { // hide dotfiles/dotfolders to keep the list short
			continue
		}
		entries = append(entries, browseEntry{Name: e.Name(), Path: filepath.Join(path, e.Name()), IsDir: e.IsDir()})
	}
	// Directories first, each group alphabetical.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	parent := ""
	if path != "/" {
		parent = filepath.Dir(path)
	}
	writeJSON(w, struct {
		Path    string        `json:"path"`
		Parent  string        `json:"parent"`
		Entries []browseEntry `json:"entries"`
	}{path, parent, entries})
}
