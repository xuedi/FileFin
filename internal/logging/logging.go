// Package logging is the app's small structured logger: a single level (error, info,
// debug) and output (STDOUT, STDERR, or a file), with per-category events. info renders
// one human line per event; debug renders the same events as enriched JSON. The guiding
// rule: log what the app does, minimally, never internal mechanics.
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Level is both the threshold (error hides info events) and the format selector (debug
// renders JSON; otherwise human text).
type Level int

const (
	Error Level = iota
	Info
	Debug
)

// Event categories. More are added as features land (a frontend, additional import
// producers); only the ones the rewrite actually emits today live here.
const (
	Backend   = "backend"
	Frontend  = "frontend"
	Import    = "import"
	Optimizer = "optimizer"
	Enrich    = "enrich"
	Thumbnail = "thumbnail"
)

// Fields is structured context attached to an event, rendered only at debug level.
type Fields map[string]any

// ParseLevel maps a config string to a Level; empty defaults to Info.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error":
		return Error, nil
	case "", "info":
		return Info, nil
	case "debug":
		return Debug, nil
	default:
		return Info, fmt.Errorf("logging: unknown level %q", s)
	}
}

func (l Level) String() string {
	switch l {
	case Error:
		return "error"
	case Debug:
		return "debug"
	default:
		return "info"
	}
}

// Logger writes events to one destination under a configured level. Both the level
// and the output can be changed live (the GUI edits them).
type Logger struct {
	mu    sync.Mutex
	level Level
	w     io.Writer
	pid   int
}

// New returns a Logger writing to w.
func New(level Level, w io.Writer) *Logger {
	return &Logger{level: level, w: w, pid: os.Getpid()}
}

// SetLevel changes the threshold/format live. Nil-safe.
func (l *Logger) SetLevel(level Level) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.level = level
	l.mu.Unlock()
}

// SetOutput swaps the destination live. The caller owns closing the previous output.
// Nil-safe.
func (l *Logger) SetOutput(w io.Writer) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.w = w
	l.mu.Unlock()
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// ResolveOutput maps a config output string to a writer plus a Closer (a file output
// is created/appended; std streams get a no-op closer).
func ResolveOutput(output string) (io.Writer, io.Closer, error) {
	switch strings.ToUpper(strings.TrimSpace(output)) {
	case "", "STDOUT":
		return os.Stdout, nopCloser{}, nil
	case "STDERR":
		return os.Stderr, nopCloser{}, nil
	default:
		f, err := os.OpenFile(output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, err
		}
		return f, f, nil
	}
}

// Open builds a Logger from config strings, resolving output to STDOUT, STDERR, or a
// file path. The returned Closer closes a file output; std streams get a no-op.
func Open(level, output string) (*Logger, io.Closer, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, nil, err
	}
	w, closer, err := ResolveOutput(output)
	if err != nil {
		return nil, nil, err
	}
	return New(lvl, w), closer, nil
}

// Scoped binds a Logger to one category so callers do not repeat it.
type Scoped struct {
	l   *Logger
	cat string
}

// For returns a category-scoped logger. Nil-safe: a nil Logger yields a no-op Scoped.
func (l *Logger) For(category string) *Scoped { return &Scoped{l: l, cat: category} }

// Info records an event visible at info and debug levels.
func (s *Scoped) Info(msg string, f ...Fields) { s.emit(Info, msg, f) }

// Debug records an event visible only at debug level. Use it for high-frequency or
// mechanical detail that would be noise at info.
func (s *Scoped) Debug(msg string, f ...Fields) { s.emit(Debug, msg, f) }

// Error records an event visible at every level.
func (s *Scoped) Error(msg string, f ...Fields) { s.emit(Error, msg, f) }

func (s *Scoped) emit(ev Level, msg string, f []Fields) {
	if s == nil || s.l == nil {
		return
	}
	var fields Fields
	if len(f) > 0 {
		fields = f[0]
	}
	s.l.emit(ev, s.cat, msg, fields)
}

func (l *Logger) emit(ev Level, cat, msg string, f Fields) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.level == Error && ev != Error {
		return
	}
	if l.level == Debug {
		l.writeJSON(now, ev, cat, msg, f)
		return
	}
	fmt.Fprintf(l.w, "[%s] %s: %s\n", now.Format("2006-01-02 15:04:05"), cat, msg)
}

// writeJSON renders the event as a single JSON object. Reserved keys win over fields.
func (l *Logger) writeJSON(now time.Time, ev Level, cat, msg string, f Fields) {
	rec := make(map[string]any, len(f)+5)
	for k, v := range f {
		rec[k] = v
	}
	rec["ts"] = now.Format(time.RFC3339)
	rec["level"] = ev.String()
	rec["category"] = cat
	rec["msg"] = msg
	rec["pid"] = l.pid
	b, err := json.Marshal(rec)
	if err != nil {
		fmt.Fprintf(l.w, "[%s] %s: %s\n", now.Format("2006-01-02 15:04:05"), cat, msg)
		return
	}
	l.w.Write(append(b, '\n'))
}
