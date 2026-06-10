# Media enricher

How existing media gets its rich metadata and poster from OMDb, after it is already in the
library. Import never calls OMDb (see `../import.md`): every folder lands unenriched - a
folder/upload stub (title/year), or a Plex folder carrying Plex's own metadata - and this
subsystem fills the gaps later without re-importing the file. The fill is **additive**: it
never overwrites a value the import already wrote, so Plex's metadata (and any imported
poster) survives and only the holes are completed.

## The enriched flag is the whole signal

Every `media` row carries an `enriched` flag. Import always leaves it unset (no source is
enriched at import time); the enricher sets it once it has merged an OMDb result in. The
durable record is the media folder's `meta.json` on disk - the `enriched` column is just a
cache mirror, re-derived on a rebuild (a folder whose `meta.json` is enriched, or merely
carries an `imdbID`, counts as enriched so it is never needlessly re-queued).

The enricher turns unenriched media into a work queue, drains it one lookup at a time, and
writes the result back to both disk and cache.

```mermaid
flowchart TD
    SCAN[Enrich scan pressed] -->|UnenrichedMedia| Q[(enrich_tasks queue)]
    SCAN -->|prune tasks for since-enriched folders| Q
    Q -->|single agent, claims one task| A[Enrich agent]
    A -->|OMDb lookup by title + year| O{match?}
    O -->|no / API error| ERR[task = error, left for admin]
    O -->|yes| W[merge OMDb into meta.json additively, flag enriched]
    W --> P[download poster only if folder has none]
    P --> M[SetMediaEnriched on cache row]
    M --> DONE[task deleted]
```

## One rate-limited agent

Unlike the optimizer's elastic pool, enrichment runs a **single agent for the process
lifetime** (started once, never cancelled). It rests briefly between lookups so OMDb is not
hammered, and idles when there is no work, no config, or no API key configured. A task
interrupted by a restart is reset from `enriching` back to `pending` on first recovery, so
nothing is lost across restarts.

The queue is **transient cache state**: it is refilled by the shared scanner - run on demand
by the "Enrich scan" button and on a timer by the discovery agent (see `discovery.md`) -
which queues a task per still-unenriched media folder and prunes tasks for folders enriched
since. A `not-found` or API error fails the task and leaves it
visible to the admin rather than silently retrying; only a poster download failure is
tolerated (the media still counts as enriched, keeping any existing poster).

## Other-media is never enriched

A category flagged **other media** (home videos / recordings) is skipped entirely: its
folders would not match OMDb, so there is no title lookup, no text fields, and no poster
download. The scan resolves each media item's owning-category **effective** flag once (the
root-propagated value stored on every category cache row - see `../mediaformat.md`) and never
queues an other-media folder; as a belt-and-suspenders guard the agent also finishes any
other-media task without a lookup should a stale one slip through. A media item in a
**sub-category** therefore inherits its root category's flag. Posters for these folders come
from the thumbnail agent's frame-extraction path instead (see `thumbnailer.md`).

## What a successful enrich writes

| target | write |
|--------|-------|
| `meta.json` | the OMDb result **merged additively** into the existing file (existing values win), flagged `enriched`, **keeping** the ffprobe `technical` block written at import time, and **preserving** the per-user `state` object (see `../playback-state.md`) - the write goes through the shared per-folder lock (`importer.Manager.Update`) so a concurrent playback event is never dropped |
| `poster.*` | downloaded into the media folder **only when the folder has no poster** and OMDb returns one; an existing poster is never overwritten |
| `media` cache row | description, plot, and poster name updated; `enriched` set |

## Dependencies

- **OMDb client** (`omdb`) - the same small client the importer uses; enrichment is gated on
  the OMDb API key set in Settings. With no key, the agent simply idles. Lookups and poster
  downloads take the request `context`, so a shutdown interrupts an in-flight HTTP call.
- **meta.json format** (`importer.MetaFromOMDb` / `MergeMeta` / `ReadMeta` / `WriteMeta`) -
  shared with the importer so both halves write the identical on-disk shape; `MergeMeta` is
  what keeps enrichment additive. `MetaFromOMDb` shares one `metaBuilder` with the Plex and
  Jellyfin meta-builders, so all three assemble the metadata/ratings maps the same way.
- **db (shared task queue)** - `enrich_tasks` is one instance of the generic queue helper
  shared with the optimizer and thumbnailer (see `optimizer.md`).
- **ffprobe** `technical` block - produced at import time (see `../import.md`); enrichment
  preserves it rather than re-probing.

## Endpoints

| method + path                       | purpose                                              |
|-------------------------------------|------------------------------------------------------|
| `POST /api/admin/enrich/scan`       | queue an enrich task per unenriched media folder     |
| `GET  /api/admin/enrich/active`     | in-flight enrichments + count still pending          |
