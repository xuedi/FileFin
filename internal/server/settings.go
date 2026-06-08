package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"filefin/internal/config"
	"filefin/internal/db"
	"filefin/internal/logging"
	"filefin/internal/mediafmt"
)

type settingRow struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// settingsPayload is the read view of the app config: the chosen media format (empty
// until permanently selected) plus a flat name/value list for display.
func settingsPayload(cfg *config.Config) map[string]any {
	users := make([]string, 0, len(cfg.Users))
	for u := range cfg.Users {
		users = append(users, u)
	}
	sort.Strings(users)
	format := cfg.MediaFormat
	if format == "" {
		format = "(not selected)"
	}
	importFolder := cfg.ImportFolder
	if importFolder == "" {
		importFolder = "(not set)"
	}
	cachePath, _ := db.Path()
	omdbKey := cfg.OMDBKey
	if omdbKey == "" {
		omdbKey = "(not set)"
	}
	logLevel := cfg.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}
	logOutput := cfg.LogOutput
	if logOutput == "" {
		logOutput = "STDOUT"
	}
	transcodeEnabled := cfg.TranscodeOn()
	transcodeValue := "on"
	if !transcodeEnabled {
		transcodeValue = "off"
	}
	optimizeMode := cfg.OptimizeModeOr()
	rows := []settingRow{
		{"Port", fmt.Sprintf("%d", cfg.Port)},
		{"Data folder", cfg.DataDir},
		{"Import folder", importFolder},
		{"Cache", "SQLite (" + cachePath + ")"},
		{"Users", strings.Join(users, ", ")},
		{"Media format", format},
		{"OMDb API key", omdbKey},
		{"Log level", logLevel},
		{"Log output", logOutput},
		{"Transcoding", transcodeValue},
		{"ffmpeg path", cfg.FFmpeg()},
		{"ffprobe path", cfg.FFprobe()},
		{"Subtitle language", cfg.SubLang()},
		{"Optimizer", optimizeMode},
	}
	return map[string]any{
		"mediaFormat":      cfg.MediaFormat,
		"importFolder":     cfg.ImportFolder,
		"omdbKey":          cfg.OMDBKey,
		"logLevel":         logLevel,
		"logOutput":        logOutput,
		"transcodeEnabled": transcodeEnabled,
		"ffmpegPath":       cfg.FFmpeg(),
		"ffprobePath":      cfg.FFprobe(),
		"subtitleLanguage": cfg.SubLang(),
		"optimizeMode":     optimizeMode,
		"settings":         rows,
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, settingsPayload(s.cfg))
}

