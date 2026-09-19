.PHONY: build build-acp ui-deps ui-build ui-test ui-typecheck test test-matrix test-race print-test-tag-sets print-full-tags print-lint-tags-no-ui test-opencode-rules check-windows lint lint-windows clean install print-version hooks deb rpm brew brew-formula brew-check site-schema site-schema-check docs docs-check docs-changelog docs-fast site-docs site-docs-check skills-vendor skills-vendor-check

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
GOEXE := $(shell go env GOEXE)
BINARY := $(BUILD_DIR)/coddy$(GOEXE)

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
ui-deps:
	cd external/ui && npm install --no-fund --no-audit

ui-build: ui-deps
	cd external/ui && npm run build:go

# The SPA's own vitest suite, part of `make test`.
ui-test: ui-deps
	cd external/ui && npm test

# The TypeScript compiler over the SPA sources, no emit: the type gate of
# `make lint`. vite only transpiles, so a type error ships unless this runs.
ui-typecheck: ui-deps
	cd external/ui && npm run typecheck

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
	cp $(BINARY) $(INSTALL_DIR)/coddy$(GOEXE)
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
# `brew-check` is the preflight for that submission. See docs/getting-started/homebrew.md.
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

# Publish internal/config/config.schema.json (the schema embedded into the
# binary for -t / --test-config) to the site repository, which serves it at
# coddy.dev/config.schema.json - the address Coddy writes into every config it
# saves. Point SITE_REPO at your checkout if it is not beside this one.
# site-schema-check reports drift without writing (for a pre-push look).
site-schema:
	scripts/sync-site-schema.sh

site-schema-check:
	CHECK=1 scripts/sync-site-schema.sh

# Documentation. docs/README.md (the hub), docs/llms.txt, docs/llms-full.txt, the
# field tables of docs/reference/config.md, the help screens of docs/reference/cli.md
# and the inventory of docs/assets/INDEX.md are generated by internal/docsgen from
# docs/nav.yaml, the embedded config schema and the --help output of a full-tag
# binary. docs-check regenerates into memory and fails on drift, on a page missing
# from nav.yaml, on a broken relative link or anchor, and on an asset nothing uses.
# docs-changelog additionally refreshes docs/getting-started/changelog.md from the
# GitHub Releases (needs gh and network).
docs:
	$(MAKE) build TAGS="$(FULL_TAGS)"
	go run ./cmd/docsgen -write -coddy $(BINARY) -tags "$(FULL_TAGS_CSV)"

docs-check:
	$(MAKE) build TAGS="$(FULL_TAGS)"
	go run ./cmd/docsgen -coddy $(BINARY) -tags "$(FULL_TAGS_CSV)"

# docs-fast regenerates everything but the CLI reference, so a machine without
# Node (the ui tag needs the SPA build) can still refresh the hub, the llms
# files, the config tables and the assets inventory.
docs-fast:
	go run ./cmd/docsgen -write -skip-cli

# The documentation layer of coddy.dev: a stable address coddy.dev/docs/<slug>
# for every page of docs/nav.yaml (one interceptor script behind the site 404 page),
# plus llms.txt and llms-full.txt at the site root. The binary, the schema and
# the bundled skill print those addresses. site-docs renders them into the
# site checkout (SITE_REPO=... if it is elsewhere), site-docs-check reports
# drift without writing.
site-docs:
	scripts/sync-site-docs.sh

site-docs-check:
	CHECK=1 scripts/sync-site-docs.sh

docs-changelog:
	$(MAKE) build TAGS="$(FULL_TAGS)"
	go run ./cmd/docsgen -changelog -write -coddy $(BINARY) -tags "$(FULL_TAGS_CSV)"

# The skills Coddy carries inside its binary (internal/skills/bundled). Those
# that live in their own repositories are vendored rather than fetched at
# runtime: skills-vendor refreshes the copies from the upstreams named in
# scripts/bundled-skills.json, skills-vendor-check reports drift without
# writing. Needs jq, git and network.
skills-vendor:
	scripts/vendor-bundled-skills.sh

skills-vendor-check:
	scripts/vendor-bundled-skills.sh --check

# Test the project plugin that attaches Cursor rules to OpenCode sessions.
test-opencode-rules:
	node --test .opencode/tests/project-rules.test.js

# ---- Tests ----
#
# Two speeds. `make test` is the express run: the SPA's vitest suite, then one
# `go test` over the whole tree with every optional module compiled in
# (FULL_TAGS, what the shipped binary contains), after the UI assets it embeds
# are built. Run it locally before a push. `make test-matrix` walks every tag
# combination in TEST_TAG_SETS - the lean untagged build with its stubs, single
# tags, pairs, the shipped set - and is what CI runs on every pull request, one
# job per combination
# (.github/workflows/tests-on-pr.yaml reads the list through
# print-test-tag-sets, so this variable is the only place it is written down).
# Locally, reach for a single combination instead: go test -tags=<set> ./...
empty :=
space := $(empty) $(empty)
comma := ,
FULL_TAGS_CSV := $(subst $(space),$(comma),$(strip $(FULL_TAGS)))

