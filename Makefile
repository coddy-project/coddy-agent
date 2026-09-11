.PHONY: build build-acp test test-matrix test-matrix-lean test-matrix-http test-matrix-ui test-matrix-full test-opencode-rules check-windows lint lint-windows clean install print-version hooks deb rpm brew brew-formula brew-check site-schema site-schema-check

# ---- Build options (extend when you add optional Go build tags) ----
#   TAGS   optional extra `go build -tags` values (space-separated).
#     Recommended full binary: make build TAGS="http ui scheduler memory cli gateway swarm"
#     http     OpenAI-compatible gateway and web UI (coddy serve)
#     ui       embedded SPA for GET / (combine with http); runs npm ui-build first
#     scheduler       cron scheduler daemon and tools (see external/scheduler/)
#     memory          long-term memory copilot and /coddy memory REST (see external/memory/)
#     gateway.telegram  Telegram bot gateway only (coddy serve; see external/gateway/)
#     gateway         all messenger gateways, currently Telegram (superset of gateway.telegram)
#     cli      interactive console TUI (bare `coddy` on a terminal; see external/cli/)
#     swarm    stateless relay that aggregates nodes (coddy serve; see external/swarm/)
#   Examples: make build TAGS=http
#             make build TAGS="http ui"
#             make build TAGS="http scheduler"
#             make build TAGS="http ui scheduler memory"
#             make build TAGS="gateway.telegram"
#             make build TAGS="http ui scheduler memory cli gateway swarm"
#   Omit memory (or other tags) for a slimmer binary; runtime memory.enable only applies when built with memory.
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
FULL_TAGS := http ui scheduler memory cli gateway swarm

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
#
# `brew-formula` renders the Homebrew formula (packaging/homebrew/coddy-formula.rb.tmpl)
# for the source archive of a release - that is the artefact homebrew/core takes,
# because Homebrew wants open-source command-line software built from source.
# `brew-check` is the preflight for that submission. See docs/homebrew.md.
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

brew-formula:
	scripts/build-homebrew-formula.sh --version "$(VERSION)" --out "$(DIST_DIR)/formula"

brew-check:
	scripts/check-homebrew-submission.sh --version "$(VERSION)"

# Publish docs/config.schema.json to the site repository, which serves it at
# coddy.dev/config.schema.json - the address Coddy writes into every config it
# saves. Point SITE_REPO at your checkout if it is not beside this one.
# site-schema-check reports drift without writing (for a pre-push look).
site-schema:
	scripts/sync-site-schema.sh

site-schema-check:
	CHECK=1 scripts/sync-site-schema.sh

# Test the project plugin that attaches Cursor rules to OpenCode sessions.
test-opencode-rules:
	node --test .opencode/tests/project-rules.test.js

# ---- Tests ----
#
# Two speeds. `make test` is the express run: one `go test` over the whole tree
# with every optional module compiled in (FULL_TAGS, what the shipped binary
# contains), after the UI assets it embeds are built. Run it locally before a
# push. `make test-matrix` covers every tag combination - the lean untagged
# build with its stubs, single tags, pairs, the shipped set - in four groups,
# test-matrix-lean|http|ui|full. CI runs the groups as parallel jobs behind one
# gate job on every pull request (.github/workflows/tests-on-pr.yaml); locally
# `make -j4 test-matrix` does the same, or reach for a single combination:
# go test -tags=<set> ./...
empty :=
space := $(empty) $(empty)
comma := ,
FULL_TAGS_CSV := $(subst $(space),$(comma),$(strip $(FULL_TAGS)))

# The tagged combinations, comma-joined, grouped by what they need: lean ones
# build without npm, http ones add the REST surface, ui ones embed the SPA (so
# ui-build must precede them), full ones are the shipped sets. The untagged
# build runs first in the lean group; the last full entry must stay
# FULL_TAGS_CSV so the express run and the matrix agree on what "everything"
# means.
TEST_TAG_SETS_LEAN := memory scheduler scheduler,memory cli cli,scheduler,memory swarm gateway
TEST_TAG_SETS_HTTP := http http,memory http,cli http,scheduler http,scheduler,memory
TEST_TAG_SETS_UI := http,ui http,ui,memory http,scheduler,ui http,scheduler,ui,memory http,scheduler,ui,memory,cli
TEST_TAG_SETS_FULL := http,scheduler,ui,memory,cli,gateway http,scheduler,ui,memory,cli,swarm $(FULL_TAGS_CSV)

# Express run: the whole tree once, with every optional module compiled in.
test: test-opencode-rules ui-build
	go test -tags=$(FULL_TAGS_CSV) ./...

# Full matrix: every group. Sequential by default; -j4 runs the groups side by
# side. CI's job; run it locally only when a build-tag boundary moved and one
# combination is not enough.
test-matrix: test-matrix-lean test-matrix-http test-matrix-ui test-matrix-full

test-matrix-lean: test-opencode-rules
	go test ./...
	@set -e; for tags in $(TEST_TAG_SETS_LEAN); do echo "go test -tags=$$tags ./..."; go test -tags=$$tags ./...; done

test-matrix-http:
	@set -e; for tags in $(TEST_TAG_SETS_HTTP); do echo "go test -tags=$$tags ./..."; go test -tags=$$tags ./...; done

test-matrix-ui: ui-build
	@set -e; for tags in $(TEST_TAG_SETS_UI); do echo "go test -tags=$$tags ./..."; go test -tags=$$tags ./...; done

test-matrix-full: ui-build
	@set -e; for tags in $(TEST_TAG_SETS_FULL); do echo "go test -tags=$$tags ./..."; go test -tags=$$tags ./...; done

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
	GOOS=windows go vet -tags=swarm ./...
	GOOS=windows go vet -tags=http,swarm ./...
	GOOS=windows go vet -tags=gateway ./...
	GOOS=windows go vet -tags=http,scheduler,memory,cli,gateway ./...
	GOOS=windows go vet -tags=http,scheduler,memory,cli,gateway,swarm ./...

# Clean build artifacts.
clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR)

# Run the linter (requires golangci-lint). The second pass compiles the
# cli-tagged console surface, which the untagged pass never sees.
lint:
	golangci-lint run ./...
	golangci-lint run --build-tags cli ./external/cli/... ./cmd/coddy/...
	golangci-lint run --build-tags swarm ./external/swarm/... ./cmd/coddy/...
	golangci-lint run --build-tags gateway ./external/gateway/... ./cmd/coddy/...

# Run the linter against the Windows build, which lint above never compiles.
lint-windows:
	GOOS=windows golangci-lint run ./...
	GOOS=windows golangci-lint run --build-tags cli ./external/cli/... ./cmd/coddy/...
	GOOS=windows golangci-lint run --build-tags gateway ./external/gateway/... ./cmd/coddy/...

# Enable the repo's git hooks (pre-commit runs scripts/checks.sh). One-time per clone.
# Bypass a single commit with: git commit --no-verify
hooks:
	git config core.hooksPath .githooks
	@echo "Enabled .githooks — 'git commit' now runs the linter (scripts/checks.sh)."
	@echo "Add tests with CODDY_HOOK_TESTS=fast|full|matrix; skip lint with CODDY_HOOK_LINT=0; bypass once with --no-verify."
