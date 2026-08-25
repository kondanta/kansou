binary          := "kansou"
main            := "."
swag_out        := "docs/swagger"
tribbie_version := `cat TRIBBIE_VERSION`

# List available recipes
default:
    @just --list

# Build the Go binary only (no UI rebuild)
build:
    mise x -- go build -o {{binary}} {{main}}

# Download the pre-built tribbie UI from its GitHub release
build-ui:
    gh release download "{{tribbie_version}}" --repo sasalx/tribbie --pattern "tribbie-{{tribbie_version}}.zip" --output /tmp/tribbie.zip --clobber
    mkdir -p web/dist
    unzip -o /tmp/tribbie.zip -d web/
    touch web/dist/.gitkeep
    rm /tmp/tribbie.zip

# Build the Vue UI from tribbie HEAD via Docker — for local testing against latest
build-ui-head:
    @if [ -d web/tribbie/.git ]; then \
        git -C web/tribbie fetch --depth 1 origin HEAD && git -C web/tribbie reset --hard FETCH_HEAD; \
    else \
        rm -rf web/tribbie && git clone --depth 1 https://github.com/sasalx/tribbie web/tribbie; \
    fi
    docker run --rm \
      -v "$(pwd)/web/tribbie:/app" \
      -v "$(pwd)/web/dist:/app/dist" \
      -w /app \
      node:22-alpine \
      sh -c "corepack enable pnpm && pnpm install && VITE_API_BASE_URL= pnpm exec vite build"
    touch web/dist/.gitkeep

# Full build: Vue UI then Go binary
build-all: build-ui build

# Build with version stamped from the nearest git tag (clean semver, no commit hash)
build-release:
    mise x -- go build -ldflags "-X github.com/kondanta/kansou/cmd.version=$(git describe --tags --abbrev=0 2>/dev/null || echo dev)" -o {{binary}} {{main}}

# Run all tests
test:
    mise x -- go test ./...

# Run all tests bypassing the result cache (always re-executes, e.g. after
# toggling external state like the Docker daemon that Go's cache can't see)
test-local:
    mise x -- go test -count=1 ./...

# Run tests with verbose output
test-v:
    mise x -- go test -v ./...

# Run tests with race detector
test-race:
    mise x -- go test -race ./...

# Run tests with race detector, bypassing the result cache
test-race-local:
    mise x -- go test -race -count=1 ./...

# Run go vet
vet:
    mise x -- go vet ./...

# Regenerate Swagger docs (run after any handler change)
swagger:
    mise x -- swag init -g main.go --output {{swag_out}}

# Run the linter
lint:
    mise x -- golangci-lint run ./...

# Run the full definition-of-done check: build + test (race) + vet + lint
ci: build test-race vet lint
    @echo "✓ all checks passed"

# Same as `ci` but forces a fresh, uncached test run
ci-local: build test-race-local vet lint
    @echo "✓ all checks passed (fresh, no test cache)"

# Remove build artifact
clean:
    rm -f {{binary}}

# Run the CLI (pass args after --)
run *args:
    mise x -- go run {{main}} {{args}}

# Start the REST server in development mode
serve:
    mise x -- go run {{main}} serve
