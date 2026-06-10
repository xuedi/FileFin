package server

import (
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

// settingsView is the typed read view of the app config returned by every settings
// endpoint: the structured values the form edits plus a flat name/value list for display.
type settingsView struct {
	MediaFormat      string       `json:"mediaFormat"`
	ImportFolder     string       `json:"importFolder"`
	OMDBKey          string       `json:"omdbKey"`
	LogLevel         string       `json:"logLevel"`
	LogOutput        string       `json:"logOutput"`
	TranscodeEnabled bool         `json:"transcodeEnabled"`
	FFmpegPath       string       `json:"ffmpegPath"`
	FFprobePath      string       `json:"ffprobePath"`
	SubtitleLanguage string       `json:"subtitleLanguage"`
	OptimizeMode     string       `json:"optimizeMode"`
	Settings         []settingRow `json:"settings"`
}

// mutateConfig applies apply to a copy of the live config and persists it, publishing the
// copy only after a successful save - so a failed write needs no manual rollback (the
// live config was never touched). It returns the new config for rendering and post-save
// side effects, or false (after writing a 500) when the save fails. Published configs are
// never mutated in place, so the returned pointer is safe to read after the lock drops.
func (s *Server) mutateConfig(w http.ResponseWriter, apply func(*config.Config)) (*config.Config, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *s.cfg
	apply(&cp)
	if err := config.Save(&cp); err != nil {
		http.Error(w, "could not save settings", http.StatusInternalServerError)
		return nil, false
	}
	s.cfg = &cp
	return &cp, true
}

// settingsPayload is the read view of the app config: the chosen media format (empty
// until permanently selected) plus a flat name/value list for display.
func settingsPayload(cfg *config.Config) settingsView {
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
	return settingsView{
		MediaFormat:      cfg.MediaFormat,
		ImportFolder:     cfg.ImportFolder,
		OMDBKey:          cfg.OMDBKey,
		LogLevel:         logLevel,
		LogOutput:        logOutput,
		TranscodeEnabled: transcodeEnabled,
		FFmpegPath:       cfg.FFmpeg(),
		FFprobePath:      cfg.FFprobe(),
		SubtitleLanguage: cfg.SubLang(),
		OptimizeMode:     optimizeMode,
		Settings:         rows,
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
	req, err := decodeJSON[struct {
		Format string `json:"format"`
	}](w, r)
	if err != nil {
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
	req, err := decodeJSON[struct {
		Path string `json:"path"`
	}](w, r)
	if err != nil {
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
	cfg, ok := s.mutateConfig(w, func(c *config.Config) { c.ImportFolder = path })
	if !ok {
		return
	}
	writeJSON(w, settingsPayload(cfg))
}

// handleSetOMDBKey records the OMDb API key used for assessment enrichment. An empty
// key is allowed and disables enrichment.
func (s *Server) handleSetOMDBKey(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Key string `json:"key"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cfg, ok := s.mutateConfig(w, func(c *config.Config) { c.OMDBKey = strings.TrimSpace(req.Key) })
	if !ok {
		return
	}
	writeJSON(w, settingsPayload(cfg))
}

// handleSetLogging updates the log level and output, persists them, and reconfigures
// the live logger so the change takes effect without a restart.
func (s *Server) handleSetLogging(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Level  string `json:"level"`
		Output string `json:"output"`
	}](w, r)
	if err != nil {
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

	cfg, ok := s.mutateConfig(w, func(c *config.Config) {
		c.LogLevel, c.LogOutput = level, output
	})
	if !ok {
		return
	}
	s.configureLogger(cfg)
	writeJSON(w, settingsPayload(cfg))
}

// handleSetTranscoding records the ffmpeg/ffprobe paths and the transcode toggle, then
// resets the live transcode manager so the new paths take effect without a restart.
func (s *Server) handleSetTranscoding(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		FFmpegPath  string `json:"ffmpegPath"`
		FFprobePath string `json:"ffprobePath"`
		Enabled     bool   `json:"enabled"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	enabled := req.Enabled
	cfg, ok := s.mutateConfig(w, func(c *config.Config) {
		c.FFmpegPath = strings.TrimSpace(req.FFmpegPath)
		c.FFprobePath = strings.TrimSpace(req.FFprobePath)
		c.TranscodeEnabled = &enabled
	})
	if !ok {
		return
	}
	s.resetHLS()
	writeJSON(w, settingsPayload(cfg))
}

// handleSetOptimizer records the background-optimizer mode and nudges the supervisor to
// relaunch its agents under the new mode without a restart.
func (s *Server) handleSetOptimizer(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Mode string `json:"mode"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	mode := strings.TrimSpace(req.Mode)
	if !config.ValidOptimizeMode[mode] {
		http.Error(w, "optimizer mode must be none, cpu, gpu, or all", http.StatusBadRequest)
		return
	}
	cfg, ok := s.mutateConfig(w, func(c *config.Config) { c.OptimizeMode = mode })
	if !ok {
		return
	}
	s.signalReconfigOpt()
	writeJSON(w, settingsPayload(cfg))
}

// handleSetSubtitleLanguage records the preferred sidecar subtitle language. An empty
// value falls back to the "en" default.
func (s *Server) handleSetSubtitleLanguage(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Language string `json:"language"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cfg, ok := s.mutateConfig(w, func(c *config.Config) { c.SubtitleLanguage = strings.TrimSpace(req.Language) })
	if !ok {
		return
	}
	writeJSON(w, settingsPayload(cfg))
}
