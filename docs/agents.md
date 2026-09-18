# Agents overview

FileFin does its background work through a small set of **agents**. An *agent* is a
queue-draining background processor: it claims one unit of work at a time and runs for the
process lifetime. A *worker* is a concurrency sub-unit **inside** one agent (only the
optimizer has them - its GPU and elastic CPU workers). This file is the map; each agent has
its own page under `agents/`, and the import poller is documented with the rest of import in
`import.md`.

## The agents

| agent | drains | does | page |
|-------|--------|------|------|
| **import poller** | `imports` (status `import`) | copy a staged source into the library, probe it, write `meta.json` + `media`/`media_files` | `import.md` |
| **enricher** | `enrich_tasks` | fill rich OMDb metadata + poster into an unenriched folder, additively | `agents/enricher.md` |
| **thumbnailer** | `thumbnail_tasks` | build sized WebP poster variants; extract a frame poster for other-media | `agents/thumbnailer.md` |
| **optimizer** | `optimize_tasks` | pre-build a browser-direct-play `.optimized.mp4` copy (GPU + elastic CPU workers) | `agents/optimizer.md` |
| **probe** | `probe_tasks` | refresh each file's true container/codecs onto the cache + `meta.json` | `agents/probe.md` |
| **TMDb matcher** | `tmdb_tasks` | record each item's TMDb snapshot (by IMDb id, or a year-strict title search) and re-merge the sources | `agents/tmdb.md` |
| **people** | `people_tasks` | resolve an item's cast on TMDb by its IMDb id, store each person's photo once in `.people/` | `agents/people.md` |
| **discovery** | (timer, no queue) | reconcile cache vs disk, refill the six queues, run health checks, repair missing subtitle sidecars | `agents/discovery.md` |

## The shared task queue

Every queue except discovery (which has none) and the import poller (which drains the richer
`imports` table) is one instance of a single generic helper, `taskQueue` in
`internal/db/taskqueue.go`. It provides the byte-for-byte identical operations each queue
needs - race-free **claim** (select-oldest-pending then flip to active in one transaction,
made atomic by the single-connection cache), **finish**, **fail** (leave an error row for the
admin), **prune** (drop pending/error tasks for a now-complete item), **reset-to-pending** (at
startup, so a task whose agent died mid-run is retried), and **clear-all** (for a rebuild).
Only the table name and status strings differ per queue. The enrich/thumbnail/probe/TMDb/people agents are
near-identical loops over this helper; the optimizer adds its own worker scaling on top.

## Refill vs. health: two kinds of "needs attention"

The work an agent does splits cleanly in two, and discovery is what keeps both current:

- **Refill (auto-fixable).** Needs enrich / thumbnail / optimize / probe / TMDb / cast work. The fix is
  automatic, so it is never recorded as a health "issue" - the shared scanner just enqueues a
  task and the relevant agent handles it. The same refill logic backs both the manual scan
  buttons and the discovery agent.
- **Health (not auto-fixable).** A missing/unparseable `meta.json`, a folder with no video, a
  listed file gone or zero-byte, a referenced poster gone, an orphaned optimizer copy or sized
  poster. These the agent cannot fix; they are recorded and surfaced to the admin.
- **Repair (fixed in place, no queue).** A video with subtitles only inside its container and
  no `.srt` sidecar beside it. The fix is one local ffmpeg extraction, too cheap to queue and
  too fixable to report, so the discovery sweep just does it.

The admin Progress page is built on the same split: a pass with a beginning and an end on one
side, the work it produces on the other. See `progress.md`.

A `meta.json` lacking its technical block is **refill** (the probe agent backfills it); a
`meta.json` that is missing or corrupt is **health** (surfaced, never fabricated). See
`agents/probe.md` and `agents/discovery.md`.

## Discovery is the scheduler

Each agent has a manual "scan" button, but pressing buttons is optional: the **discovery
agent** runs on an admin-chosen interval and, every tick, reconciles the cache against the
filesystem and then runs the **same** refill logic for the optimize, enrich, thumbnail,
probe, TMDb, and people queues. So discovery is the scheduler that feeds the other queue-draining agents; the
agents themselves just drain whatever the queue holds, whether a button or discovery filled
it.

```mermaid
flowchart TD
    DISC[discovery agent\n（timer）] -->|reconcile cache vs disk| CACHE[(media / media_files)]
    DISC -->|refill| QE[(enrich_tasks)]
    DISC -->|refill| QT[(thumbnail_tasks)]
    DISC -->|refill| QO[(optimize_tasks)]
    DISC -->|refill| QP[(probe_tasks)]
    DISC -->|refill| QM[(tmdb_tasks)]
    DISC -->|refill| QC[(people_tasks)]
    DISC -->|health checks| HEALTH[(media_health)]
    BTN[manual scan buttons] -->|same refill logic| QE & QT & QO & QP & QM & QC
    QE --> AE[enricher]
    QT --> AT[thumbnailer]
    QO --> AO[optimizer\nGPU + CPU workers]
    QP --> AP[probe agent]
    QM --> AM[TMDb matcher]
    QC --> AC[people agent]
    IMPORT[import poller] -->|writes rows| CACHE
```

## Dependencies and responsibilities

The agents share a few packages and a strict ordering of who writes what. Import creates a
folder; the others refine it. The probe agent owns the *format* truth; the enricher owns the
*metadata* truth; the thumbnailer owns *poster variants*; the optimizer owns the *direct-play
copy*. None overwrites another's output.

```mermaid
flowchart LR
    subgraph shared[shared packages]
        TQ[db taskQueue]
        FP[ffprobe]
        FR[ffrun]
        TR[transcode]
        MM[importer.Manager\nper-folder meta lock]
    end
    POLL[import poller] --> FP & MM
    ENR[enricher] --> TQ & MM
    THM[thumbnailer] --> TQ & FR
    OPT[optimizer] --> TQ & FR & TR
    PRB[probe agent] --> TQ & FP & MM & TR
    DSC[discovery] --> TQ
```

- **`importer.Manager`** serializes every `meta.json` write (importer, enricher, probe, and
  the playback-state handlers) through one per-folder lock, so no two agents drop a section.
- **`ffprobe`** is the single `-show_format -show_streams` decode used by import, the probe
  agent, and live playback.
- **`ffrun`** is the shared ffmpeg subprocess runner behind the optimizer, thumbnailer, and
  live HLS streamer.
- **`transcode`** holds the `DirectPlayable` / remux rules the probe agent's output feeds and
  the optimizer and playback both consume.
