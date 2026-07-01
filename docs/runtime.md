# Server runtime

How the binary boots, decides whether it is a fresh install or a running appliance, serves
the GUI and API, and applies settings changes live. The `server` package *is* the whole
runtime: one process, one embedded frontend, one JSON API, no external services.

## Two modes from one binary

There is a single persisted file - `~/.filefin.json` - holding the port, the bind address, the
data dir, the user accounts, and all settings. The mode switch is **whether an admin account
exists** (`SetupComplete()`), not merely whether the file is present: a config that holds only
a port, bind address, and a one-time setup token (no users) is a normal **pending** state that
keeps the server in install mode. See `install.md` for the full absent -> pending -> complete
lifecycle, the setup token, and the CLI. The cache is always local SQLite (see `library.md`),
so there is nothing else to provision.

```mermaid
flowchart TD
    START[Run] --> EX{setup complete?<br/>(any users)}
    EX -->|no| INS[install mode: serve the token-gated setup on the pending port]
    EX -->|yes| APP[app mode: serve GUI + API on the configured port]
    INS -->|POST /api/install: create admin, clear token| REL[reload signal]
    REL --> APP
```

The server loop owns this: it loads the config (if any), starts the background workers, binds
an HTTP listener at `bindAddress:port`, and serves until a **reload signal** arrives - at which
point it shuts the current listener and re-binds from the freshly loaded config. That signal is
how completing setup swaps the server into app mode **without a restart**: `POST /api/install`
creates the admin user, clears the setup token, writes the config, then fires reload and the
loop rebuilds the handler in app mode (the port was already fixed when the pending config was
bootstrapped, so it does not change here).

## Routes by mode

The router is rebuilt per bind. `/api/state` is always present. The token-gated install routes
(`/api/install`, `/api/install/browse`) are mounted **only while setup is pending** and
disappear once it is complete; the authenticated end-user and admin routes mount **only** once
setup is complete. Everything else falls through to the SPA handler, which serves the embedded
Svelte build and falls back to `index.html` for client-side routes, so the app works fully
offline with no external assets - and so a completed server answers a stale `/api/install` with
the SPA rather than an installer.

## Auth

Accounts live in the config as bcrypt hashes; the username is an email. Login verifies the
hash, rejects a **blocked** account (with the same 401 as a bad password, so a block leaks
nothing), records the last-login time, and creates an in-memory session handed back as an
`HttpOnly` cookie; sessions are cleared on restart (there is no persistent session store).

Login is hardened against guessing and enumeration. Every attempt spends the same time in
one bcrypt compare - an unknown or blocked account is checked against a fixed dummy hash - so
timing never reveals whether an account exists. A throttle locks an account (and, separately,
a source IP) after too many failures within a sliding window, returning `429 Too Many
Requests` until the short lockout lifts; a correct login clears the account's counter. The
client IP is taken from `X-Forwarded-For` only when the immediate peer is loopback (the
co-located proxy), so it cannot be spoofed. Passwords have a minimum length and a 72-byte
ceiling (bcrypt ignores bytes past 72) enforced on install, create, and change.

The session cookie is `HttpOnly` + `SameSite=Lax`, and carries `Secure` when the request
arrived over TLS or through a loopback proxy that set `X-Forwarded-Proto: https` - so a
production instance behind Caddy gets `Secure` while a plain-HTTP LAN box is not silently
logged out. HSTS belongs at the TLS edge (Caddy), not the app.

```mermaid
flowchart TD
    L[POST /api/login] --> T{throttled? account/IP over limit}
    T -->|yes| U429[429 + Retry-After]
    T -->|no| V{constant-time hash match and not blocked?}
    V -->|no| U401[401 + record failure]
    V -->|yes| SES[clear counter, stamp last login, create session -> cookie]
    REQ[any app route] --> AUTH[auth: valid session cookie?]
    AUTH -->|no| R401[401]
    AUTH -->|yes| ADM{admin route?}
    ADM -->|no| OK[handler]
    ADM -->|admin user?| OK
    ADM -->|not admin| F403[403]
```

Every response carries a baseline set of security headers (a strict `Content-Security-Policy`
with no `unsafe-inline`, since the bundled frontend ships only external hashed assets;
`X-Content-Type-Options: nosniff`; `X-Frame-Options: DENY`; `Referrer-Policy`;
`Permissions-Policy`). The `http.Server` sets a read-header and idle timeout (Slowloris guard)
while leaving the body read/write timeouts open so large uploads and long video responses are
never truncated.

Two middlewares wrap handlers: `auth` requires a valid session and stashes the username;
`admin` additionally requires the user be flagged admin, and lazily ensures the cache is built
on entry (best-effort - admin pages still work if the cache is down). Every `/api/admin/*`
route is behind `admin`, so the gating is enforced server-side regardless of the UI; the SPA
mirrors it by hiding the Library/Admin toggle and the admin nav from non-admins.

## User accounts

The full account record lives in the config (the source of truth): `id`, the email username
(map key), `alias`, `admin`, `blocked`, `createdAt`, `lastLoginAt`, and the bcrypt `hash`. A
disposable SQLite `users` table mirrors this; its **only load-bearing job is minting the
auto-increment `id`**, which is written back into the config. On cache build the mirror is
reconciled from the config: existing accounts are re-seeded at their stored id, and any account
without an id yet (the install admin on first run) has one minted and saved back. So ids are
stable across a cache wipe, and the cache stays fully rebuildable from the config.

