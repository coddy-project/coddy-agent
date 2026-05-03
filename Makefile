.PHONY: build build-acp test lint clean install

VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS := -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(VERSION)

BUILD_DIR := build
BINARY := $(BUILD_DIR)/coddy

# Build the coddy CLI (skills commands + ACP entrypoint).
build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/coddy/

# Install binary: /usr/local/bin for root, ~/.local/bin for regular users.
INSTALL_DIR := $(if $(filter 0,$(shell id -u)),/usr/local/bin,$(HOME)/.local/bin)

install: build
	@mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/coddy
	@echo "Installed to $(INSTALL_DIR)/coddy"

# Run all tests.
test:
	go test -v ./...

# Clean build artifacts.
clean:
	rm -rf $(BUILD_DIR)

# Run the linter (requires golangci-lint).
lint:
	golangci-lint run ./...
