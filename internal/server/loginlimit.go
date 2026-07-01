package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Login throttle thresholds. An account or a source IP that crosses its failure count
// within its sliding window is locked for the lockout period; a correct login clears the
// account's counter. These are deliberately generous so a legitimate user fumbling a
// password is never locked out, while an automated brute force is stopped quickly.
const (
	loginAccountFails  = 5
	loginAccountWindow = 15 * time.Minute
	loginAccountLock   = 15 * time.Minute

	loginIPFails  = 20
	loginIPWindow = 5 * time.Minute
	loginIPLock   = 15 * time.Minute

	// loginLimiterMax bounds the tracking maps; past it, stale records are pruned so a
	// spray across many accounts/IPs cannot grow memory without limit.
	loginLimiterMax = 4096
)

type loginAttempts struct {
	fails       int
	windowStart time.Time
	lockedUntil time.Time
}

// loginLimiter throttles /api/login by account and by source IP, so an unauthenticated
// attacker can neither brute-force credentials nor flood the deliberately slow bcrypt
// compare into a CPU-exhaustion DoS. It is in-memory and cleared on restart, which is
// sufficient for a single instance behind one proxy.
type loginLimiter struct {
	mu     sync.Mutex
	byAcct map[string]*loginAttempts
	byIP   map[string]*loginAttempts
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{byAcct: map[string]*loginAttempts{}, byIP: map[string]*loginAttempts{}}
}

// allowed reports whether a login for account from ip may proceed; when throttled it also
// returns how long until the lock lifts.
func (l *loginLimiter) allowed(account, ip string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if d := lockRemaining(l.byAcct[account], now); d > 0 {
		return false, d
	}
	if d := lockRemaining(l.byIP[ip], now); d > 0 {
		return false, d
	}
	return true, 0
}

func lockRemaining(a *loginAttempts, now time.Time) time.Duration {
	if a == nil || a.lockedUntil.IsZero() || !now.Before(a.lockedUntil) {
		return 0
	}
	return a.lockedUntil.Sub(now)
}

// fail records one failed attempt against both the account and the IP, locking whichever
// crosses its threshold within its window.
func (l *loginLimiter) fail(account, ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.byAcct[account] = bumpAttempt(l.byAcct[account], now, loginAccountWindow, loginAccountFails, loginAccountLock)
	l.byIP[ip] = bumpAttempt(l.byIP[ip], now, loginIPWindow, loginIPFails, loginIPLock)
	if len(l.byAcct)+len(l.byIP) > loginLimiterMax {
		l.prune(now)
	}
}

// success clears the account's failure record on a correct login. The IP record is left to
// expire so a shared address still running an attack keeps trending toward its limit.
func (l *loginLimiter) success(account string) {
	l.mu.Lock()
	delete(l.byAcct, account)
	l.mu.Unlock()
}

func bumpAttempt(a *loginAttempts, now time.Time, window time.Duration, max int, lock time.Duration) *loginAttempts {
	if a == nil || now.Sub(a.windowStart) > window {
		a = &loginAttempts{windowStart: now}
	}
	a.fails++
	if a.fails >= max {
		a.lockedUntil = now.Add(lock)
		a.fails = 0
		a.windowStart = now
	}
	return a
}

// prune drops records whose window has elapsed and that are not currently locked. Called
// under l.mu.
func (l *loginLimiter) prune(now time.Time) {
	for k, a := range l.byAcct {
		if attemptStale(a, now, loginAccountWindow) {
			delete(l.byAcct, k)
		}
	}
	for k, a := range l.byIP {
		if attemptStale(a, now, loginIPWindow) {
			delete(l.byIP, k)
		}
	}
}

func attemptStale(a *loginAttempts, now time.Time, window time.Duration) bool {
	if a == nil {
		return true
	}
	if !a.lockedUntil.IsZero() && now.Before(a.lockedUntil) {
		return false
	}
	return now.Sub(a.windowStart) > window
}

// clientIP is the source address used for login throttling and (in future) any per-IP
// logic. A forwarded-for header is trusted only when the immediate peer is loopback (the
// co-located reverse proxy), never from a direct client, so it cannot be spoofed to evade
// or poison the limiter.
func clientIP(r *http.Request) string {
	host := hostOf(r.RemoteAddr)
	if isLoopbackHost(host) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				xff = xff[:i]
			}
			return strings.TrimSpace(xff)
		}
	}
	return host
}

// cookieSecure reports whether the session cookie should carry the Secure attribute: the
// request arrived over TLS directly, or via the co-located (loopback) reverse proxy that
// terminated TLS and forwarded X-Forwarded-Proto=https. A plain-HTTP LAN deployment leaves
// it off so the cookie is not silently dropped and login keeps working.
func cookieSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return isLoopbackHost(hostOf(r.RemoteAddr)) &&
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func hostOf(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

func isLoopbackHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}
