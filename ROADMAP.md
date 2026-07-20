# Roadmap

The path from the current pre-1.0 series to a stable **1.0**.

FileFin's core is built: the filesystem-as-truth model, the disposable cache and rebuild, direct
play with HLS fallback, the background agents (optimizer, thumbnailer, enricher, probe, discovery),
imports from a drop folder, Plex, Jellyfin, MyDramaList, and MyAnimeList, facet search, metadata
matching and hand-editing, category markers that preselect an import's target, multi-user accounts
with per-user state, and a full admin UI. 1.0 is not about adding more subsystems. It is about the
things that make a self-hosted server **safe to expose, easy to recover, and pleasant to use on a
large library**.

This file is the single place that tracks that work. Check items off as they land.

## What 1.0 means

A 1.0 server is one an admin can put on the public internet behind a reverse proxy, point at a
library of thousands of items, and trust to stay up, stay searchable, and survive a bad config
write. Everything below is scoped to that promise. Anything not required for it is deferred to
[Beyond 1.0](#beyond-10).

## Milestones

### 1. Discoverability on a large library

Search is built - by title, cast, genre, director, language, or year, with detail-page facets
doubling as links into a scoped search. What is still missing is a ceiling: no query, category, or
home row caps its result set, so a big library loads unbounded pages.

- [x] Library search endpoint (facet match over the cache).
- [x] Search box in the frontend (library header), wired to the endpoint.
- [ ] Pagination or hard result caps on search, category, and home-row endpoints. This is now the
      whole of this milestone: search made it easier to ask for thousands of rows at once.

### 2. Security hardening before public exposure

The auth model is sound (bcrypt, constant-response login, central admin gate); the defenses a
public deployment needs are in place.

- [x] Login rate limiting / lockout (per-account and per-IP backoff) to bound brute force.
- [x] Set the `Secure` flag on the session cookie when served over HTTPS (also honoured behind a
      loopback proxy that sets `X-Forwarded-Proto`).
- [x] Minimum password length / basic strength check on create and update.
- [x] Document the security model: TLS is terminated by an external reverse proxy, and CSRF
      protection currently rests on `SameSite=Lax`. Both are stated in `docs/runtime.md`.

### 3. Resilience and recovery

The config file in `$HOME` is the only durable state outside the media tree (accounts, settings).

- [x] Atomic config writes (temp file in the same directory, then rename), so a crashed or
      concurrent write can never leave a half-written config.
- [ ] Keep a prior copy of the config alongside the atomic write, so a *valid but wrong* write
      (a bad settings change, a botched edit) is recoverable and not just a non-corrupt loss.
- [ ] A documented backup/restore procedure for the config and, optionally, the cache.
- [x] Decide and document the session model: sessions are in-memory and cleared on restart
      (including the deploy path), stated as expected behaviour in `docs/runtime.md`.

### 4. Operability

Small gaps that matter once the server runs unattended.

- [ ] An unauthenticated liveness endpoint (`/healthz` or similar) for proxy/monitor health checks.
      `GET /api/state` is unauthenticated and already serves as a de-facto probe; either bless it
      as the documented one or add a purpose-built endpoint.
- [ ] Confirm structured logs cover the events an operator needs (auth failures, agent errors,
      transcode failures) without overlogging.

### 5. Quality gates

CI runs `gofmt`, `go vet`, build, and `go test` with ffmpeg present. Enough to ship, but a few
additions raise confidence for a 1.0 tag.

- [ ] Run the test suite with the race detector (`go test -race`).
- [ ] Add tests for the currently untested core: the ffmpeg exec wrapper (`internal/ffrun`) and
      `internal/fsutil`.
- [x] A release path that cannot ship a mismatched version: `just release X.Y.Z` verifies
      `version.go`, the README badge, a clean tree and `main` before tagging, and the tag-triggered
      workflow builds and publishes the artifacts.
- [ ] Smoke-test the packaged artifacts, not just the source build: install one release package on
      a clean machine and walk through install mode once per release.

### 6. Documentation for 1.0

- [ ] A deployment guide (reverse proxy + TLS, systemd unit, data-dir and config layout, backups).
      The pieces are scattered across `docs/install.md` and `docs/runtime.md`; 1.0 needs one page
      an admin can follow start to finish.
- [ ] Keep the architecture docs in `docs/` current with anything the milestones above change.

### 7. A compact, modular UI

The frontend works, but it is a thin skin over Bulma: 398 lines of hand-written `ff-*` rules (124
distinct classes) layered onto a 687 kB Bulma stylesheet, with the markup written inline in each
view and only five shared components. Every new page re-invents its own layout, and the density is
loose - admin tables run wide and pages scroll for rows that would fit on one screen.

The goal is a UI built from **composable pieces with near-zero CSS of its own**: what a thing looks
like belongs to a component, not to a global sheet that grows with every feature.

- [ ] Choose the target system - a classless/semantic base or a utility layer - and replace the
      Bulma + `ff-*` mix with it. One choice applied everywhere, not a third layer on top of two.
- [ ] Extract the repeating patterns into components: the admin list/table, the card section with
      its heading, the labelled field with its help line, the page header with actions, the status
      chip. Pages become compositions of those.
- [ ] Shrink `web/src/app.css` to layout primitives only (app shell, poster grid, player), with a
      stated ceiling instead of "whatever accumulates".
- [ ] A density pass: compact rows and fewer wrapping columns, so a library page and an admin table
      show more per screen without shrinking the type.
- [ ] Absorb what this makes cheap: responsive breakpoints and a light theme stop being separate
      projects once styling lives in components (see Beyond 1.0).

## Beyond 1.0

Real wants, deliberately out of scope for the first stable release:

- **Mobile / responsive frontend.** The UI is desktop-first (fixed sidebar, no breakpoints). 1.0 is
  scoped as a desktop appliance; the responsive pass and collapsible nav ride on milestone 7.
- **Light theme** alongside the current hardcoded dark theme - likewise a by-product of milestone 7
  rather than its own project.
- **Accessibility to WCAG-AA.** Today it is roughly WCAG-A. Notably: `aria-live` for toasts and a
  keyboard alternative to drag-to-reorder.
- **Internationalisation.** Strings are hardcoded English with no i18n framework.
- **Persistent sessions** with idle expiry and "log out other devices".
- **Frontend test suite** and a stricter Go linter (golangci-lint / staticcheck) in CI.
- **Metrics endpoint** (Prometheus-style) for dashboards.
- **Identify from the import page.** An admin-triggered metadata search on a low-confidence import
  row, reusing the existing search endpoint - never automatic, so the import page stays a pure
  offline filesystem preview.
