.PHONY: build build-acp test lint clean install print-version

# Prefer a tag that points at HEAD (semantically latest if several), else nearest tag from history,
# else abbreviated commit (only if this is a git checkout), else "dev".
VERSION := $(shell \
	point=$$(git tag -l --points-at HEAD --sort=-v:refname 2>/dev/null | head -n1); \
	if [ -n "$$point" ]; then echo $$point; \
	elif desc=$$(git describe --tags --dirty 2>/dev/null); then echo $$desc; \
	elif desc=$$(git describe --tags --always --dirty 2>/dev/null); then echo $$desc; \
	else echo dev; fi)
LDFLAGS := -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(VERSION)

BUILD_DIR := build
BINARY := $(BUILD_DIR)/coddy

# Plain `make` must run `build`. Without this, the first rule would be `print-version`.
.DEFAULT_GOAL := build

# Build the coddy CLI (skills commands + ACP entrypoint).
build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/coddy/

# Print the same version string embedded by `make build` (for manual go build -ldflags).
print-version:
	@echo $(VERSION)

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