// handleSetFormat permanently records the media-folder format. It can only be set
// once; a config that already has one is rejected.
func (s *Server) handleSetFormat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Format string `json:"format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !mediafmt.Valid(req.Format) {
		http.Error(w, "unknown media format", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.MediaFormat != "" {
		http.Error(w, "media format is already set and cannot be changed", http.StatusConflict)
		return
	}
	s.cfg.MediaFormat = req.Format
	if err := config.Save(s.cfg); err != nil {
		s.cfg.MediaFormat = "" // roll back the in-memory change on a failed write
		http.Error(w, "could not save settings", http.StatusInternalServerError)
		return
	}
	writeJSON(w, settingsPayload(s.cfg))
}

// handleSetImportFolder records the path media is imported from. Unlike the media
// format it is freely editable; it must be an existing absolute directory.
func (s *Server) handleSetImportFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	path := filepath.Clean(strings.TrimSpace(req.Path))
	if !filepath.IsAbs(path) {
		http.Error(w, "import folder must be an absolute path", http.StatusBadRequest)
		return
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		http.Error(w, "import folder must be an existing directory", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.cfg.ImportFolder
	s.cfg.ImportFolder = path
	if err := config.Save(s.cfg); err != nil {
		s.cfg.ImportFolder = prev
		http.Error(w, "could not save settings", http.StatusInternalServerError)
		return
	}
	writeJSON(w, settingsPayload(s.cfg))
}

// handleSetOMDBKey records the OMDb API key used for assessment enrichment. An empty
// key is allowed and disables enrichment.
func (s *Server) handleSetOMDBKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.cfg.OMDBKey
	s.cfg.OMDBKey = strings.TrimSpace(req.Key)
	if err := config.Save(s.cfg); err != nil {
		s.cfg.OMDBKey = prev
		http.Error(w, "could not save settings", http.StatusInternalServerError)
		return
	}
	writeJSON(w, settingsPayload(s.cfg))
}

// handleSetLogging updates the log level and output, persists them, and reconfigures
// the live logger so the change takes effect without a restart.
func (s *Server) handleSetLogging(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Level  string `json:"level"`
		Output string `json:"output"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	level := strings.TrimSpace(req.Level)
	if _, err := logging.ParseLevel(level); err != nil {
		http.Error(w, "log level must be error, info, or debug", http.StatusBadRequest)
		return
	}
	output := strings.TrimSpace(req.Output)
	switch strings.ToUpper(output) {
	case "", "STDOUT", "STDERR":
	default:
		if !filepath.IsAbs(output) {
			http.Error(w, "log output must be STDOUT, STDERR, or an absolute file path", http.StatusBadRequest)
			return
		}
		// Verify the path is writable before committing it, so a bad path is rejected
		// here rather than silently keeping the old destination at apply time.
		f, err := os.OpenFile(output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			http.Error(w, "log output path is not writable", http.StatusBadRequest)
			return
		}
		f.Close()
	}

	s.mu.Lock()
	prevLevel, prevOutput := s.cfg.LogLevel, s.cfg.LogOutput
	s.cfg.LogLevel, s.cfg.LogOutput = level, output
	cfg := s.cfg
	if err := config.Save(cfg); err != nil {
		s.cfg.LogLevel, s.cfg.LogOutput = prevLevel, prevOutput
		s.mu.Unlock()
		http.Error(w, "could not save settings", http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.configureLogger(cfg)
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, settingsPayload(s.cfg))
}

// handleSetTranscoding records the ffmpeg/ffprobe paths and the transcode toggle, then
// resets the live transcode manager so the new paths take effect without a restart.
func (s *Server) handleSetTranscoding(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FFmpegPath  string `json:"ffmpegPath"`
		FFprobePath string `json:"ffprobePath"`
		Enabled     bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	enabled := req.Enabled
	s.mu.Lock()
	prevFF, prevFP, prevEn := s.cfg.FFmpegPath, s.cfg.FFprobePath, s.cfg.TranscodeEnabled
	s.cfg.FFmpegPath = strings.TrimSpace(req.FFmpegPath)
	s.cfg.FFprobePath = strings.TrimSpace(req.FFprobePath)
	s.cfg.TranscodeEnabled = &enabled
	cfg := s.cfg
	if err := config.Save(cfg); err != nil {
		s.cfg.FFmpegPath, s.cfg.FFprobePath, s.cfg.TranscodeEnabled = prevFF, prevFP, prevEn
		s.mu.Unlock()
		http.Error(w, "could not save settings", http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.resetHLS()
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, settingsPayload(s.cfg))
}

// handleSetOptimizer records the background-optimizer mode and nudges the supervisor to
// relaunch its agents under the new mode without a restart.
func (s *Server) handleSetOptimizer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	mode := strings.TrimSpace(req.Mode)
	if !config.ValidOptimizeMode[mode] {
		http.Error(w, "optimizer mode must be none, cpu, gpu, or all", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	prev := s.cfg.OptimizeMode
	s.cfg.OptimizeMode = mode
	cfg := s.cfg
	if err := config.Save(cfg); err != nil {
		s.cfg.OptimizeMode = prev
		s.mu.Unlock()
		http.Error(w, "could not save settings", http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.signalReconfigOpt()
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, settingsPayload(s.cfg))
}

// handleSetSubtitleLanguage records the preferred sidecar subtitle language. An empty
// value falls back to the "en" default.
func (s *Server) handleSetSubtitleLanguage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	prev := s.cfg.SubtitleLanguage
	s.cfg.SubtitleLanguage = strings.TrimSpace(req.Language)
	if err := config.Save(s.cfg); err != nil {
		s.cfg.SubtitleLanguage = prev
		s.mu.Unlock()
		http.Error(w, "could not save settings", http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, settingsPayload(s.cfg))
}
