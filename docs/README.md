# FileFin architecture

How FileFin is built, one subsystem per file. These documents describe the **general
architecture and how it works** - the flows, the data model, and how the pieces connect -
not the line-by-line code. Diagrams are [Mermaid](https://mermaid.js.org/); GitHub renders
them inline.

Start with [`agents.md`](agents.md) for the background-processing model, then read whichever
subsystem you are working in.

| Document | Subsystem | What it covers |
|----------|-----------|----------------|
| [`agents.md`](agents.md) | Agents overview | Every background agent, the shared task queue, the refill-vs-health split, discovery as the scheduler |
| [`agents/enricher.md`](agents/enricher.md) | Media enricher | Background OMDb re-enrichment queue, `meta.json` + ffprobe, additive merge |
| [`agents/thumbnailer.md`](agents/thumbnailer.md) | Thumbnail agent | Sized WebP posters, frame-derived posters for home media, `?size=` |
| [`agents/optimizer.md`](agents/optimizer.md) | Pre-transcoder | Background `.optimized.mp4` copies, GPU worker + load-driven CPU pool |
| [`agents/probe.md`](agents/probe.md) | Format-probe agent | Backfills/refreshes the true container + codecs onto the cache and `meta.json` |
| [`agents/discovery.md`](agents/discovery.md) | Discovery agent | Timer-driven reconcile, queue refill, and `media_health` checks as a rolling sweep |
| [`import.md`](import.md) | Import | Source front stages, the preCheck page, the import poller, the `imports` table |
| [`playback.md`](playback.md) | Video player | Direct-play (by probed format) vs HLS transcode, subtitles |
| [`mediaformat.md`](mediaformat.md) | Media format & categories | On-disk layout, the `config.json` discriminator, the category tree, probed-format truth |
| [`library.md`](library.md) | Library & cache | The media cache, rebuild, browsing, and naming formats |
| [`playback-state.md`](playback-state.md) | Playback state | Per-user state in `meta.json`: resume pointer, watched flag, favorite |
| [`runtime.md`](runtime.md) | Server runtime | Install mode, port rebind, auth/sessions, live settings, the background agents |
| [`frontend.md`](frontend.md) | Frontend | Svelte + Bulma: app state in context, component tree, routing, player effects |
