.PHONY: build build-acp test lint clean install

VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS := -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(VERSION)

record:
	~/go/bin/vhs assets/demo.tape 2>&1

# Build the full binary (TUI + ACP).
build:
	go build -ldflags "$(LDFLAGS)" -o coddy ./cmd/coddy/

# Install binary: /usr/local/bin for root, ~/.local/bin for regular users.
INSTALL_DIR := $(if $(filter 0,$(shell id -u)),/usr/local/bin,$(HOME)/.local/bin)

install: build
	@mkdir -p $(INSTALL_DIR)
	cp coddy $(INSTALL_DIR)/coddy
	@echo "Installed to $(INSTALL_DIR)/coddy"

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
