package server

import (
	"encoding/json"
	"testing"
)

func TestSummary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, h, admin, bob := installedServer(t, t.TempDir())

	// Non-admin is forbidden.
	if rr := do(t, h, "GET", "/api/admin/summary", "", bob); rr.Code != 403 {
		t.Fatalf("non-admin summary: %d, want 403", rr.Code)
	}

	// Create a category so the library tally is non-zero.
	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Movies","alias":"Films"}`, admin); rr.Code != 200 {
		t.Fatalf("create category: %d %s", rr.Code, rr.Body.String())
	}

	rr := do(t, h, "GET", "/api/admin/summary", "", admin)
	if rr.Code != 200 {
		t.Fatalf("summary: %d %s", rr.Code, rr.Body.String())
	}
	var sum struct {
		Library   map[string]int `json:"library"`
		Users     map[string]int `json:"users"`
		Optimizer map[string]any `json:"optimizer"`
		Enrich    map[string]int `json:"enrich"`
		Imports   map[string]int `json:"imports"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &sum); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if sum.Library["categories"] != 1 {
		t.Fatalf("categories = %d, want 1", sum.Library["categories"])
	}
	// installedServer seeds two accounts, one of them admin.
	if sum.Users["total"] != 2 || sum.Users["admins"] != 1 {
		t.Fatalf("users = %+v, want total 2 / admins 1", sum.Users)
	}
	if sum.Optimizer["mode"] != "none" {
		t.Fatalf("optimizer mode = %v, want none", sum.Optimizer["mode"])
	}
}
