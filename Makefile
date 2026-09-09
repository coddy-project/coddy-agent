.PHONY: build build-acp test test-opencode-rules check-windows lint lint-windows clean install print-version hooks deb rpm brew

# ---- Build options (extend when you add optional Go build tags) ----
#   TAGS   optional extra `go build -tags` values (space-separated).
#     Recommended full binary: make build TAGS="http ui scheduler memory cli"
#     (default Docker BUILD_TAGS additionally includes gateway)
#     http     OpenAI-compatible gateway (coddy http)
#     ui       embedded SPA for GET / (combine with http); runs npm ui-build first
#     scheduler       cron scheduler daemon and tools (see external/scheduler/)
#     memory          long-term memory copilot and /coddy memory REST (see external/memory/)
#     gateway.telegram  Telegram bot gateway only (coddy gateway; see external/gateway/)
#     gateway         all messenger gateways, currently Telegram (superset of gateway.telegram)
#     cli      interactive console TUI (bare `coddy` on a terminal; see external/cli/)
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
FULL_TAGS := http ui scheduler memory cli

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
MAN_DIR := $(if $(filter 0,$(shell id -u)),/usr/local/share/man/man1,$(HOME)/.local/share/man/man1)

# Install build/coddy onto PATH. Reuses an existing binary; builds FULL_TAGS only when missing.
# The man page goes with it, so `man coddy` works for a from-source install too;
# `make packages` is the same page, compressed, inside the deb and rpm.
install:
	@mkdir -p $(INSTALL_DIR) $(MAN_DIR)
	@if [ ! -f $(BINARY) ]; then \
		echo "No $(BINARY); building with TAGS=\"$(FULL_TAGS)\""; \
		$(MAKE) build TAGS="$(FULL_TAGS)"; \
	else \
		echo "Installing existing $(BINARY)"; \
	fi
	cp $(BINARY) $(INSTALL_DIR)/coddy
	cp packaging/man/coddy.1 $(MAN_DIR)/coddy.1
	@echo "Installed to $(INSTALL_DIR)/coddy and $(MAN_DIR)/coddy.1"

# ---- Distribution packages ----
#   PKG_ARCHS  architectures to package, space or comma separated (default: host)
#   PKG_TAGS   go build tags for the packaged binary (default: the release set)
#   DIST_DIR   where the packages land (default: dist)
#
# `deb` and `rpm` build Linux packages from packaging/nfpm.yaml through
# scripts/build-packages.sh; the release workflow calls the same script with the
# binaries it has already cross-compiled. nfpm is fetched on demand.
#
# `brew` renders the Homebrew cask (packaging/homebrew/coddy.rb.tmpl) for the
# macOS archives of a release. It needs those archives, so pass the version of a
# published release: make brew VERSION=1.0.11
PKG_ARCHS ?= $(shell go env GOARCH)
PKG_TAGS ?= $(FULL_TAGS)
DIST_DIR ?= dist

# Same rule as `build`: embedded assets must exist before a binary claims to
# carry them.
ifneq ($(and $(findstring http,$(PKG_TAGS)),$(findstring ui,$(PKG_TAGS))),)
deb rpm: ui-build
endif

deb:
	TAGS="$(PKG_TAGS)" scripts/build-packages.sh --version "$(VERSION)" --arch "$(PKG_ARCHS)" --out "$(DIST_DIR)" --formats deb

rpm:
	TAGS="$(PKG_TAGS)" scripts/build-packages.sh --version "$(VERSION)" --arch "$(PKG_ARCHS)" --out "$(DIST_DIR)" --formats rpm

brew:
	scripts/build-homebrew-cask.sh --version "$(VERSION)" --out "$(DIST_DIR)"

# Test the project plugin that attaches Cursor rules to OpenCode sessions.
test-opencode-rules:
	node --test .opencode/tests/project-rules.test.js

# Run all tests.
test: test-opencode-rules
	go test ./...
	go test -tags=memory ./...
	go test -tags=http ./...
	go test -tags=http,memory ./...
	go test -tags=scheduler ./...
	go test -tags=scheduler,memory ./...
	go test -tags=cli ./...
	go test -tags=cli,scheduler,memory ./...
	go test -tags=http,cli ./...
	$(MAKE) ui-build
	go test -tags=http,ui ./...
	go test -tags=http,ui,memory ./...
	go test -tags=http,scheduler ./...
	go test -tags=http,scheduler,memory ./...
	go test -tags=http,scheduler,ui ./...
	go test -tags=http,scheduler,ui,memory ./...
	go test -tags=http,scheduler,ui,memory,cli ./...

# Type-check the Windows build without a Windows machine.
#
# The suite above runs on the host only, so every file behind //go:build windows
# — the process group probe, console output decoding, the shell detector — is
# invisible to it and to the linter. A change to a shared signature therefore
# compiles here and breaks there, which is exactly the kind of drift nobody sees
# until a user reports it. go vet builds test files too, so this covers the
# Windows-only tests as well.
#
# The ui tag is deliberately absent: it embeds assets that only exist after
# ui-build, and it carries no platform-specific code.
check-windows:
	GOOS=windows go build ./...
	GOOS=windows go build -tags=cli ./...
	GOOS=windows go vet ./...
	GOOS=windows go vet -tags=memory ./...
	GOOS=windows go vet -tags=http ./...
	GOOS=windows go vet -tags=http,memory ./...
	GOOS=windows go vet -tags=scheduler ./...
	GOOS=windows go vet -tags=scheduler,memory ./...
	GOOS=windows go vet -tags=http,scheduler ./...
	GOOS=windows go vet -tags=http,scheduler,memory ./...
	GOOS=windows go vet -tags=cli ./...
	GOOS=windows go vet -tags=cli,scheduler,memory ./...

# Clean build artifacts.
clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR)

# Run the linter (requires golangci-lint). The second pass compiles the
# cli-tagged console surface, which the untagged pass never sees.
lint:
	golangci-lint run ./...
	golangci-lint run --build-tags cli ./external/cli/... ./cmd/coddy/...

# Run the linter against the Windows build, which lint above never compiles.
lint-windows:
	GOOS=windows golangci-lint run ./...
	GOOS=windows golangci-lint run --build-tags cli ./external/cli/... ./cmd/coddy/...

# Enable the repo's git hooks (pre-commit runs scripts/checks.sh). One-time per clone.
# Bypass a single commit with: git commit --no-verify
hooks:
	git config core.hooksPath .githooks
	@echo "Enabled .githooks — 'git commit' now runs the linter (scripts/checks.sh)."
	@echo "Add tests with CODDY_HOOK_TESTS=fast|full; skip lint with CODDY_HOOK_LINT=0; bypass once with --no-verify."
