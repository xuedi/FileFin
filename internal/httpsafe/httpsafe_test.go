package httpsafe

import (
	"net/http"
	"testing"
)

func TestNoInternalRedirect(t *testing.T) {
	mk := func(rawURL string) *http.Request {
		req, err := http.NewRequest("GET", rawURL, nil)
		if err != nil {
			t.Fatalf("bad url %q: %v", rawURL, err)
		}
		return req
	}
	via := []*http.Request{mk("https://api.example.com/start")}

	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1/",
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://[::1]/",
		"http://0.0.0.0/",
	}
	for _, u := range blocked {
		if err := NoInternalRedirect(mk(u), via); err == nil {
			t.Errorf("redirect to %s should be refused", u)
		}
	}

	allowed := []string{
		"https://cdn.example.com/image.jpg",
		"https://other-public-host.net/x",
	}
	for _, u := range allowed {
		if err := NoInternalRedirect(mk(u), via); err != nil {
			t.Errorf("redirect to %s should be allowed, got %v", u, err)
		}
	}

	// Too many hops is stopped regardless of target.
	many := make([]*http.Request, 10)
	if err := NoInternalRedirect(mk("https://public.example.com/"), many); err == nil {
		t.Error("expected redirect chain to stop after 10 hops")
	}
}
