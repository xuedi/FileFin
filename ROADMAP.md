# Roadmap

The path from the current pre-1.0 series to a stable **1.0**.

FileFin's core is built: the filesystem-as-truth model, the disposable cache and rebuild, direct
play with HLS fallback, the background agents (optimizer, thumbnailer, enricher, probe, discovery),
imports from Plex, Jellyfin, MyDramaList, and MyAnimeList, multi-user accounts with per-user state,
and a full admin UI. 1.0 is not about adding more subsystems. It is about the things that make a
self-hosted server **safe to expose, easy to recover, and pleasant on a large library**.

This file is the single place that tracks that work. Check items off as they land.

## What 1.0 means

A 1.0 server is one an admin can put on the public internet behind a reverse proxy, point at a
library of thousands of items, and trust to stay up, stay searchable, and survive a bad config
write. Everything below is scoped to that promise. Anything not required for it is deferred to
[Beyond 1.0](#beyond-10).

## Milestones

### 1. Discoverability on a large library

Browsing is category -> grid -> detail only. There is no search, and grid/category endpoints have
no result cap, so a big library both lacks a way to find a title and loads unbounded pages.

- [ ] Library search endpoint (title match over the cache) with a result limit.
- [ ] Search box in the frontend (library header), wired to the endpoint.
- [ ] Pagination or hard result caps on category and home-row endpoints.

### 2. Security hardening before public exposure

The auth model is sound (bcrypt, constant-response login, central admin gate) but missing the
defenses a public deployment needs.

- [ ] Login rate limiting / lockout (per-account and per-IP backoff) to bound brute force.
- [ ] Set the `Secure` flag on the session cookie when served over HTTPS.
- [ ] Minimum password length / basic strength check on create and update.
- [ ] Document the security model: TLS is terminated by an external reverse proxy, and CSRF
      protection currently rests on `SameSite=Lax`. Make both explicit rather than implicit.

### 3. Resilience and recovery

The config file in `$HOME` is the only durable state outside the media tree (accounts, settings).
A corrupt write loses every account, and there is no recovery story.

- [ ] Atomic, backed-up config writes (write-temp-then-rename, keep a prior copy).
- [ ] A documented backup/restore procedure for the config and, optionally, the cache.
- [ ] Decide and document the session model: sessions are in-memory, so a restart (including the
      deploy path) logs everyone out. Either persist sessions or state this clearly as expected.

### 4. Operability

Small gaps that matter once the server runs unattended.

- [ ] An unauthenticated liveness endpoint (`/healthz` or similar) for proxy/monitor health checks.
- [ ] Confirm structured logs cover the events an operator needs (auth failures, agent errors,
      transcode failures) without overlogging.

### 5. Quality gates

CI runs `gofmt`, `go vet`, build, and `go test` with ffmpeg present. Enough to ship, but a few
additions raise confidence for a 1.0 tag.

- [ ] Run the test suite with the race detector (`go test -race`).
- [ ] Add tests for the currently untested core: the ffmpeg exec wrapper (`internal/ffrun`) and
      `internal/fsutil`.
- [ ] A short release checklist (build static binary, smoke-test install mode, verify import paths).

### 6. Documentation for 1.0

- [ ] A deployment guide (reverse proxy + TLS, systemd unit, data-dir and config layout, backups).
- [ ] Keep the architecture docs in `docs/` current with anything the milestones above change.

## Beyond 1.0

Real wants, deliberately out of scope for the first stable release:

- **Mobile / responsive frontend.** The UI is desktop-first (fixed sidebar, no breakpoints). 1.0 is
  scoped as a desktop appliance; a responsive pass and collapsible nav come after.
- **Accessibility to WCAG-AA.** Today it is roughly WCAG-A. Notably: `aria-live` for toasts and a
  keyboard alternative to drag-to-reorder.
- **Internationalisation.** Strings are hardcoded English with no i18n framework.
- **Persistent sessions** with idle expiry and "log out other devices".
- **Frontend test suite** and a stricter Go linter (golangci-lint / staticcheck) in CI.
- **Metrics endpoint** (Prometheus-style) for dashboards.
- **Light theme** alongside the current hardcoded dark theme.
