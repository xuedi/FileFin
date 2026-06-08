# Task runner.

app := "filefin"

clean:
    rm -f /home/xuedi/.filefin.json

# build everything: the Svelte frontend bundle and the single binary that embeds it
build:
    cd web && npm install && npm run build
    go build -o bin/{{app}} ./cmd/{{app}}

# build, then run the server
run: build
    ./bin/{{app}}

# format, vet, and test
check:
    gofmt -w .
    go vet ./...
    go test ./...

# mount backspace's Plex home (DB + Metadata) and media root read-only, for manual
# Plex-import testing through the GUI. backspace is strictly read-only; this never writes.
plex-mount:
    mkdir -p /tmp/filefin-plex/lib /tmp/filefin-plex/data
    mountpoint -q /tmp/filefin-plex/lib || sshfs -o ro,reconnect "backspace:/var/lib/plex/Plex Media Server" /tmp/filefin-plex/lib
    mountpoint -q /tmp/filefin-plex/data || sshfs -o ro,reconnect backspace:/mnt/data/plex /tmp/filefin-plex/data
    @echo "DB path:      /tmp/filefin-plex/lib/Plug-in Support/Databases/com.plexapp.plugins.library.db"
    @echo "metadata-dir: /tmp/filefin-plex/lib/Metadata"
    @echo "media base:   /tmp/filefin-plex/data"

# unmount the read-only backspace Plex mounts
plex-unmount:
    -fusermount -u /tmp/filefin-plex/data
    -fusermount -u /tmp/filefin-plex/lib
