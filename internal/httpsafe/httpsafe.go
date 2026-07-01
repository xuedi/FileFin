// Package httpsafe holds shared hardening for the app's outbound HTTP clients (OMDb, MAL,
// MDL): a redirect guard that refuses to follow a redirect into internal address space
// (SSRF), and a body-size ceiling so a hostile or oversized upstream cannot exhaust memory.
package httpsafe

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
)

// MaxBodyBytes is the default ceiling applied to an outbound response body. It is generous
// for the JSON APIs and HTML list pages the app fetches, while still bounding a hostile
// upstream.
const MaxBodyBytes = 8 << 20 // 8 MiB

// LimitBody wraps an upstream response body so at most MaxBodyBytes are ever read from it.
func LimitBody(r io.Reader) io.Reader { return io.LimitReader(r, MaxBodyBytes) }

// NoInternalRedirect is an http.Client.CheckRedirect that follows ordinary public
// redirects but refuses one whose target host is an internal/loopback/link-local/private
// address, closing the SSRF pivot a hostile upstream could otherwise use (e.g. a 302 to
// 169.254.169.254). It also stops after 10 hops.
func NoInternalRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if isInternalHost(req.URL.Hostname()) {
		return fmt.Errorf("refusing redirect to internal host %q", req.URL.Hostname())
	}
	return nil
}

// isInternalHost reports whether host is an IP literal in internal address space. A bare
// hostname is not resolved here (no DNS), so only literal-IP redirect targets - the classic
// metadata/loopback SSRF vectors - are blocked.
func isInternalHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