The admin **Users** page (`/admin/users`) lists every account and can add a user, edit an
alias, grant/revoke admin, and **block/unblock** (the moderation primitive - there is no hard
delete). Blocking **or changing a password** drops the account's active sessions immediately,
so a password reset actually locks out anyone holding an old session rather than leaving it
valid until restart. Guardrails refuse any change that would lock the install out: an admin
cannot block or de-admin their own account, and no change may leave zero **active** (admin and
not blocked) admins. The first user, created at install, is admin.

```mermaid
flowchart TD
    NEW[POST /api/admin/users] --> MINT[InsertUser -> auto id from SQLite]
    MINT --> CFG[write account into config with that id + Save]
    BUILD[cache build] --> REC[reconcile: re-seed mirror from config,\nmint + save any id-less account]
```

## Live settings, no restart

Almost every setting applies in place without rebinding the listener; only the install-time
port choice uses the reload path. Each settings handler goes through one shared `mutateConfig`
helper: it applies the change to a **copy** of the live config, persists that copy, and
publishes it only on a successful save - so a failed write needs no manual rollback (the live
config was never touched), and published configs are never mutated in place. The save itself
is atomic (temp file + rename via the shared `fsutil` helper, mode `0600`). After the swap the
handler pushes the change into the relevant live component:

| setting change | applied by |
|----------------|------------|
| logging level / output | reconfigure the live logger in place (a bad output keeps the current destination, only the level applies, so a typo never silences the app) |
| transcoding on/off, ffmpeg/ffprobe, hardware accel | discard the HLS manager so the next playback re-detects paths/encoder (see `playback.md`) |
| optimizer mode | signal the optimizer supervisor to cancel and relaunch its agents (see `agents/optimizer.md`) |
| discovery interval | signal the discovery supervisor to re-arm its ticker, or idle when off (see `agents/discovery.md`) |
| import folder, OMDb key, media format, subtitle language | stored in config; read on next use by import / enrichment / library |

The admin **Settings** page groups these into tabs (System, Library, Playback, Automation,
Logging, Maintenance). The System tab is read-only install facts (port, data folder, cache
path, media format, user count) plus a live discovery status (the scheduler exposes the next
sweep time, rendered as "Off" or a "next run in ..." countdown); the editable tabs bind to a working copy of the config and
expose one dirty-aware Save per tab that dispatches only the changed sub-groups to the
endpoints above (so one Save may issue one or two POSTs, or none). The whole-library
operations (the four agent re-scans and the cache rebuild) live on the Maintenance tab, not
among the settings; rebuild sits in a confirm-gated danger zone.

## Request/response helpers

The JSON API leans on a few shared `server` helpers so each handler stays thin and uniform:

- **`decodeJSON[T]`** decodes a request body into a typed value behind an `http.MaxBytesReader`
  cap, so no handler streams an unbounded body into memory and the 1 MiB limit is set once.
- **typed responses** - every endpoint returns a named struct (or generic `queueStatus[T]` /
  `scanResult`) fed to `writeJSON`, rather than ad-hoc `map[string]any`, so the wire shape is
  defined by Go types the compiler checks.
- **`bestEffort`** logs-and-swallows the deliberately non-fatal writes (cache mirrors of the
  source-of-truth config, throttled progress updates), making "this failure is intentionally
  ignored" greppable instead of a bare `_ =`.

## Background workers

Started once at boot and shared across rebinds (they outlive a listener swap): the import
**poller** (drains the imports queue, see `import.md`), the **optimizer** supervisor (see
`agents/optimizer.md`), the single **enrichment** and **thumbnail** agents (see `agents/enricher.md`,
`agents/thumbnailer.md`), and the **discovery** supervisor (the timer-driven reconcile + health sweep,
see `agents/discovery.md`). Live per-task progress for imports and optimize encodes is kept in
in-memory maps on the server and mirrored to the cache, so the Progress page can poll fresh
values.

## Admin dashboard

The dashboard is the admin landing page. `GET /api/admin/summary` aggregates a cheap overview
in one call - it derives everything from the cache plus the in-memory config and keeps no
long-lived state:

```
{ library:   { categories, media, files },
  users:     { total, admins },
  optimizer: { mode, pending, active },
  enrich:    { pending },
  imports:   { active },
  health:    { issues, unchecked, lastSweep, discovery } }
```

## Logging

One structured logger, reconfigurable live. Events are grouped by facet (`backend`,
`frontend`, `import`, `optimizer`, `enrich`, `thumbnail`, `discovery`) and carry telemetry in
structured fields rather than the message text. The default before any config is info level to
stdout.

## Endpoints

| method + path                    | purpose                                         |
|----------------------------------|-------------------------------------------------|
| `GET  /api/state`                | whether first-run setup is still needed (never exposes the token) |
| `POST /api/install`              | first-run: token-gated create admin, clear token, rebind (pending only) |
| `GET  /api/install/browse`       | pick a data folder (pending only, token-gated)  |
| `POST /api/login` / `logout`     | session create / destroy                        |
| `GET  /api/me`                   | current user + admin flag + alias               |
| `GET  /api/admin/summary`        | dashboard overview (library/users/optimizer/enrich/imports/health) |
| `GET  /api/admin/health`         | list items flagged with issues (see `agents/discovery.md`) |
| `POST /api/admin/discovery/run`  | trigger an immediate discovery sweep            |
| `GET  /api/admin/users`          | list accounts (id, email, alias, admin, blocked, timestamps) |
| `POST /api/admin/users`          | create an account (mints id, writes config)     |
| `PUT  /api/admin/users/{id}`     | edit alias / admin / blocked / password         |
| `GET  /api/admin/settings`       | read settings                                   |
| `POST /api/admin/settings/*`     | apply a setting live (see table above)          |
