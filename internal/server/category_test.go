package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"filefin/internal/library"
)

// categoryPage reads one category's page payload.
func categoryPage(t *testing.T, h http.Handler, admin *http.Cookie, name string) categoryDetail {
	t.Helper()
	rr := do(t, h, "GET", "/api/admin/categories/"+name, "", admin)
	if rr.Code != 200 {
		t.Fatalf("category page %s: %d %s", name, rr.Code, rr.Body.String())
	}
	var d categoryDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCategoryPageWritesEveryMarker(t *testing.T) {
	dataDir := t.TempDir()
	_, h, admin, _ := installedServer(t, dataDir)
	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Shows","alias":"Shows - Korea"}`, admin); rr.Code != 200 {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}

	// A fresh category takes both kinds and has learned nothing.
	d := categoryPage(t, h, admin, "Shows")
	if d.Markers.Kind != library.KindBoth || len(d.Learned) != 0 || !d.TopLevel || !d.Empty {
		t.Fatalf("fresh category page = %+v", d)
	}

	body := `{"alias":"Korean shows","otherMedia":false,"markers":{"kind":"shows",` +
		`"languages":["Korean"],"countries":["South Korea"],"keywords":["kdrama"]}}`
	if rr := do(t, h, "PUT", "/api/admin/categories/Shows", body, admin); rr.Code != 204 {
		t.Fatalf("update: %d %s", rr.Code, rr.Body.String())
	}
	d = categoryPage(t, h, admin, "Shows")
	if d.Alias != "Korean shows" || d.Markers.Kind != library.KindShows ||
		len(d.Markers.Languages) != 1 || len(d.Markers.Countries) != 1 || len(d.Markers.Keywords) != 1 {
		t.Fatalf("after update = %+v", d)
	}

	// Learning survives an update that carries no learned map; a map that is sent replaces it.
	if err := library.Learn(dataDir, "Shows", []string{"grp:JKCT"}); err != nil {
		t.Fatal(err)
	}
	if rr := do(t, h, "PUT", "/api/admin/categories/Shows", body, admin); rr.Code != 204 {
		t.Fatalf("second update: %d %s", rr.Code, rr.Body.String())
	}
	d = categoryPage(t, h, admin, "Shows")
	if len(d.Learned) != 1 || d.Learned[0].Marker != "grp:JKCT" || d.Learned[0].Count != 1 {
		t.Fatalf("learning lost on an unrelated save: %+v", d.Learned)
	}
	pruned := `{"alias":"Korean shows","markers":{"kind":"shows","learned":{}}}`
	if rr := do(t, h, "PUT", "/api/admin/categories/Shows", pruned, admin); rr.Code != 204 {
		t.Fatalf("prune: %d %s", rr.Code, rr.Body.String())
	}
	if d = categoryPage(t, h, admin, "Shows"); len(d.Learned) != 0 {
		t.Fatalf("learned marker not removed: %+v", d.Learned)
	}
	// The list carries the same summary, so the state is visible without opening a category.
	rr := do(t, h, "GET", "/api/admin/categories", "", admin)
	if !strings.Contains(rr.Body.String(), `"kind":"shows"`) {
		t.Fatalf("list is missing the markers summary: %s", rr.Body.String())
	}
}

func TestCategoryPageRejectsOtherMediaOnSubCategory(t *testing.T) {
	_, h, admin, _ := installedServer(t, t.TempDir())
	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Movies"}`, admin); rr.Code != 200 {
		t.Fatalf("create parent: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do(t, h, "POST", "/api/admin/categories", `{"name":"Action","parentId":1}`, admin); rr.Code != 200 {
		t.Fatalf("create sub: %d %s", rr.Code, rr.Body.String())
	}
	sub := "Movies%2FAction"
	if rr := do(t, h, "PUT", "/api/admin/categories/"+sub, `{"alias":"Action","otherMedia":true}`, admin); rr.Code != 204 {
		t.Fatalf("update sub: %d %s", rr.Code, rr.Body.String())
	}
	d := categoryPage(t, h, admin, sub)
	if d.TopLevel || d.OtherMedia {
		t.Fatalf("a sub-category must not store its own other-media flag: %+v", d)
	}
	// The parent's flag is what it inherits.
	if rr := do(t, h, "PUT", "/api/admin/categories/Movies", `{"alias":"Movies","otherMedia":true}`, admin); rr.Code != 204 {
		t.Fatalf("update parent: %d %s", rr.Code, rr.Body.String())
	}
	if d = categoryPage(t, h, admin, sub); !d.Inherited {
		t.Fatalf("sub-category should inherit the root flag: %+v", d)
	}
}

func TestCategoryPageUnknownCategory(t *testing.T) {
	_, h, admin, _ := installedServer(t, t.TempDir())
	if rr := do(t, h, "GET", "/api/admin/categories/Nope", "", admin); rr.Code != 404 {
		t.Fatalf("unknown category: %d, want 404", rr.Code)
	}
	if rr := do(t, h, "PUT", "/api/admin/categories/Nope", `{"alias":"x"}`, admin); rr.Code != 404 {
		t.Fatalf("update unknown category: %d, want 404", rr.Code)
	}
}
