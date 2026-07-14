# FileFin

[![CI](https://github.com/xuedi/FileFin/actions/workflows/ci.yml/badge.svg)](https://github.com/xuedi/FileFin/actions/workflows/ci.yml)
[![Version](https://img.shields.io/badge/Version-0.10.1-31c754.svg)](https://github.com/xuedi/FileFin/releases)
[![License](https://img.shields.io/badge/License-EUPL_v1.2-31c754.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26+-31c754.svg)](https://go.dev)

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
single config file in your home directory. FileFin never modifies your existing media during normal
operation; only first-run setup, importing, the optional optimizer, and per-user watch-state tracking
(kept in each item's `meta.json`) ever write to the data directory.

For a deeper tour of how it works - the background agents, the import pipeline, playback, and the
disposable cache - see the [architecture documentation](docs/).

## Filesystem layout

```
<data-dir>/
├── Films - English/                           # a category: any folder with a config.json
│   ├── config.json                            # marks this folder as a category
│   └── (1980) The Gods Must Be Crazy/         # a media folder: "(YYYY) Title"
│       ├── (1980) The Gods Must Be Crazy.avi  # the media file
│       ├── poster.jpg                         # optional
│       └── meta.json                          # title, fields, technical info, per-user state
└── Shows - English/
    ├── config.json
    └── (2002) Firefly/
        ├── (2002) Firefly - 1x1.avi           # "(YYYY) Title - SxE"
        ├── (2002) Firefly - 1x2.avi
        └── meta.json
```

A folder is a category when it contains a `config.json`; otherwise a folder holding one or more video
files is a media item. Categories nest to any depth. There is no distinction between films and TV shows: a
media folder simply holds one or more media files, whether that is a single film, a film series, or a
multi-episode show.

## Installation

.deb, .rpm, and Arch packages are on the [latest release](https://github.com/xuedi/FileFin/releases) -
install the one for your distro with its package manager. No package? Build from source: 

```sh
git clone https://github.com/xuedi/FileFin                     # clone repo
cd FileFin                                                     # enter folder
just install                                                   # build binary (need: golang, node & npm)
sudo -u filefin HOME=/var/lib/filefin filefin setup --port 80  # follow printed instructions (web installer)
sudo systemctl enable --now filefin                            # enable & start the service
```

Running behind a reverse proxy, without systemd, or curious about the token flow? See
[docs/install.md](docs/install.md).

## Usage

FileFin is one binary with a tiny CLI; everything after setup is driven from the web UI.

```sh
filefin serve                 # run the server (the default command)
filefin setup --port 80       # prepare a pending install and print the setup URL
filefin version               # print the release version
```

With no config (`~/.filefin.json`), the first `serve` comes up in **install mode** and logs a token-bearing
setup URL; open it to create the admin account and pick your data directory. After that, everything - media,
categories, users, and settings - is managed from the web UI.

## Features

- **Filesystem as source of truth** - readable on-disk layout; the SQLite index is a disposable cache you
  can delete and rebuild at any time with no data loss.
- **Keep your own naming** - free-form categories that nest to any depth; no rigid file-naming rules.
- **Single binary** - the CLI, web server, and frontend ship in one self-contained executable that runs
  fully offline with no external assets.
- **Direct streaming with on-demand transcoding** - HTTP byte-range direct play, falling back to HLS
  transcoding (VAAPI hardware encoding when available) for non-browser-native formats.
- **Optional background optimizer** - pre-transcodes media to browser-friendly copies to avoid live
  transcoding.
- **External subtitle support** - imports and serves sidecar `.srt` subtitles in the player.
- **OMDb enrichment** - background fetch of titles, posters, and metadata, with frame-derived posters for
  home videos and recordings.
- **Multi-user with per-user state** - accounts with admin/block controls, resume points, watched flags,
  and favorites.
- **Import from Plex and Jellyfin** - bring an existing library across into FileFin's layout.

## License

Licensed under the [EUPL v1.2](LICENSE).
