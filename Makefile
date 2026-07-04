.PHONY: build build-acp test lint clean install print-version

# ---- Build options (extend when you add optional Go build tags) ----
#   TAGS   optional extra `go build -tags` values (space-separated).
#     Recommended full binary (matches default Docker BUILD_TAGS): make build TAGS="http ui scheduler memory"
#     http     OpenAI-compatible gateway (coddy http)
#     ui       embedded SPA for GET / (combine with http); runs npm ui-build first
#     scheduler       cron scheduler daemon and tools (see external/scheduler/)
#     memory          long-term memory copilot and /coddy memory REST (see external/memory/)
#     gateway.telegram  Telegram bot gateway only (coddy gateway; see external/gateway/)
#     gateway         all messenger gateways, currently Telegram (superset of gateway.telegram)
#   Examples: make build TAGS=http
#             make build TAGS="http ui"
#             make build TAGS="http scheduler"
#             make build TAGS="http ui scheduler memory"
#             make build TAGS="gateway.telegram"
#             make build TAGS="http ui scheduler memory gateway"
#   Omit memory (or other tags) for a slimmer binary; runtime memory.enabled only applies when built with memory.
#   VERSION / LDFLAGS   embedded version string (see print-version).

# Prefer a tag that points at HEAD (semantically latest if several), else nearest tag from history,
# else abbreviated commit (only if this is a git checkout), else "dev".
VERSION := $(shell \
	point=$$(git tag -l --points-at HEAD --sort=-v:refname 2>/dev/null | head -n1); \
	if [ -n "$$point" ]; then echo $$point; \
	elif desc=$$(git describe --tags --dirty 2>/dev/null); then echo $$desc; \
	elif desc=$$(git describe --tags --always --dirty 2>/dev/null); then echo $$desc; \
	else echo dev; fi)
LDFLAGS := -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(VERSION)

TAGS ?=
BUILD_DIR := build
BINARY := $(BUILD_DIR)/coddy

# Default tag set for `make install` when build/coddy is missing (matches Docker BUILD_TAGS).
FULL_TAGS := http ui scheduler memory

# Plain `make` must run `build`. Without this, the first rule would be `print-version`.
.DEFAULT_GOAL := build

ifneq ($(strip $(TAGS)),)
GO_TAGS_FLAG := -tags "$(strip $(TAGS))"
endif

# Embedded UI (go:embed) is included only with both http and ui tags.
ifneq ($(and $(findstring http,$(TAGS)),$(findstring ui,$(TAGS))),)
build: ui-build
endif

# Run npm from inside external/ui (cd + &&) rather than `npm --prefix`: some npm
# builds (notably on Windows) resolve `--prefix` for the install target but still
# read package.json from the cwd, failing with ENOENT on the repo root.
ui-build:
	cd external/ui && npm install --no-fund --no-audit
	cd external/ui && npm run build:go

# Build the coddy CLI (skills commands + ACP entrypoint; optional modules via TAGS).
build:
	@mkdir -p $(BUILD_DIR)
	go build $(GO_TAGS_FLAG) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/coddy/

# Print the same version string embedded by `make build` (for manual go build -ldflags).
print-version:
	@echo $(VERSION)

# Install binary: /usr/local/bin for root, ~/.local/bin for regular users.
INSTALL_DIR := $(if $(filter 0,$(shell id -u)),/usr/local/bin,$(HOME)/.local/bin)

# Install build/coddy onto PATH. Reuses an existing binary; builds FULL_TAGS only when missing.
install:
	@mkdir -p $(INSTALL_DIR)
	@if [ ! -f $(BINARY) ]; then \
		echo "No $(BINARY); building with TAGS=\"$(FULL_TAGS)\""; \
		$(MAKE) build TAGS="$(FULL_TAGS)"; \
	else \
		echo "Installing existing $(BINARY)"; \
	fi
	cp $(BINARY) $(INSTALL_DIR)/coddy
	@echo "Installed to $(INSTALL_DIR)/coddy"

# Run all tests.
test:
	go test ./...
	go test -tags=memory ./...
	go test -tags=http ./...
	go test -tags=http,memory ./...
	go test -tags=scheduler ./...
	go test -tags=scheduler,memory ./...
	$(MAKE) ui-build
	go test -tags=http,ui ./...
	go test -tags=http,ui,memory ./...
	go test -tags=http,scheduler ./...
	go test -tags=http,scheduler,memory ./...
	go test -tags=http,scheduler,ui ./...
	go test -tags=http,scheduler,ui,memory ./...

# Clean build artifacts.
clean:
	rm -rf $(BUILD_DIR)

# Run the linter (requires golangci-lint).
lint:
	golangci-lint run ./...
