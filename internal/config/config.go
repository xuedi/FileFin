// Package config reads and writes the single durable app config at ~/.<app>.md.
// The file is hand-editable markdown; user accounts live here as bcrypt hashes.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// AppName is the binary name and the config-file stem (~/.filefin.md).
const AppName = "filefin"

// User is one account: a bcrypt password hash and whether the account is an admin.
type User struct {
	Hash  string
	Admin bool
}

// Config is the durable application state. It is the only thing the app persists
// outside the filesystem data directory and the disposable cache.
type Config struct {
	DataDir   string
	CachePath string
	Port      int
	APIKeys   map[string]string
	Users     map[string]User // username -> account

	FFmpegPath         string
	FFprobePath        string
	TranscodeEnabled   bool
	HWAccel            string // "auto" (detect GPU) | "off" (force software)
	HWAccelDevice      string // optional DRM render node override, e.g. /dev/dri/renderD128
	OptimizeEnabled    bool   // background pre-transcode to direct-play copies (off by default)
	OptimizeMaxWorkers int    // ceiling on concurrent optimize encodes; 0 = auto (CPU count)
	OptimizeTargetLoad int    // CPU busy %% under which CPU workers may be added; 0 = default (80)

	LogLevel  string // error | info | debug
	LogOutput string // STDOUT | STDERR | a file path

	SubtitleLanguage string // fallback language tag for external subtitles with no detectable language

	path string
}

// New returns a Config with defaults applied.
func New() *Config {
	return &Config{
		Port:             8080,
		APIKeys:          map[string]string{},
		Users:            map[string]User{},
		FFmpegPath:       "ffmpeg",
		FFprobePath:      "ffprobe",
		TranscodeEnabled: true,
		HWAccel:          "auto",
		LogLevel:         "info",
		LogOutput:        "STDOUT",
		SubtitleLanguage: "en",
	}
}

// DefaultPath is ~/.<app>.md.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "."+AppName+".md"), nil
}

// DefaultCachePath is the per-user cache location for the disposable SQLite index.
func DefaultCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AppName, "cache.sqlite"), nil
}

// Path reports where this config was loaded from or last saved to.
func (c *Config) Path() string { return c.path }

// Load parses a config markdown file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := New()
	c.path = path
	section := ""
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		trimmed := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(trimmed, "## ") {
			section = strings.ToLower(strings.TrimSpace(trimmed[3:]))
			continue
		}
		if !strings.HasPrefix(trimmed, "-") {
			continue
		}
		key, val := splitKV(strings.TrimSpace(trimmed[1:]))
		switch section {
		case "data":
			switch key {
			case "dir":
				c.DataDir = val
			case "cache":
				c.CachePath = val
			}
		case "server":
			if key == "port" {
				if p, err := strconv.Atoi(val); err == nil {
					c.Port = p
				}
			}
		case "transcode":
			switch key {
			case "ffmpeg":
				if val != "" {
					c.FFmpegPath = val
				}
			case "ffprobe":
				if val != "" {
					c.FFprobePath = val
				}
			case "enabled":
				if b, err := strconv.ParseBool(val); err == nil {
					c.TranscodeEnabled = b
				}
			case "hwaccel":
				if val != "" {
					c.HWAccel = val
				}
			case "device":
				c.HWAccelDevice = val
			}
		case "optimize":
			switch key {
			case "enabled":
				if b, err := strconv.ParseBool(val); err == nil {
					c.OptimizeEnabled = b
				}
			case "maxWorkers":
				if n, err := strconv.Atoi(val); err == nil && n >= 0 {
					c.OptimizeMaxWorkers = n
				}
			case "targetLoad":
				if n, err := strconv.Atoi(val); err == nil && n >= 0 {
					c.OptimizeTargetLoad = n
				}
			}
		case "logging":
			switch key {
			case "level":
				if val != "" {
					c.LogLevel = val
				}
			case "output":
				if val != "" {
					c.LogOutput = val
				}
			}
		case "subtitles":
			if key == "defaultLanguage" {
				if v := strings.ToLower(strings.TrimSpace(val)); v != "" {
					c.SubtitleLanguage = v
				}
			}
		case "apikeys":
			if key != "" {
				c.APIKeys[key] = val
			}
		case "users":
			if key != "" {
				c.Users[key] = parseUser(val)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

// Save writes the config in the hand-editable markdown format. Keys are sorted so
// the output is stable across runs.
func (c *Config) Save(path string) error {
	if path == "" {
		path = c.path
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s config\n\n", AppName)
	b.WriteString("## data\n")
	fmt.Fprintf(&b, " - dir: %s\n", c.DataDir)
	fmt.Fprintf(&b, " - cache: %s\n", c.CachePath)
	b.WriteString("\n## server\n")
	fmt.Fprintf(&b, " - port: %d\n", c.Port)
	b.WriteString("\n## transcode\n")
	fmt.Fprintf(&b, " - ffmpeg: %s\n", c.FFmpegPath)
	fmt.Fprintf(&b, " - ffprobe: %s\n", c.FFprobePath)
	fmt.Fprintf(&b, " - enabled: %t\n", c.TranscodeEnabled)
	fmt.Fprintf(&b, " - hwaccel: %s\n", c.HWAccel)
	if c.HWAccelDevice != "" {
		fmt.Fprintf(&b, " - device: %s\n", c.HWAccelDevice)
	}
	b.WriteString("\n## optimize\n")
	fmt.Fprintf(&b, " - enabled: %t\n", c.OptimizeEnabled)
	if c.OptimizeMaxWorkers > 0 {
		fmt.Fprintf(&b, " - maxWorkers: %d\n", c.OptimizeMaxWorkers)
	}
	if c.OptimizeTargetLoad > 0 {
		fmt.Fprintf(&b, " - targetLoad: %d\n", c.OptimizeTargetLoad)
	}
	b.WriteString("\n## logging\n")
	fmt.Fprintf(&b, " - level: %s\n", c.LogLevel)
	fmt.Fprintf(&b, " - output: %s\n", c.LogOutput)
	b.WriteString("\n## subtitles\n")
	fmt.Fprintf(&b, " - defaultLanguage: %s\n", c.SubtitleLanguage)
	b.WriteString("\n## apikeys\n")
	for _, k := range sortedKeys(c.APIKeys) {
		fmt.Fprintf(&b, " - %s: %s\n", k, c.APIKeys[k])
	}
	b.WriteString("\n## users\n")
	for _, k := range sortedUsers(c.Users) {
		u := c.Users[k]
		if u.Admin {
			fmt.Fprintf(&b, " - %s: %s (admin)\n", k, u.Hash)
		} else {
			fmt.Fprintf(&b, " - %s: %s\n", k, u.Hash)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	c.path = path
	return nil
}

// parseUser splits a user bullet value into a hash and an admin flag. A trailing
// "(admin)" marker (case-insensitive, any surrounding spacing) grants admin; the rest is
// the bcrypt hash.
func parseUser(val string) User {
	u := User{}
	rest := strings.TrimSpace(val)
	if i := strings.LastIndex(strings.ToLower(rest), "(admin)"); i >= 0 && strings.TrimSpace(rest[i+len("(admin)"):]) == "" {
		u.Admin = true
		rest = strings.TrimSpace(rest[:i])
	}
	u.Hash = rest
	return u
}

func sortedUsers(m map[string]User) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func splitKV(s string) (string, string) {
	idx := strings.Index(s, ":")
	if idx < 0 {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:])
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
