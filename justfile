# Task runner.

app := "filefin"

clean:
    rm -f /home/xuedi/.filefin.json

# build just the Svelte frontend bundle the binary embeds
frontend:
    cd web && npm install && npm run build

# build everything: the Svelte frontend bundle and the single binary that embeds it
build: frontend
    go build -o bin/{{app}} ./cmd/{{app}}

# build, then run the server
run: build
    ./bin/{{app}}

# install the built binary + hardened systemd unit + service user (source/bare-metal, no distro package)
install: build
    #!/usr/bin/env bash
    set -euo pipefail
    bin=/usr/local/bin/{{app}}
    unit=/etc/systemd/system/{{app}}.service
    nologin=/usr/sbin/nologin
    [ -x "$nologin" ] || nologin=/sbin/nologin
    [ -x "$nologin" ] || nologin=/bin/false
    echo "installing {{app}} -> $bin (elevating with sudo)"
    sudo install -Dm755 bin/{{app}} "$bin"
    getent group filefin >/dev/null 2>&1 || sudo groupadd --system filefin
    getent passwd filefin >/dev/null 2>&1 || sudo useradd --system --gid filefin \
        --home-dir /var/lib/filefin --shell "$nologin" --comment "FileFin media server" filefin
    sudo install -d -o filefin -g filefin -m 0750 /var/lib/filefin
    sed "s#^ExecStart=.*#ExecStart=$bin serve#" packaging/systemd/{{app}}.service | sudo tee "$unit" >/dev/null
    sudo systemctl daemon-reload
    echo
    echo "installed; the unit is disabled. to finish:"
    echo "  1. sudo -u filefin HOME=/var/lib/filefin {{app}} setup --port 80"
    echo "  2. sudo systemctl enable --now {{app}}"
    echo "  3. open the printed setup URL and set the admin account + data folder"

# format, vet, and test
check:
    gofmt -w .
    go vet ./...
    go test ./...

# scan Go dependencies for known vulnerabilities (needs the online vuln database)
sec-vuln:
    govulncheck ./...

# static security-oriented checks: gosec if installed, else go vet as a floor
sec-static:
    @command -v gosec >/dev/null 2>&1 && gosec ./... || (echo "gosec not installed; running go vet as a floor" && go vet ./...)

# audit the bundled frontend dependencies
sec-web:
    cd web && npm audit

# cut a release: verify VERSION matches version.go + the README badge on a clean main, then
# tag vVERSION and push it (the tag-triggered GitHub Action builds and publishes the rest).
release VERSION:
    #!/usr/bin/env bash
    set -euo pipefail
    ver="{{VERSION}}"
    ver="${ver#v}"
    grep -q "\"$ver\"" internal/version/version.go || { echo "internal/version/version.go is not at $ver"; exit 1; }
    grep -q "Version-$ver-" README.md || { echo "the README badge is not at $ver"; exit 1; }
    branch="$(git rev-parse --abbrev-ref HEAD)"
    [ "$branch" = "main" ] || { echo "not on main (on $branch)"; exit 1; }
    [ -z "$(git status --porcelain)" ] || { echo "working tree is dirty; commit first"; exit 1; }
    git tag "v$ver"
    git push origin "v$ver"
    echo "pushed tag v$ver - watch the Release workflow for the published artifacts"

# local dry run of the whole packaging pipeline: builds the binary and every distro package
# into ./dist without publishing (needs goreleaser on PATH)
release-snapshot:
    goreleaser release --snapshot --clean
