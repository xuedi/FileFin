package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/logging"
	"filefin/internal/thumbnail"
)

// The admin metadata editor behind the library detail page's "Edit" button: read one item's
// raw meta.json fields, save admin edits back (a manual replace-mode write, preserving the
// ffprobe technical block and per-user state), and upload a replacement poster. The OMDb
// re-match flow (see rematch.go) stays reachable from the editor's "Match with the API" link.

// maxPosterBytes caps an uploaded poster. Posters are small images, so a generous ceiling
// guards against a giant body without rejecting anything legitimate.
const maxPosterBytes = 15 << 20

// knownMetaKeys are the meta.json metadata keys the editor surfaces as named fields; any
// other key (e.g. one a future importer adds) is preserved untouched on save.
var knownMetaKeys = map[string]bool{
	"release": true, "runtime": true, "language": true, "origin": true, "directedBy": true,
	"writtenBy": true, "contentRating": true, "awards": true, "boxOffice": true, "imdbID": true,
}

// knownRatingKeys are the rating keys the editor surfaces; other sources (e.g. "plex") are
// preserved untouched on save.
var knownRatingKeys = map[string]bool{"imdb": true, "rottenTomatoes": true, "metacritic": true}

// mediaMetaForm is the editable field set, shared by the read response and the save request.
type mediaMetaForm struct {
	Title          string   `json:"title"`
	Year           int      `json:"year"`
	Description    string   `json:"description"`
	Plot           string   `json:"plot"`
	Release        string   `json:"release"`
	Runtime        string   `json:"runtime"`
	Language       string   `json:"language"`
	Country        string   `json:"country"`
	Director       string   `json:"director"`
	Writer         string   `json:"writer"`
	ContentRating  string   `json:"contentRating"`
	Awards         string   `json:"awards"`
	BoxOffice      string   `json:"boxOffice"`
	ImdbID         string   `json:"imdbId"`
	Imdb           string   `json:"imdb"`
	RottenTomatoes string   `json:"rottenTomatoes"`
	Metacritic     string   `json:"metacritic"`
	Actors         []string `json:"actors"`
	Tags           []string `json:"tags"`
}

// mediaMetaEdit is the read response: the form fields plus the folder facts the page shows.
type mediaMetaEdit struct {
	ID        string `json:"id"`
	Folder    string `json:"folder"`
	Category  string `json:"category"`
	HasPoster bool   `json:"hasPoster"`
	mediaMetaForm
}

