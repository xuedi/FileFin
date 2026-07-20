package server

import (
	"net/http"
	"strings"

	"filefin/internal/db"
	"filefin/internal/library"
)

// misfiledMedia is one item whose looked-up language and country contradict the markers of
// the category it sits in. It is a report, never an action: the flag names the category it
// would suggest and leaves the move to the admin.
type misfiledMedia struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Year     int    `json:"year"`
	Category string `json:"category"`
	Language string `json:"language"`
	Country  string `json:"country"`
	Suggest  string `json:"suggest"` // the category the facets point at, empty when none does
}

// handleMisfiled lists the media whose metadata disagrees with where it was filed. Only a
// category that declares languages or countries can be contradicted - one that declares
// nothing has said nothing to be wrong about.
func (s *Server) handleMisfiled(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	media, err := db.ListEnrichedMedia(r.Context(), pool)
	if err != nil {
		http.Error(w, "could not list media", http.StatusInternalServerError)
		return
	}
	cats, err := library.List(s.dataDir())
	if err != nil {
		http.Error(w, "could not read categories", http.StatusInternalServerError)
		return
	}
	byID := make(map[int64]library.Category, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}
	items := []misfiledMedia{}
	for _, m := range media {
		cat, ok := byID[m.CategoryID]
		// A media the lookup recorded no origin for cannot contradict anything: that is a gap
		// in its metadata, which the page's other lists are about, not a filing mistake.
		if !ok || !declaresOrigin(cat.Markers) || !knowsOrigin(m) || originAgrees(cat.Markers, m) {
			continue
		}
		items = append(items, misfiledMedia{
			ID: m.ID, Title: m.Title, Year: m.Year, Category: cat.Alias,
			Language: m.Language, Country: m.Country, Suggest: suggestCategory(cats, cat, m),
		})
	}
	writeJSON(w, struct {
		Items []misfiledMedia `json:"items"`
	}{items})
}

// declaresOrigin reports whether a category says anything about where its media comes from.
func declaresOrigin(m library.Markers) bool {
	return len(m.Languages) > 0 || len(m.Countries) > 0
}

// knowsOrigin reports whether the lookup wrote anything about where a media comes from.
func knowsOrigin(m db.EnrichedMedia) bool {
	return strings.TrimSpace(m.Language) != "" || strings.TrimSpace(m.Country) != ""
}

// originAgrees reports whether a media's looked-up language or country matches what the
// category declares. A match in either dimension is enough: a Korean film shot in Japan is
// still a Korean film, and one dimension agreeing is no contradiction.
func originAgrees(m library.Markers, media db.EnrichedMedia) bool {
	return anyMatch(m.Languages, media.Language) || anyMatch(m.Countries, media.Country)
}

// anyMatch reports whether any declared value appears in a comma-separated facet the lookup
// wrote ("English, Korean").
func anyMatch(declared []string, facet string) bool {
	for _, part := range strings.Split(facet, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		for _, d := range declared {
			if strings.EqualFold(strings.TrimSpace(d), part) {
				return true
			}
		}
	}
	return false
}

// suggestCategory names the category whose declared origin the media actually matches, or
// nothing when no category claims it. The suggestion respects the kind markers, so a film is
// never pointed at a shows-only category.
func suggestCategory(cats []library.Category, cur library.Category, media db.EnrichedMedia) string {
	for _, c := range cats {
		if c.ID == cur.ID || !declaresOrigin(c.Markers) {
			continue
		}
		if c.Markers.Kind != "" && cur.Markers.Kind != "" && c.Markers.Kind != cur.Markers.Kind {
			continue
		}
		if originAgrees(c.Markers, media) {
			return c.Alias
		}
	}
	return ""
}
