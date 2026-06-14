// Package config is the single piece of persistent app state: a JSON file at
// ~/.filefin.json holding the server port and user accounts. It is written only by
// the server (via the GUI), so it does not need to be hand-editable.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"filefin/internal/fsutil"
)

// DefaultPort is used before any config exists (install mode).
const DefaultPort = 8080

// User is one account. The map key in Config.Users is the username (an email). Hash is
// a bcrypt hash of the password. ID is minted by the SQLite cache (the disposable
// mirror) and written back here, so the config stays the source of truth. Alias is a
// free-editable display name; Blocked temporarily bars login; CreatedAt/LastLoginAt are
// unix seconds (LastLoginAt 0 = never).
type User struct {
	ID          int64  `json:"id,omitempty"`
	Hash        string `json:"hash"`
	Alias       string `json:"alias,omitempty"`
	Admin       bool   `json:"admin,omitempty"`
	Blocked     bool   `json:"blocked,omitempty"`
	CreatedAt   int64  `json:"createdAt,omitempty"`
	LastLoginAt int64  `json:"lastLoginAt,omitempty"`
	MDLUsername string `json:"mdlUsername,omitempty"` // MyDramaList account to import watched + ratings from
}

// ActiveAdmins counts accounts that are admin and not blocked, i.e. those that can
// currently reach the admin area. User management uses it to refuse any change that
// would leave an installation with no usable admin.
func (c *Config) ActiveAdmins() int {
	n := 0
	for _, u := range c.Users {
		if u.Admin && !u.Blocked {
			n++
		}
	}
	return n
}

// Config is the whole persisted state. The cache is always a local SQLite file (see
// internal/db), so there is no database backend to configure here.
type Config struct {
	Port         int             `json:"port"`
	Users        map[string]User `json:"users"`
	DataDir      string          `json:"dataDir"`
	MediaFormat  string          `json:"mediaFormat"`  // "" until permanently chosen in Settings
	ImportFolder string          `json:"importFolder"` // server path media is imported from
	OMDBKey      string          `json:"omdbKey"`      // OMDb API key; "" skips metadata enrichment
	LogLevel     string          `json:"logLevel"`     // error|info|debug; "" => info
	LogOutput    string          `json:"logOutput"`    // STDOUT|STDERR|file path; "" => STDOUT

	FFmpegPath       string `json:"ffmpegPath"`       // "" => "ffmpeg" on PATH
	FFprobePath      string `json:"ffprobePath"`      // "" => "ffprobe" on PATH
	TranscodeEnabled *bool  `json:"transcodeEnabled"` // nil => enabled (the default)
	SubtitleLanguage string `json:"subtitleLanguage"` // preferred sidecar language; "" => "en"
	OptimizeMode     string `json:"optimizeMode"`     // none|cpu|gpu|all; "" => none (off)

	// DiscoveryInterval is the background discovery sweep period in seconds; 0 (the
	// default) turns the agent off. Only the fixed set in ValidDiscoveryInterval is
	// accepted (off / 1h / 3h / 12h / 24h).
	DiscoveryInterval int `json:"discoveryInterval"`
}

// Discovery sweep intervals (seconds). 0 is off; the rest are 1h / 3h / 12h / 24h.
const (
	Discovery1h  = 3600
	Discovery3h  = 10800
	Discovery12h = 43200
	Discovery24h = 86400
)

// ValidDiscoveryInterval is the set of accepted discovery intervals (0 = off).
var ValidDiscoveryInterval = map[int]bool{
	0: true, Discovery1h: true, Discovery3h: true, Discovery12h: true, Discovery24h: true,
}

// Optimizer modes drive which background pre-transcode agents run.
const (
	OptimizeNone = "none" // off (the default)
	OptimizeCPU  = "cpu"  // elastic software workers only
	OptimizeGPU  = "gpu"  // one always-on worker on the best encoder
	OptimizeAll  = "all"  // always-on worker plus elastic CPU workers
)

// ValidOptimizeMode is the set of accepted optimizer modes.
var ValidOptimizeMode = map[string]bool{
	OptimizeNone: true, OptimizeCPU: true, OptimizeGPU: true, OptimizeAll: true,
}

// OptimizeModeOr returns the configured optimizer mode, defaulting to none when unset.
func (c *Config) OptimizeModeOr() string {
	if c.OptimizeMode == "" {
		return OptimizeNone
	}
	return c.OptimizeMode
}

// TranscodeOn reports whether on-the-fly transcoding is enabled (the default when
// unset, so an upgraded config without the field keeps playing non-native files).
func (c *Config) TranscodeOn() bool {
	return c.TranscodeEnabled == nil || *c.TranscodeEnabled
}

// FFmpeg returns the configured ffmpeg path or the PATH default.
func (c *Config) FFmpeg() string {
	if c.FFmpegPath == "" {
		return "ffmpeg"
	}
	return c.FFmpegPath
}

// FFprobe returns the configured ffprobe path or the PATH default.
func (c *Config) FFprobe() string {
	if c.FFprobePath == "" {
		return "ffprobe"
	}
	return c.FFprobePath
}

// SubLang returns the preferred sidecar subtitle language or the "en" default.
func (c *Config) SubLang() string {
	if c.SubtitleLanguage == "" {
		return "en"
	}
	return c.SubtitleLanguage
}

// Path is ~/.filefin.json.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".filefin.json"), nil
}

// Exists reports whether a config file is present.
func Exists() bool {
	p, err := Path()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Load reads and parses the config.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Users == nil {
		c.Users = map[string]User{}
	}
	return &c, nil
}

// Save writes the config atomically (temp file + rename) with mode 0600, since it
// holds password hashes.
func Save(c *Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return fsutil.WriteFileAtomic(p, data, 0o600)
}
