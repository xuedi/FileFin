# FileFin

A small, self-hosted media server that treats the **filesystem as the single source of truth**.

FileFin grew out of two frustrations with the usual options: Plex blocking access for servers hosted in
some data centers, and Jellyfin being too strict about how media files must be named. FileFin lets you
keep your own naming scheme, keeps all durable data on disk in a human-readable layout, and ships as a
single binary that is both the CLI and the web server.

## Design in one paragraph

Your media lives on disk in a simple, fixed layout. FileFin scans it into a disposable SQLite index used
only for fast browsing and search - delete the index any time and `rebuild` reconstructs it from the
filesystem with no data loss. The server exposes an authenticated API and a small web UI, and streams
files directly with HTTP byte-range support. The only state FileFin keeps outside your media folder is a
single config file in your home directory. FileFin never modifies your media directory during normal
operation; only the explicit import commands and `setup` ever write there.

## Filesystem layout

```
<data-dir>/
├── Films - English/                       # a category (free-form label)
│   └── (1980) The Gods Must Be Crazy/      # a media folder: "(YYYY) Title"
│       ├── (1980) The Gods Must Be Crazy.avi
│       ├── poster.jpg                       # optional
│       ├── banner.png                       # optional
│       └── meta.md                          # optional metadata
└── Shows - English/
    └── (2002) Firefly/
        ├── (2002) Firefly - 1x1.avi         # "(YYYY) Title - SxE"
        ├── (2002) Firefly - 1x2.avi
        └── meta.md
```

There is no distinction between films and TV shows: a media folder simply holds one or more media files,
whether that is a single film, a film series, or a multi-episode show. See `meta.md` in this repo for a
worked example of the per-media metadata file.

## Requirements

- Go 1.24+
- Node.js + npm (to build the web frontend)
- [`just`](https://github.com/casey/just) (optional, for the task recipes below)

## Build

```sh
just web-build   # build the Svelte frontend into web/dist
just build       # compile the single binary into ./bin/filefin
```

Without `just`:

```sh
cd web && npm install && npm run build && cd ..
CGO_ENABLED=0 go build -o bin/filefin .
```

The binary embeds the built frontend, so the result is fully self-contained.

## Usage

```sh
# one-time setup: create the data directory and write the config (prompts for an admin login)
filefin setup /path/to/media

# copy a file into the library (flags must come before positional args)
filefin import "Films - English" "/downloads/(1999) The Matrix.mkv"

# check the library is well-formed (read-only)
filefin validate

# rebuild the cache index from the filesystem
filefin rebuild

# run the server (default http://localhost:8080)
filefin serve
```

`import` identifies the title, year, and (for shows) season/episode from the filename, supporting both
`(1999) The Matrix.mkv` and release-style `The.Matrix.1999.1080p.mkv` names. It copies the file into the
canonical `(YYYY) Title/` layout and writes a `meta.md` for new media folders.

## Configuration

Configuration lives in `~/.filefin.md` (created by `setup`): a hand-editable markdown file holding the
data directory, server port, API keys, and user accounts (passwords are stored as bcrypt hashes).

### Optional: metadata enrichment

If you add an [OMDb API](https://www.omdbapi.com) key to the config, `import` will fill `meta.md`
(description, runtime, director, cast, genres as tags, ...) and download a poster automatically:

```markdown
## apikeys
 - omdb: YOUR_OMDB_KEY
```

Pass `--no-fetch` to skip the lookup for a single import.

## Status

FileFin is an early MVP. Working today:

- `setup`, `validate`, `rebuild`, `serve`, and `import` (with optional OMDb enrichment)
- filesystem scan → SQLite cache → authenticated API → embedded web UI
- direct-play streaming with byte-range support

Not yet implemented:

- transcoding (the web player currently direct-plays browser-native containers such as `.mp4`/`.webm`)
- `plex` and `jellyfin` library importers
- configurable naming scheme

## License

Licensed under the [EUPL v1.2](LICENSE).
