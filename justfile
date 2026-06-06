# Task runner for this project. Run `just` to list recipes.
# All routine dev actions live here so they can be allowed once as `just` commands.

app := "filefin"
bindir := "bin"

# show all recipes
default:
    @just --list

# build the single binary into ./bin
build:
    go build -o {{bindir}}/{{app}} ./cmd/{{app}}

# run the app, passing a subcommand (e.g. `just run serve`)
run *args:
    go run ./cmd/{{app}} {{args}}

# run the full test suite
test:
    go test ./...

# run a subset (e.g. `just test-one ./internal/scanner` or `just test-one -run TestParse ./...`)
test-one *args:
    go test {{args}}

# report suspicious constructs
vet:
    go vet ./...

# format all Go code
fmt:
    gofmt -w .

# lint (golangci-lint if installed, otherwise go vet)
lint:
    #!/usr/bin/env sh
    if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else echo "golangci-lint not found; running go vet"; go vet ./...; fi

# tidy module dependencies
tidy:
    go mod tidy

# full pre-commit gate: format, vet, test
check: fmt vet test

# install + build the Svelte frontend (expects ./web)
web-build:
    cd web && npm install && npm run build

# run the Svelte dev server
web-dev:
    cd web && npm run dev

# remove build output and the local cache db
clean:
    rm -rf {{bindir}}
    rm -f *.sqlite *.sqlite-* *.db
