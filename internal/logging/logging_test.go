package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{"error": Error, "info": Info, "debug": Debug, "": Info, "INFO": Info}
	for in, want := range cases {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("expected error for unknown level")
	}
}

func TestInfoRendersHumanText(t *testing.T) {
	var buf bytes.Buffer
	New(Info, &buf).For(Import).Info("imported Django", Fields{"took_ms": 1234})
	out := buf.String()
	if !strings.Contains(out, "import: imported Django") {
		t.Errorf("missing human line: %q", out)
	}
	if strings.Contains(out, "took_ms") || strings.Contains(out, "{") {
		t.Errorf("info text leaked fields/JSON: %q", out)
	}
}

func TestDebugRendersJSONWithFields(t *testing.T) {
	var buf bytes.Buffer
	New(Debug, &buf).For(Import).Info("imported Django", Fields{"took_ms": 1234, "category": "Movies"})

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("debug output is not JSON: %v\n%s", err, buf.String())
	}
	if rec["category"] != "import" || rec["level"] != "info" || rec["msg"] != "imported Django" {
		t.Errorf("unexpected record: %v", rec)
	}
	if rec["took_ms"].(float64) != 1234 {
		t.Errorf("fields missing from JSON: %v", rec)
	}
	if _, ok := rec["pid"]; !ok {
		t.Error("pid missing from debug JSON")
	}
}

func TestErrorLevelHidesInfo(t *testing.T) {
	var buf bytes.Buffer
	lg := New(Error, &buf)
	lg.For(Backend).Info("started")
	if buf.Len() != 0 {
		t.Errorf("info event leaked at error level: %q", buf.String())
	}
	lg.For(Backend).Error("boom")
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("error event missing at error level: %q", buf.String())
	}
}

func TestReservedKeysWinOverFields(t *testing.T) {
	var buf bytes.Buffer
	New(Debug, &buf).For(Backend).Info("real", Fields{"msg": "spoofed", "category": "evil"})
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["msg"] != "real" || rec["category"] != "backend" {
		t.Errorf("fields overrode reserved keys: %v", rec)
	}
}

func TestNilLoggerIsNoop(t *testing.T) {
	var l *Logger
	l.For(Backend).Info("should not panic")
	l.SetLevel(Debug)
	l.SetOutput(&bytes.Buffer{})
}

func TestSetLevelAndOutputLive(t *testing.T) {
	var a, b bytes.Buffer
	lg := New(Info, &a)
	lg.For(Backend).Info("first")
	// Raise threshold to error: an info event is now hidden.
	lg.SetLevel(Error)
	lg.For(Backend).Info("hidden")
	if strings.Contains(a.String(), "hidden") {
		t.Errorf("info leaked after SetLevel(Error): %q", a.String())
	}
	// Swap output: subsequent events land in b, not a.
	lg.SetLevel(Info)
	lg.SetOutput(&b)
	lg.For(Backend).Info("second")
	if !strings.Contains(b.String(), "second") || strings.Contains(a.String(), "second") {
		t.Errorf("SetOutput did not redirect: a=%q b=%q", a.String(), b.String())
	}
}
