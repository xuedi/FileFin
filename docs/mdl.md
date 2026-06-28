# MyDramaList import

How a user pulls their MyDramaList (MDL) watch history and 1-10 ratings into FileFin. MDL's
official API is closed to the public, so FileFin reads the **public** profile list page and
matches it against the local library. The flow is user-driven and explicitly confirmed: nothing
is written until the user reviews the proposed matches.

## Why scraping, and the limits that follow

MDL issues no public API keys, so there is no authenticated data feed to call. The one openly
readable surface is a member's public list page (`mydramalist.com/dramalist/{username}`), which
server-renders a table per status bucket with each title, its year, and the member's score. That
shapes the whole subsystem and its limits:

- Only **public** lists are readable; a private or empty list yields nothing.
- Parsing depends on MDL's HTML and is therefore **fragile** - an upstream markup change breaks it,
  which an offline fixture test exists to catch early.
- MDL titles and on-disk titles rarely agree exactly, so matching is **approximate** and always
  goes through a review step.

## The flow

```mermaid
flowchart TD
    U["user settings: MyDramaList"] -->|save username| P[(config.User.MDLUsername)]
    U -->|Import| PV["POST /api/mdl/preview"]
    PV --> F["fetch public list page"]
    F --> PARSE["parse status tables -> entries<br/>(title, year, 1-10 rating, status)"]
    PARSE --> M["match entries to library<br/>by normalized title (+ year)"]
    M --> R["review table:<br/>matched (selectable) + unmatched"]
    R -->|Confirm| AP["POST /api/mdl/apply"]
    AP --> W["per item: write rating<br/>+ watched via UpdateState"]
    W --> MJ[(meta.json state)]
```

- The MDL username is a **per-user** profile field on `config.User`, saved by the user themselves
  through `POST /api/profile/mdl` (auth-gated, not admin-gated) and echoed back by `GET /api/me`.
- **Preview** scrapes and matches synchronously - a list is a single page fetch plus an in-memory
  match against the media cache, so it needs no background agent or queue. It returns the matched
  proposals (each with the library title, the MDL title, the rating, and whether it would mark the
  item watched) and the MDL titles that found no library item. It writes nothing.
- Parsing walks the page in document order: each status label sets the current bucket, and every
  following row with a title cell becomes an entry in it. A score of `0.0` means unrated.
- **Status -> state**: `Completed` marks the item watched; the 1-10 rating is imported for any
  status that carries one. The two are independent - a rated but dropped title imports only the
  rating. MDL's half-point scores are rounded to the nearest integer.
- **Apply** takes only the rows the user confirmed and writes each through the same per-folder
  `meta.json` path every other state writer uses (see [`playback-state.md`](playback-state.md)),
  so a rating import can never drop anyone's resume pointer or the OMDb metadata. Re-running is
  idempotent.

## Matching

Matching is a shared, source-neutral matcher (the MyAnimeList importer in [`mal.md`](mal.md) uses
the same one, so the year-strict rules live in one place). Both sides are normalized to lowercase
alphanumerics (punctuation and spacing dropped, `&` folded to `and`) and paired on that key. An
entry may also carry alternative titles (aliases) to try when its primary title matches nothing;
MyDramaList carries a single title, so it brings none.

The **year is part of the decision**, which stops a shared normalized title (an anime "Kingdom" and
a Korean drama "Kingdom") from auto-selecting the wrong release:

- When a candidate's year equals the entry's year, it is picked and flagged **exact**.
- Otherwise the candidate whose year is **closest** is offered (not blindly the first), flagged
  **approximate**, and the review table shows `approx: year x != y`.
- Only exact rows are **pre-selected**; approximate rows arrive **unchecked**, so a year-mismatched
  match is never applied without a deliberate tick.

Unmatched titles are listed so the user can see what was skipped.

The preview optionally restricts its candidates to a **category** and its descendants. Scoping a
list to one category is the structural fix for cross-title collisions - a list scoped to a category
never sees a same-titled item filed elsewhere.
