binary   := "kansou"
main     := "."
swag_out := "docs/swagger"

# List available recipes
default:
    @just --list

# Build the binary
build:
    go build -o {{binary}} {{main}}

# Build with version stamped from git tag
build-release:
    go build -ldflags "-X github.com/kondanta/kansou/cmd.version=$(git describe --tags --always --dirty)" -o {{binary}} {{main}}

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
