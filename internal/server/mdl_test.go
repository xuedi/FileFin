package server

import (
	"strings"
	"testing"

	"filefin/internal/importer"
)

func TestRatingEndpoint(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	id, dir := seedMedia(t, s, dataDir, "Movies", catID, "(1999) The Matrix", "(1999) The Matrix.mp4",
		importer.Meta{Title: "The Matrix", Year: 1999})

	// Set a rating.
	if rr := do(t, h, "POST", "/api/media/"+id+"/rating", `{"rating":7}`, admin); rr.Code != 204 {
		t.Fatalf("rate: %d %s", rr.Code, rr.Body.String())
	}
	if m, err := importer.ReadMeta(dir); err != nil || m.State["admin"].Rating != 7 {
		t.Fatalf("rating not recorded: %+v err=%v", m.State, err)
	}
	if rr := do(t, h, "GET", "/api/media/"+id, "", admin); !strings.Contains(rr.Body.String(), `"rating":7`) {
		t.Fatalf("detail missing rating: %s", rr.Body.String())
	}

	// Out-of-range is rejected; a valid 0 clears it.
	if rr := do(t, h, "POST", "/api/media/"+id+"/rating", `{"rating":11}`, admin); rr.Code != 400 {
		t.Fatalf("expected 400 for rating 11, got %d", rr.Code)
	}
	if rr := do(t, h, "POST", "/api/media/"+id+"/rating", `{"rating":0}`, admin); rr.Code != 204 {
		t.Fatalf("clear rating: %d", rr.Code)
	}
	if m, _ := importer.ReadMeta(dir); m.State["admin"].Rating != 0 {
		t.Fatalf("rating not cleared: %+v", m.State)
	}
}

func TestMDLProfileAndApply(t *testing.T) {
	s, h, admin, dataDir, catID := mediaTestServer(t)
	id, dir := seedMedia(t, s, dataDir, "Movies", catID, "(2021) Vincenzo", "(2021) Vincenzo.mp4",
		importer.Meta{Title: "Vincenzo", Year: 2021})

	// Preview before a username is set is a clean 400, not a scrape attempt.
	if rr := do(t, h, "POST", "/api/mdl/preview", "", admin); rr.Code != 400 {
		t.Fatalf("preview without username: %d %s", rr.Code, rr.Body.String())
	}

	// Save the username; it comes back on the profile response.
	if rr := do(t, h, "POST", "/api/profile/mdl", `{"mdlUsername":" someone "}`, admin); rr.Code != 200 ||
		!strings.Contains(rr.Body.String(), `"mdlUsername":"someone"`) {
		t.Fatalf("save username: %d %s", rr.Code, rr.Body.String())
	}

	// Apply a confirmed row: rating + watched land in meta.json.
	body := `{"items":[{"mediaId":"` + id + `","rating":8,"markWatched":true}]}`
	if rr := do(t, h, "POST", "/api/mdl/apply", body, admin); rr.Code != 200 ||
		!strings.Contains(rr.Body.String(), `"applied":1`) {
		t.Fatalf("apply: %d %s", rr.Code, rr.Body.String())
	}
	m, err := importer.ReadMeta(dir)
	if err != nil || m.State["admin"].Rating != 8 || !m.State["admin"].Watched {
		t.Fatalf("apply did not record rating+watched: %+v err=%v", m.State, err)
	}
}
