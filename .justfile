binary   := "kansou"
main     := "."
swag_out := "docs/swagger"

# List available recipes
default:
    @just --list

# Build the Go binary only (no UI rebuild)
build:
    go build -o {{binary}} {{main}}

# Reset and refetch the tribbie submodule from scratch
reset-submodule:
    rm -rf web/tribbie
    git submodule add --force https://github.com/sasalx/tribbie web/tribbie

# Build the Vue UI via Docker — no local Node/pnpm required
build-ui:
    git submodule update --init --recursive
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
    go build -ldflags "-X github.com/kondanta/kansou/cmd.version=$(git describe --tags --abbrev=0 2>/dev/null || echo dev)" -o {{binary}} {{main}}

# Run all tests
test:
    go test ./...

# Run tests with verbose output
test-v:
    go test -v ./...

# Run tests with race detector
test-race:
    go test -race ./...

# Run go vet
vet:
    go vet ./...

# Regenerate Swagger docs (run after any handler change)
swagger:
    swag init -g main.go --parseDependency --output {{swag_out}}

# Run the full definition-of-done check: build + test + vet
check: build test vet
    @echo "✓ all checks passed"

# Remove build artifact
clean:
    rm -f {{binary}}

# Run the CLI (pass args after --)
run *args:
    go run {{main}} {{args}}

# Start the REST server in development mode
serve:
    go run {{main}} serve

