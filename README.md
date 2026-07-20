# FileFin

[![CI](https://github.com/xuedi/FileFin/actions/workflows/ci.yml/badge.svg)](https://github.com/xuedi/FileFin/actions/workflows/ci.yml)
[![Version](https://img.shields.io/badge/Version-0.18.0-31c754.svg)](https://github.com/xuedi/FileFin/releases)
[![License](https://img.shields.io/badge/License-EUPL_v1.2-31c754.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26+-31c754.svg)](https://go.dev)

A small, self-hosted media server that treats the **filesystem as the single source of truth**.

FileFin grew out of two frustrations with the usual options: Plex blocking access for servers hosted in
some data centers, and Jellyfin being too strict about how media files must be named. FileFin lets you
keep your own naming scheme, keeps all durable data on disk in a human-readable layout, and ships as a
single binary that is both the CLI and the web server.

## Why filesystem-first

Most media servers own your library through a database: the metadata you corrected, the poster you
picked, and how far you watched all live in an application's private storage. FileFin writes every one
of those next to the media, as plain JSON and image files.

- **Your library outlives the app.** Title, plot, cast, poster, resume point, watched flag and rating
  are files inside the media folder. Move that folder to another disk or another machine and it arrives
  complete. Uninstall FileFin and you have lost an index, not a library.
- **Nothing to migrate, nothing to lock in.** Leaving is `cp -r`. Backups are `rsync` or `borg` over
  ordinary files, and a restore is those same files back in place.
- **Keep your own naming.** Categories are plain folders, nested as deep as you like and named whatever
  you want. Recognition reads the names you already have instead of insisting on
  `Show (2001)/Season 01/Show S01E01.mkv`; the only renaming happens when *you* import, into one of
  three layouts you choose.
- **No account, no cloud, no phone-home.** There is nothing to sign in to but your own server, and no
  third party gets to decide that your IP address is the wrong kind. The single optional outbound call
  is OMDb metadata, which needs a key you supply and can be left unset.
- **The index is disposable on purpose.** SQLite is a cache for fast browsing and search, never a record
  of anything. Delete it and one click in Settings rebuilds it from disk; a background agent reconciles
  the two continuously, so drift heals itself instead of accumulating.
- **The filename is never trusted.** Every file is probed for its real container and codecs, so a
  library where everything happens to be called `.avi` still plays correctly.

The server exposes an authenticated API and a small web UI, and streams files with HTTP byte-range
support. The only state kept outside your media folder is a single config file in your home directory.
Your existing media is never modified in normal operation: only setup, importing, the optional
optimizer, and per-user state ever write to the data directory.

For a deeper tour - the background agents, the import pipeline, playback, and the disposable cache - see
the [architecture documentation](docs/).

## Filesystem layout

```
<data-dir>/
├── Films - English/                           # a category: any folder with a config.json
│   ├── config.json                            # marks this folder as a category, and says what belongs in it
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
filefin rename-user OLD NEW   # rename an account (stop the service first); --dry-run to preview
filefin version               # print the release version
```

With no config (`~/.filefin.json`), the first `serve` comes up in **install mode** and logs a token-bearing
setup URL; open it to create the admin account and pick your data directory. After that, everything - media,
categories, users, and settings - is managed from the web UI.

## Features

- **Single binary** - the CLI, web server, and frontend ship in one self-contained executable that runs
  fully offline with no external assets.
- **Import that reads what you already have** - point it at a drop folder and review one row per
  recognised media: title, year, show-or-film verdict, file count, and a confidence marker that names
  what looked wrong. Anything the library already holds is flagged before a byte is copied, and each
  row's target category is preselected from what past imports taught.
- **Import from Plex, Jellyfin, or your browser** - bring an existing library across (Jellyfin/Kodi NFO
  libraries included), or upload files straight from the browser.
- **Metadata three ways** - background OMDb enrichment, a manual search-and-match page for what it
  missed, and a hand-editor for every field with poster upload.
- **Direct streaming with on-demand transcoding** - byte-range direct play decided by the *probed*
  format, falling back to HLS (VAAPI hardware encoding when available) only when the browser really
  cannot play the file.
- **Optional background optimizer** - pre-builds browser-friendly copies so live transcoding is rarely
  needed.
- **Subtitles** - sidecar files ride along on import, embedded text tracks are extracted, and non-SRT
  formats are converted.
- **Multi-user with per-user state** - accounts with admin/block controls; resume points, watched flags,
  favorites, and 1-10 ratings, each stored per user in the item's `meta.json`.
- **Bring your ratings with you** - import a public MyDramaList or MyAnimeList list and apply its
  watched flags and scores to the titles you own.
- **Search the whole library** - by title, cast, genre, director, language, or year, with every facet on
  a detail page doubling as a link into a scoped search.
- **Home videos too** - a category marked *other media* skips metadata lookups, derives its posters from
  a video frame, and offers a fullscreen swipe player.
- **Self-healing** - background agents reconcile the cache against disk, refresh probed formats, build
  sized posters, and report whatever is broken on an admin health page.

## What FileFin is not

No live TV or DVR, no music or photo libraries, no native mobile or TV apps - the web UI is the client.
It is one server for a household, not a cluster. If you need any of that, Jellyfin is the better fit -
and FileFin will happily import from it.

## License

Licensed under the [EUPL v1.2](LICENSE).
