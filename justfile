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