// handleMetaEdit returns one media item's raw editable metadata for the editor form.
func (s *Server) handleMetaEdit(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	m, err := db.GetMedia(r.Context(), pool, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cat, _ := db.CategoryName(r.Context(), pool, m.CategoryID)
	meta, _ := importer.ReadMeta(m.Path)
	out := mediaMetaEdit{
		ID: m.ID, Folder: filepath.Base(m.Path), Category: cat, HasPoster: m.Poster != "",
		mediaMetaForm: mediaMetaForm{
			Title: m.Title, Year: m.Year,
			Description: meta.Description, Plot: meta.Plot,
			Release: meta.Metadata["release"], Runtime: meta.Metadata["runtime"],
			Language: meta.Metadata["language"], Country: meta.Metadata["origin"],
			Director: meta.Metadata["directedBy"], Writer: meta.Metadata["writtenBy"],
			ContentRating: meta.Metadata["contentRating"], Awards: meta.Metadata["awards"],
			BoxOffice: meta.Metadata["boxOffice"], ImdbID: meta.Metadata["imdbID"],
			Imdb: meta.Ratings["imdb"], RottenTomatoes: meta.Ratings["rottenTomatoes"],
			Metacritic: meta.Ratings["metacritic"],
			Actors:     nonNil(meta.Actors), Tags: nonNil(meta.Tags),
		},
	}
	writeJSON(w, out)
}

// handleSaveMeta writes admin edits onto a media item: it replaces the descriptive meta.json
// fields (keeping the technical block, per-user state, and any unknown metadata/rating keys),
// flags the item enriched, refreshes the cache row, and clears any leftover enrich task.
func (s *Server) handleSaveMeta(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	m, err := db.GetMedia(r.Context(), pool, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	req, err := decodeJSON[mediaMetaForm](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = m.Title // a blank title falls back to the current one, never empties the row
	}
	year := req.Year
	meta, err := s.metaMgr.Update(m.Path, func(cur importer.Meta) importer.Meta {
		out := cur // preserves Technical, State, and Enriched's siblings by default
		out.Title, out.Year = title, year
		out.Description = strings.TrimSpace(req.Description)
		out.Plot = strings.TrimSpace(req.Plot)
		out.Metadata = applyMetaFields(cur.Metadata, req)
		out.Ratings = applyRatingFields(cur.Ratings, req)
		out.Actors = trimList(req.Actors)
		out.Tags = lowerList(req.Tags)
		out.Enriched = true
		return out
	})
	if err != nil {
		http.Error(w, "could not save the changes", http.StatusInternalServerError)
		return
	}
	if err := s.writeMediaCacheRow(r.Context(), pool, m.ID, title, year, meta, folderPoster(m.Path)); err != nil {
		http.Error(w, "could not save the changes", http.StatusInternalServerError)
		return
	}
	s.bestEffort(db.PruneEnrich(r.Context(), pool, id), "clear enrich task after manual edit")
	s.elog().Info(userFrom(r)+" edited "+title, logging.Fields{"id": id, "title": title, "year": year})
	w.WriteHeader(http.StatusNoContent)
}

// handleUploadPoster stores an uploaded image as the item's base poster, drops the previous
// base poster and its stale sized variants so the thumbnail agent rebuilds them, and points
// the cache row at the new file.
func (s *Server) handleUploadPoster(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	m, err := db.GetMedia(r.Context(), pool, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPosterBytes)
	file, _, err := formFile(r, "poster")
	if err != nil {
		http.Error(w, "expected an image upload", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil || len(data) == 0 {
		http.Error(w, "empty upload", http.StatusBadRequest)
		return
	}
	ext := imageExtFromCT(http.DetectContentType(data))
	if ext == "" {
		http.Error(w, "not an image (jpg, png, webp, or gif)", http.StatusBadRequest)
		return
	}
	old := folderPoster(m.Path)
	name := "poster" + ext
	if err := os.WriteFile(filepath.Join(m.Path, name), data, 0o644); err != nil {
		http.Error(w, "could not save the poster", http.StatusInternalServerError)
		return
	}
	if old != "" && old != name {
		_ = os.Remove(filepath.Join(m.Path, old))
	}
	_ = os.Remove(filepath.Join(m.Path, thumbnail.DetailName()))
	_ = os.Remove(filepath.Join(m.Path, thumbnail.TileName()))
	s.bestEffort(db.SetMediaPoster(r.Context(), pool, id, name), "set uploaded poster")
	s.elog().Info(userFrom(r)+" replaced poster for "+m.Title, logging.Fields{"id": id})
	writeJSON(w, struct {
		HasPoster bool `json:"hasPoster"`
	}{true})
}

// formFile parses the multipart body and returns the named file part.
func formFile(r *http.Request, name string) (io.ReadCloser, string, error) {
	if err := r.ParseMultipartForm(maxPosterBytes); err != nil {
		return nil, "", err
	}
	f, hdr, err := r.FormFile(name)
	if err != nil {
		return nil, "", err
	}
	return f, hdr.Filename, nil
}

// applyMetaFields rebuilds the metadata map from the form's named fields, preserving any key
// the editor does not surface. A map with no entries collapses to nil (omitempty).
func applyMetaFields(cur map[string]string, req mediaMetaForm) map[string]string {
	out := map[string]string{}
	for k, v := range cur {
		if !knownMetaKeys[k] && strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	set := func(key, val string) {
		if v := strings.TrimSpace(val); v != "" {
			out[key] = v
		}
	}
	set("release", req.Release)
	set("runtime", req.Runtime)
	set("language", req.Language)
	set("origin", req.Country)
	set("directedBy", req.Director)
	set("writtenBy", req.Writer)
	set("contentRating", req.ContentRating)
	set("awards", req.Awards)
	set("boxOffice", req.BoxOffice)
	set("imdbID", req.ImdbID)
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyRatingFields rebuilds the ratings map from the form's rating fields, preserving any
// rating source the editor does not surface (e.g. an import's "plex").
func applyRatingFields(cur map[string]string, req mediaMetaForm) map[string]string {
	out := map[string]string{}
	for k, v := range cur {
		if !knownRatingKeys[k] && strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	set := func(key, val string) {
		if v := strings.TrimSpace(val); v != "" {
			out[key] = v
		}
	}
	set("imdb", req.Imdb)
	set("rottenTomatoes", req.RottenTomatoes)
	set("metacritic", req.Metacritic)
	if len(out) == 0 {
		return nil
	}
	return out
}

// trimList trims each entry and drops the blanks.
func trimList(in []string) []string {
	out := []string{}
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// lowerList is trimList with each entry lowercased (genres/tags are stored lowercase).
func lowerList(in []string) []string {
	out := trimList(in)
	for i, v := range out {
		out[i] = strings.ToLower(v)
	}
	return out
}

// nonNil returns in, or an empty slice when in is nil, so the JSON encodes [] not null.
func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// imageExtFromCT maps a sniffed content type to the poster file extension, or "" when the
// bytes are not a supported image.
func imageExtFromCT(ct string) string {
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ""
	}
}
