# Building Coddy from source

This page is the detailed reference for local builds. For a short version, see [Installation](../README.md#installation) in the root **README**.

## Prerequisites

- **Go** - match `go` in [`go.mod`](../go.mod) (currently **1.25**).
- **Git** - the Makefile embeds a version string from tags or `git describe` when available.
- **Node.js and npm** - required only when you build with both **`http`** and **`ui`**, because the Makefile runs **`ui-build`** (see [`Makefile`](../Makefile)) to produce the assets that **`go:embed`** picks up.

Optional:

- **`golangci-lint` v2.x** (built with Go **1.25** or newer) - for **`make lint`**. CI uses **`golangci/golangci-lint-action@v7`** or newer (v6 supports only golangci-lint v1).

## Recommended full binary (HTTP, UI, scheduler, memory, console)

Build with **`memory`** to link long-term memory (`external/memory`). Enable behavior at runtime with **`memory.enabled`** in config (see [`external/memory/README.md`](../external/memory/README.md)).

The **HTTP gateway**, **embedded SPA**, **scheduler**, **memory**, the **messenger gateway** (**`gateway`**, **`coddy gateway`** — see [`docs/gateway.md`](gateway.md)), and the **interactive console** (**`cli`**, bare **`coddy`** on a terminal — see [`docs/cli.md`](cli.md)) are controlled by Go build tags. For a single binary that matches the default **Docker** image and includes every optional feature:

```bash
make build TAGS="http ui scheduler memory cli gateway"
```

Output: **`build/coddy`**.

Equivalent **`go build`** (after `ui-build` when you use **`ui`**, or use **`make build`**, which runs **`ui-build`** automatically when **`TAGS`** contains both **`http`** and **`ui`**):

```bash
make ui-build   # only when using -tags=...,ui,... with http; Makefile runs this for you on `make build`
VERSION="$(make -s print-version)"
go build -tags=http,ui,scheduler,memory,cli \
  -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=${VERSION}" \
  -o build/coddy \
  ./cmd/coddy/
```

The [**Dockerfile**](../Dockerfile) uses the same idea: comma-separated tags via **`BUILD_TAGS`** (default **`http,scheduler,ui,memory,gateway,cli`**) and strips debug symbols with **`-ldflags "-s -w ..."`** in addition to the version **`X`** flag.

## Install on your PATH

**`make install`** copies **`build/coddy`** onto your **`PATH`**:

- If **`build/coddy`** already exists (for example after **`make build TAGS="http ui scheduler memory cli gateway"`**), it is installed as-is without rebuilding.
- If the binary is missing, **`make install`** runs **`make build TAGS="http ui scheduler memory cli gateway"`** first.

- **root** - **`/usr/local/bin/coddy`**, man page in **`/usr/local/share/man/man1`**
- **non-root** - **`~/.local/bin/coddy`** (ensure that directory is on **`PATH`**), man page in **`~/.local/share/man/man1`**

```bash
make build TAGS="http ui scheduler memory cli gateway"
make install
```

## Distribution packages

**`make deb`** and **`make rpm`** build the Linux packages a release publishes; **`make brew`**
renders the Homebrew cask for one.

```bash
make deb
make rpm
```

Output: **`dist/coddy_<version>_linux_<arch>.deb`** and **`.rpm`**. Knobs:

| Variable | Default | What |
|----------|---------|------|
| **`PKG_ARCHS`** | host **`GOARCH`** | architectures to package, e.g. **`"amd64 arm64"`** |
| **`PKG_TAGS`** | **`http ui scheduler memory cli`** | build tags for the packaged binary |
| **`DIST_DIR`** | **`dist`** | where the packages land |

```bash
make deb PKG_ARCHS="amd64 arm64"
make rpm PKG_TAGS="http cli"      # lean binary, no npm step
```

The recipe is **`packaging/nfpm.yaml`**, driven by **`scripts/build-packages.sh`**, which stages the
man page (**`packaging/man/coddy.1`**), the shell completions (**`packaging/completions/`**),
**`config.example.yaml`** and **`LICENSE`** into one directory and runs
[nfpm](https://nfpm.goreleaser.com/) over it. nfpm is not a module dependency: the script uses the
**`nfpm`** on **`PATH`** when there is one and otherwise fetches the pinned version with
**`go run`**, so there is nothing to install first.

The package installs a binary and its documentation and nothing else - no service, no system
account, no files under **`/etc`** - because Coddy's state lives in the invoking user's
**`~/.coddy`**.

Version strings are normalised for the two formats by **`scripts/package-version.sh`** - rpm forbids
**`-`** in a version and dpkg reads the last one as the start of the Debian revision, so
**`1.0.8-5-gb6b7d31-dirty`** is packaged as **`1.0.8+5.gb6b7d31.dirty`**, which both accept and both
sort after **`1.0.8`**.

### Homebrew cask

```bash
make brew VERSION=1.0.11
```

**`scripts/build-homebrew-cask.sh`** fills **`packaging/homebrew/coddy.rb.tmpl`** with the version
and the SHA-256 of both macOS archives, writing **`dist/coddy.rb`**. It takes those archives from
**`DIST_DIR`** when they are there (which is the case in the release job, right after the
cross-compile) and downloads them from the GitHub release otherwise - a cask pins checksums, so it
can only be rendered for a version whose archives exist.

Install the rendered file to try it:

```bash
brew install --cask dist/coddy.rb
```

The cask links **`coddy`**, **`coddy.1`** and both completion scripts, which is why the release
**`darwin`** and **`linux`** archives carry those files beside the binary. Its **`livecheck`** block
tracks GitHub releases, so once the cask is accepted into
[homebrew/cask](https://github.com/Homebrew/homebrew-cask) Homebrew's own automation opens the
version bumps; until then each release publishes **`coddy.rb`** as an asset, and
**`brew install --cask <url>`** installs from it.

What the packages install, and how they interact with **`coddy update`**, is documented in
[install.md](install.md#linux-packages-deb-rpm) and
[update.md](update.md#installations-owned-by-a-package-manager).

## Update from GitHub Releases

See **[docs/update.md](update.md)** for **`coddy update`**, release asset names, and how that differs from **`make install`**.

## Lean build (ACP-focused, smaller binary)

Plain **`make build`** (empty **`TAGS`**) omits **`external/httpserver`**, the embedded UI, **`external/scheduler`**, and **`external/memory`**. You still get **`coddy acp`**, core tools, and MCP.

```bash
make build
```

Use this when you only need stdio ACP and want fewer dependencies and no **`npm`** step.

## Version string (`LDFLAGS`, `print-version`)

The Makefile sets:

```text
LDFLAGS := -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(VERSION)
```

**`VERSION`** is resolved from git (tag at **HEAD**, else **`git describe`**, else **`dev`**). Print the same value the next **`make build`** would embed:

```bash
make -s print-version
```

Manual one-liner aligned with **`make build`**:

```bash
go build \
  -tags=http,ui,scheduler,memory,cli \
  -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(make -s print-version)" \
  -o build/coddy \
  ./cmd/coddy/
```

## **`TAGS` vs `go build -tags`**

In **`Makefile`**, **`TAGS`** is **space-separated**:

```bash
make build TAGS="http ui scheduler memory cli gateway"
```

**`go build`** expects a **comma-separated** list (no spaces):

```bash
go build -tags=http,ui,scheduler,memory,cli ...
```

Order does not matter for these tags.

## Build tags reference

| Tag | Enables | Documentation |
|-----|---------|----------------|
| **`memory`** | Long-term memory copilot; with **`http`**, **`/coddy/sessions/{id}/memory/*`** REST; toggle runtime behavior with **`memory.enabled`** | [`external/memory/README.md`](../external/memory/README.md) |
| **`http`** | **`coddy http`**, OpenAI-shaped REST gateway, **`/docs`**, **`/openapi.yaml`** | [`docs/http-api.md`](http-api.md) · [`external/httpserver/`](../external/httpserver/) |
| **`ui`** | Embedded SPA on **`/`** (requires **`http`**; **`/`** returns **404** with **`http`** only) | [`docs/ui.md`](ui.md) · [`DESIGN.md`](../DESIGN.md) |
| **`scheduler`** | Scheduler daemon hooks, **`coddy_scheduler_*`** tools; with **`http`**, **`/coddy/scheduler`** REST | [`docs/scheduler.md`](scheduler.md) · [`external/scheduler/README.md`](../external/scheduler/README.md) |
| **`gateway.telegram`** | **`coddy gateway`** subcommand with Telegram bot adapter; per-user/group sessions, access control | [`docs/gateway.md`](gateway.md) · [`external/gateway/`](../external/gateway/) |
| **`gateway`** | All messenger adapters (superset of **`gateway.telegram`**; includes future Discord, Slack adapters) | [`docs/gateway.md`](gateway.md) |

**`make test`** exercises tag combinations (see **`test`** target in [`Makefile`](../Makefile)).

## Release binaries (CI)

On each SemVer git tag **`X.Y.Z`** that is on **`main`**, the [**Release binaries**](../.github/workflows/release-binaries.yaml) workflow (separate from Docker CI) uploads archives to the matching **GitHub Release**:

| Archive | Platform |
|---------|----------|
| **`coddy_X.Y.Z_linux_amd64.tar.gz`** | Linux x86_64 |
| **`coddy_X.Y.Z_linux_arm64.tar.gz`** | Linux arm64 |
| **`coddy_X.Y.Z_windows_amd64.zip`** | Windows x86_64 (**`coddy.exe`**) |
| **`coddy_X.Y.Z_darwin_amd64.tar.gz`** | macOS Intel |
| **`coddy_X.Y.Z_darwin_arm64.tar.gz`** | macOS Apple Silicon |
| **`coddy_X.Y.Z_linux_amd64.deb`**, **`coddy_X.Y.Z_linux_arm64.deb`** | Debian, Ubuntu and derivatives |
| **`coddy_X.Y.Z_linux_amd64.rpm`**, **`coddy_X.Y.Z_linux_arm64.rpm`** | Fedora, RHEL, openSUSE and derivatives |
| **`coddy.rb`** | Homebrew cask for the macOS archives of this tag |
| **`SHA256SUMS`** | Checksums for every archive and package above |

The **`.tar.gz`** archives carry the man page and the shell completions beside the binary; the
packages wrap the Linux binaries the same job just built rather than compiling their own, so the
**`.deb`**, the **`.rpm`** and the **`.tar.gz`** of one tag hold byte-identical executables.

Tags match the full feature set: **`http`**, **`ui`**, **`scheduler`**, **`memory`**. Manual run after a tag exists:

```bash
gh workflow run "Release binaries" --ref X.Y.Z -f tag=X.Y.Z
```

## **`go install` from upstream**

```bash
go install github.com/EvilFreelancer/coddy-agent/cmd/coddy@latest
```

That compiles whatever the module default is **without** your local **`TAGS`**. For a known set of features (HTTP, UI, scheduler, memory, console, messenger gateway), clone the repo and use **`make build TAGS="http ui scheduler memory cli gateway"`** (or **`go build -tags=...`** as above).
