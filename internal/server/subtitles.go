package server

import (
	"context"
	"net/http"
	"time"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/logging"
)

// subtitleExtractTimeout caps one file's extraction. Demuxing a text track is fast even on
// a long film, so this is not a budget but a stop: without it a single pathological file
// would hang ffmpeg forever and, with it, the sweep that holds the guard - leaving the
// agent wedged and every manual sweep refused until a restart.
const subtitleExtractTimeout = 5 * time.Minute

// repairRequestTimeout caps a whole folder's on-demand repair, so a pathological item cannot
// leave a detached goroutine running for the process's lifetime.
const repairRequestTimeout = 30 * time.Minute

// subtitleTools returns the ffmpeg/ffprobe binaries and the configured subtitle language
// under lock - everything the sidecar repair needs from live settings.
func (s *Server) subtitleTools() (ffmpeg, ffprobe, lang string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return "", "", ""
	}
	return s.cfg.FFmpeg(), s.cfg.FFprobe(), s.cfg.SubLang()
}

// repairSubtitles externalises the embedded subtitle tracks of the media's video files
// that carry no ".srt" sidecar, so a release whose subtitles exist only inside the
// container still shows them in the player (it renders sidecars, never embedded tracks).
//
// The gate is deliberately cheap and stateless: a file that already has a sidecar is
// skipped on a directory read alone, so a swept library costs nothing and no column is
// needed to remember which files were looked at. The price is that a file with no
// subtitles anywhere is probed once per rotation - which is also what lets a track added
// to a file later still be found. Best-effort throughout, like every other repair the
// sweep does. Returns how many sidecars were written.
func (s *Server) repairSubtitles(ctx context.Context, files []db.MediaFile) int {
	ffmpeg, ffprobeBin, lang := s.subtitleTools()
	if ffmpeg == "" { // no config yet: both paths default to PATH once there is one
		return 0
	}
	written := 0
	for _, f := range files {
		if hasSRTSidecar(f.Path) {
			continue
		}
		fileCtx, cancel := context.WithTimeout(ctx, subtitleExtractTimeout)
		written += importer.ExtractEmbeddedSubtitles(fileCtx, f.Path, ffmpeg, ffprobeBin, lang)
		cancel()
	}
	return written
}

// hasSRTSidecar reports whether the video already has an SRT sidecar beside it. Other
// sidecar formats do not count: the player only renders SRT, so a folder holding just a
// bitmap ".sup" still has subtitles worth externalising.
func hasSRTSidecar(videoPath string) bool {
	for _, sub := range importer.FindSidecarSubtitles(videoPath) {
		if sub.Ext == ".srt" {
			return true
		}
	}
	return false
}

// handleRepairSubtitles runs the sidecar repair over one media folder on demand. The sweep
// gets there eventually, but it walks the library least-recently-checked first, so a folder
// just noticed to be missing its subtitles can be most of a rotation away; this is the
// "fix this one now" the admin detail page needs.
//
// The work is detached from the request: a folder of thirty episodes takes longer than a
// proxy is willing to hold a connection open, and a disconnect must not kill ffmpeg
// halfway. The response still waits for it, so the admin gets a real count.
func (s *Server) handleRepairSubtitles(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	m, err := db.GetMedia(r.Context(), pool, id)
	if err != nil {
		http.Error(w, "media not found", http.StatusNotFound)
		return
	}
	files, err := db.MediaFiles(r.Context(), pool, id)
	if err != nil {
		http.Error(w, "could not read the media files", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), repairRequestTimeout)
	defer cancel()
	written := s.repairSubtitles(ctx, files)
	s.logSubtitleRepair(m.Title, id, written)
	writeJSON(w, struct {
		Written int `json:"written"`
	}{written})
}

// logSubtitleRepair reports a repaired folder, one line per media item that gained
// sidecars and nothing at all when there was no work.
func (s *Server) logSubtitleRepair(title string, id string, written int) {
	if written == 0 {
		return
	}
	s.dlog().Info("extracted embedded subtitles for "+title,
		logging.Fields{"id": id, "count": written})
}