# Every tagged combination the tree is tested under, comma-joined. The untagged
# build is implied and always runs first; the last entry must stay FULL_TAGS_CSV
# so the express run and the matrix agree on what "everything" means. A ui
# entry needs the embedded assets, so ui-build precedes the matrix and the CI
# job builds them only for those entries.
TEST_TAG_SETS := \
	memory \
	http \
	http,memory \
	scheduler \
	scheduler,memory \
	cli \
	cli,scheduler,memory \
	http,cli \
	swarm \
	http,ui \
	http,ui,memory \
	http,scheduler \
	http,scheduler,memory \
	http,scheduler,ui \
	http,scheduler,ui,memory \
	http,scheduler,ui,memory,cli \
	gateway \
	http,scheduler,ui,memory,cli,gateway \
	http,scheduler,ui,memory,cli,swarm \
	$(FULL_TAGS_CSV)

# Express run: the SPA suite, then the whole Go tree once with every optional
# module compiled in.
test: test-opencode-rules ui-build ui-test
	go test -tags=$(FULL_TAGS_CSV) ./...

# Full matrix: every combination in TEST_TAG_SETS, in sequence. CI's job; run
# it locally only when a build-tag boundary moved and one combination is not
# enough.
test-matrix: test-opencode-rules ui-build ui-test
	go test ./...
	@set -e; for tags in $(TEST_TAG_SETS); do \
		echo "go test -tags=$$tags ./..."; \
		go test -tags=$$tags ./...; \
	done

# The matrix as a JSON array for the CI workflow: [""] for the untagged build,
# then every entry of TEST_TAG_SETS.
print-test-tag-sets:
	@printf '[""'; for tags in $(TEST_TAG_SETS); do printf ',"%s"' "$$tags"; done; printf ']\n'

# The race detector over the whole tree, with every optional module compiled in
# but ui (LINT_TAGS_NO_UI below): that tag only embeds the SPA and needs Node,
# and adds no Go concurrency. CI runs this target on every pull request, and
# GOFLAGS=-count=3 repeats each test to shake out a race that shows up rarely.
#
# RACE_SKIP_PKGS names the packages that still report data races, relative to
# the module. They are tested by every other run as usual; a package leaves
# the list in the change that fixes its races, and one that is not on it,
# a new package included, is covered without anyone adding it.
RACE_SKIP_PKGS := \
	external/httpserver \
	external/scheduler/daemon \
	internal/agent \
	internal/llm

test-race:
	@set -e; mod=$$(go list -m); \
	pkgs=$$(go list -tags=$(LINT_TAGS_NO_UI_CSV) ./... | grep -v -x -F $(foreach p,$(RACE_SKIP_PKGS),-e "$$mod/$(p)")); \
	echo "go test -race -tags=$(LINT_TAGS_NO_UI_CSV) ./... (skipping $(RACE_SKIP_PKGS))"; \
	go test -race -tags=$(LINT_TAGS_NO_UI_CSV) $$pkgs

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

# ---- Lint ----
#
# Three golangci-lint passes reach every Go file of the host platform: the
# untagged tree (every stub), every tag but ui (the files that exist only
# without the SPA: http && !ui, swarm && !ui, and every single-tag surface),
# and the shipped set (the embedded SPA and the http && ui handlers, which is
# why the assets are built first). The TypeScript pass closes the gate over
# the SPA sources. The CI lint job reads the two tag lists through
# print-full-tags and print-lint-tags-no-ui, so FULL_TAGS is the only place
# they are written down.
LINT_TAGS_NO_UI := $(filter-out ui,$(FULL_TAGS))
LINT_TAGS_NO_UI_CSV := $(subst $(space),$(comma),$(strip $(LINT_TAGS_NO_UI)))

print-full-tags:
	@printf '%s\n' "$(FULL_TAGS_CSV)"

print-lint-tags-no-ui:
	@printf '%s\n' "$(LINT_TAGS_NO_UI_CSV)"

lint: ui-build
	golangci-lint run ./...
	golangci-lint run --build-tags $(LINT_TAGS_NO_UI_CSV) ./...
	golangci-lint run --build-tags $(FULL_TAGS_CSV) ./...
	$(MAKE) ui-typecheck

# The same three passes against the Windows build, which lint above never
# compiles.
lint-windows: ui-build
	GOOS=windows golangci-lint run ./...
	GOOS=windows golangci-lint run --build-tags $(LINT_TAGS_NO_UI_CSV) ./...
	GOOS=windows golangci-lint run --build-tags $(FULL_TAGS_CSV) ./...

# Enable the repo's git hooks (pre-commit runs scripts/checks.sh). One-time per clone.
# Bypass a single commit with: git commit --no-verify
hooks:
	git config core.hooksPath .githooks
	@echo "Enabled .githooks — 'git commit' now runs the linter (scripts/checks.sh)."
	@echo "Add tests with CODDY_HOOK_TESTS=fast|full|matrix; skip lint with CODDY_HOOK_LINT=0; bypass once with --no-verify."
