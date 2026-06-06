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
	New(Info, &buf).For(Optimizer).Info("optimized Django", Fields{"took_ms": 1234})
	out := buf.String()
	if !strings.Contains(out, "optimizer: optimized Django") {
		t.Errorf("missing human line: %q", out)
	}
	if strings.Contains(out, "took_ms") || strings.Contains(out, "{") {
		t.Errorf("info text leaked fields/JSON: %q", out)
	}
}

func TestDebugRendersJSONWithFields(t *testing.T) {
	var buf bytes.Buffer
	New(Debug, &buf).For(Optimizer).Info("optimized Django", Fields{"took_ms": 1234, "encoder": "vaapi"})

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("debug output is not JSON: %v\n%s", err, buf.String())
	}
	if rec["category"] != "optimizer" || rec["level"] != "info" || rec["msg"] != "optimized Django" {
		t.Errorf("unexpected record: %v", rec)
	}
	if rec["encoder"] != "vaapi" || rec["took_ms"].(float64) != 1234 {
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
}
