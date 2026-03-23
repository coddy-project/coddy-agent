.PHONY: build build-acp test lint clean install

VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS := -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(VERSION)

# Build the full binary (TUI + ACP).
build:
	go build -ldflags "$(LDFLAGS)" -o coddy ./cmd/coddy/

# Install to GOPATH/bin.
install:
	go install -ldflags "$(LDFLAGS)" ./cmd/coddy/

# Run all tests.
test:
	go test ./...

# Run tests with verbose output.
test-v:
	go test -v ./...

# Clean build artifacts.
clean:
	rm -f coddy

# Run the linter (requires golangci-lint).
lint:
	golangci-lint run ./...
