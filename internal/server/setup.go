package server

import (
	"net"
	"os"
	"strconv"

	"filefin/internal/config"
)

// SetupURLs builds the token-bearing web-install URLs for a pending config. When the server
// binds all interfaces it offers every detected non-loopback address plus the hostname and
// loopback; when pinned to a specific bind address it offers just that one. The token rides in
// the query string so the whole line can be pasted as a link. It is shared by the `setup`
// command (which prints them) and install-mode logging (which logs them).
func SetupURLs(cfg *config.Config) []string {
	port := strconv.Itoa(cfg.Port)
	hosts := setupHosts(cfg.BindAddress)
	urls := make([]string, 0, len(hosts))
	for _, h := range hosts {
		urls = append(urls, "http://"+net.JoinHostPort(h, port)+"/?token="+cfg.SetupToken)
	}
	return urls
}

// setupHosts returns the candidate hostnames/IPs a browser could use to reach the installer.
// A specific bind address pins the list to that one; all-interfaces detects the hostname and
// every non-loopback interface address, always ending with localhost as a fallback.
func setupHosts(bind string) []string {
	if bind != "" && bind != "0.0.0.0" && bind != "::" {
		return []string{bind}
	}
	seen := map[string]bool{}
	var hosts []string
	add := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	if name, err := os.Hostname(); err == nil {
		add(name)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.IsGlobalUnicast() {
				add(ipnet.IP.String())
			}
		}
	}
	add("localhost")
	return hosts
}
