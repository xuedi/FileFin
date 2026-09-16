package server

import (
	"context"

	"filefin/internal/db"
	"filefin/internal/importer"
	"filefin/internal/logging"
)

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
		written += importer.ExtractEmbeddedSubtitles(ctx, f.Path, ffmpeg, ffprobeBin, lang)
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

// logSubtitleRepair reports a repaired folder, one line per media item that gained
// sidecars and nothing at all when there was no work.
func (s *Server) logSubtitleRepair(title string, id string, written int) {
	if written == 0 {
		return
	}
	s.dlog().Info("extracted embedded subtitles for "+title,
		logging.Fields{"id": id, "count": written})
}
