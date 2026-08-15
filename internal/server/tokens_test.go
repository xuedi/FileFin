package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// bearer issues a request carrying an Authorization: Bearer header instead of a session
// cookie, for exercising the token-auth path that do() (cookie-only) cannot reach.
func bearer(t *testing.T, h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestTokenLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	_, h, admin, _ := installedServer(t, dataDir)

	// Create.
	rr := do(t, h, "POST", "/api/profile/tokens", `{"label":"laptop script"}`, admin)
	if rr.Code != 200 {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Label != "laptop script" || created.Token == "" || created.ID == "" {
		t.Fatalf("incomplete create response: %+v", created)
	}
	if !strings.HasPrefix(created.Token, "ffpat_") {
		t.Fatalf("expected ffpat_ prefixed token, got %q", created.Token)
	}

	// List: the secret never comes back.
	rr = do(t, h, "GET", "/api/profile/tokens", "", admin)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), created.ID) || strings.Contains(rr.Body.String(), created.Token) {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}

	// The minted token authenticates a plain API call.
	rr = bearer(t, h, "GET", "/api/me", created.Token)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"user":"admin"`) {
		t.Fatalf("bearer auth: %d %s", rr.Code, rr.Body.String())
	}

	// A garbage token is rejected.
	if rr := bearer(t, h, "GET", "/api/me", "ffpat_not-a-real-token"); rr.Code != 401 {
		t.Fatalf("garbage token: expected 401, got %d", rr.Code)
	}

	// Revoke, then the same token no longer authenticates and is gone from the list.
	rr = do(t, h, "DELETE", "/api/profile/tokens/"+created.ID, "", admin)
	if rr.Code != 204 {
		t.Fatalf("revoke: %d %s", rr.Code, rr.Body.String())
	}
	if rr := bearer(t, h, "GET", "/api/me", created.Token); rr.Code != 401 {
		t.Fatalf("expected 401 after revoke, got %d", rr.Code)
	}
	rr = do(t, h, "GET", "/api/profile/tokens", "", admin)
	if strings.Contains(rr.Body.String(), created.ID) {
		t.Fatalf("revoked token still listed: %s", rr.Body.String())
	}
}

func TestTokenRevokeIsScopedToCaller(t *testing.T) {
	dataDir := t.TempDir()
	_, h, admin, bob := installedServer(t, dataDir)

	rr := do(t, h, "POST", "/api/profile/tokens", `{"label":"bob's token"}`, bob)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// admin cannot revoke bob's token by id - it isn't among admin's own tokens.
	if rr := do(t, h, "DELETE", "/api/profile/tokens/"+created.ID, "", admin); rr.Code != 404 {
		t.Fatalf("expected 404 revoking another user's token, got %d %s", rr.Code, rr.Body.String())
	}

	// bob can still revoke his own.
	if rr := do(t, h, "DELETE", "/api/profile/tokens/"+created.ID, "", bob); rr.Code != 204 {
		t.Fatalf("owner revoke: %d %s", rr.Code, rr.Body.String())
	}
}

func TestBlockedUserTokenStopsAuthenticating(t *testing.T) {
	dataDir := t.TempDir()
	s, h, _, bob := installedServer(t, dataDir)

	rr := do(t, h, "POST", "/api/profile/tokens", `{"label":"bob's token"}`, bob)
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if rr := bearer(t, h, "GET", "/api/me", created.Token); rr.Code != 200 {
		t.Fatalf("expected token to authenticate before block, got %d", rr.Code)
	}

	s.mu.Lock()
	u := s.cfg.Users["bob"]
	u.Blocked = true
	s.cfg.Users["bob"] = u
	s.mu.Unlock()

	if rr := bearer(t, h, "GET", "/api/me", created.Token); rr.Code != 401 {
		t.Fatalf("expected 401 for a blocked user's token, got %d", rr.Code)
	}
}
