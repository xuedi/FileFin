// Package meta parses a media folder's meta.md file. The format is intentionally
// forgiving: missing sections are fine and unknown sections are ignored.
package meta

import (
	"bufio"
	"os"
	"strings"

	"filefin/internal/model"
)

// ParseFile reads and parses a meta.md file.
func ParseFile(path string) (*model.Meta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(data)), nil
}

// Parse parses meta.md content.
func Parse(s string) *model.Meta {
	m := &model.Meta{}
	section := ""
	var desc, plot []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		trimmed := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(trimmed, "# "):
			m.Title = strings.TrimSpace(trimmed[2:])
			continue
		case strings.HasPrefix(trimmed, "## "):
			section = strings.ToLower(strings.TrimSpace(trimmed[3:]))
			continue
		}
		bullet := strings.HasPrefix(trimmed, "-")
		switch section {
		case "description":
			if trimmed != "" {
				desc = append(desc, trimmed)
			}
		case "plot":
			if trimmed != "" {
				plot = append(plot, trimmed)
			}
		case "metadata", "technical":
			if !bullet {
				continue
			}
			k, v := splitKV(strings.TrimSpace(trimmed[1:]))
			kv := model.KV{Key: k, Value: v}
			if section == "metadata" {
				m.Metadata = append(m.Metadata, kv)
			} else {
				m.Technical = append(m.Technical, kv)
			}
		case "actors", "tags":
			if !bullet {
				continue
			}
			val := strings.TrimSpace(trimmed[1:])
			if val == "" {
				continue
			}
			if section == "actors" {
				m.Actors = append(m.Actors, val)
			} else {
				m.Tags = append(m.Tags, val)
			}
		}
	}
	m.Description = strings.Join(desc, "\n")
	m.Plot = strings.Join(plot, "\n")
	return m
}

func splitKV(s string) (string, string) {
	idx := strings.Index(s, ":")
	if idx < 0 {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:])
}
