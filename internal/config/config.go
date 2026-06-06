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

// Config is the durable application state. It is the only thing the app persists
// outside the filesystem data directory and the disposable cache.
type Config struct {
	DataDir   string
	CachePath string
	Port      int
	APIKeys   map[string]string
	Users     map[string]string // username -> bcrypt hash

	FFmpegPath       string
	FFprobePath      string
	TranscodeEnabled bool

	path string
}

// New returns a Config with defaults applied.
func New() *Config {
	return &Config{
		Port:             8080,
		APIKeys:          map[string]string{},
		Users:            map[string]string{},
		FFmpegPath:       "ffmpeg",
		FFprobePath:      "ffprobe",
		TranscodeEnabled: true,
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
			}
		case "apikeys":
			if key != "" {
				c.APIKeys[key] = val
			}
		case "users":
			if key != "" {
				c.Users[key] = val
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
	b.WriteString("\n## apikeys\n")
	for _, k := range sortedKeys(c.APIKeys) {
		fmt.Fprintf(&b, " - %s: %s\n", k, c.APIKeys[k])
	}
	b.WriteString("\n## users\n")
	for _, k := range sortedKeys(c.Users) {
		fmt.Fprintf(&b, " - %s: %s\n", k, c.Users[k])
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
