# Building Coddy from source

This page is the detailed reference for local builds. For a short version, see [Installation](../../README.md#install) in the root **README**.

## Prerequisites

- **Go** - **1.26** or newer, the `go` line of [`go.mod`](../../go.mod). The `toolchain` line next to it (currently **`go1.26.9`**) is the oldest release any build links: CI and the release archives build with exactly that one, and with the default `GOTOOLCHAIN=auto` an older local `go` downloads it on first use. A newer local Go, or a newer patch in the `golang:1.26` image the Dockerfile starts from, builds with itself, which only adds fixes. Raise it when a Go patch release fixes a vulnerability the code reaches: `go mod edit -toolchain=go1.26.N` (see [AppSec scanning](security-scanning.md#the-go-toolchain)).
- **Git** - the Makefile embeds a version string from tags or `git describe` when available.
- **Node.js and npm** - required when you build with both **`http`** and **`ui`**, because the Makefile runs **`ui-build`** (see [`Makefile`](../../Makefile)) to produce the assets that **`go:embed`** picks up, and by **`make test`** and **`make lint`**, which run the SPA's vitest suite and its TypeScript check.

Optional:

- **`golangci-lint` v2.x** (built with Go **1.26** or newer) - for **`make lint`**. CI uses **`golangci/golangci-lint-action@v7`** or newer (v6 supports only golangci-lint v1).

## Recommended full binary (HTTP, UI, scheduler, memory, console)

Build with **`memory`** to link long-term memory (`external/memory`). Enable behavior at runtime with **`memory.enable`** in config (see [`external/memory/README.md`](../../external/memory/README.md)).

The **HTTP gateway**, **embedded SPA**, **scheduler**, **memory**, the **messenger gateway** (**`gateway`** — see [`docs/surfaces/gateway.md`](../surfaces/gateway.md)), and the **interactive console** (**`cli`**, bare **`coddy`** on a terminal — see [`docs/surfaces/console.md`](../surfaces/console.md)) are controlled by Go build tags. For a single binary that matches the default **Docker** image and includes every optional feature:

```bash
make build TAGS="http ui scheduler memory cli gateway swarm metrics"
```

Output: **`build/coddy`**.

Equivalent **`go build`** (after `ui-build` when you use **`ui`**, or use **`make build`**, which runs **`ui-build`** automatically when **`TAGS`** contains both **`http`** and **`ui`**):

```bash
make ui-build   # only when using -tags=...,ui,... with http; Makefile runs this for you on `make build`
VERSION="$(make -s print-version)"
go build -tags=http,ui,scheduler,memory,cli,gateway,swarm,metrics \
  -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=${VERSION}" \
  -o build/coddy \
  ./cmd/coddy/
```

The [**Dockerfile**](../../Dockerfile) uses the same idea: comma-separated tags via **`BUILD_TAGS`** (default **`http,scheduler,ui,memory,gateway,cli,swarm,metrics`**) and strips debug symbols with **`-ldflags "-s -w ..."`** in addition to the version **`X`** flag.

## Install on your PATH

**`make install`** copies **`build/coddy`** onto your **`PATH`**:

- If **`build/coddy`** already exists (for example after **`make build TAGS="http ui scheduler memory cli gateway swarm metrics"`**), it is installed as-is without rebuilding.
- If the binary is missing, **`make install`** runs **`make build TAGS="http ui scheduler memory cli gateway swarm metrics"`** first.

- **root** - **`/usr/local/bin/coddy`**, man page in **`/usr/local/share/man/man1`**
- **non-root** - **`~/.local/bin/coddy`** (ensure that directory is on **`PATH`**), man page in **`~/.local/share/man/man1`**

```bash
make build TAGS="http ui scheduler memory cli gateway swarm metrics"
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
| **`PKG_TAGS`** | **`http ui scheduler memory cli gateway swarm metrics`** | build tags for the packaged binary |
| **`DIST_DIR`** | **`dist`** | where the packages land |

```bash
make deb PKG_ARCHS="amd64 arm64"
make rpm PKG_TAGS="http cli"      # lean binary, no npm step
```

The recipe is **`packaging/nfpm.yaml`**, driven by **`scripts/build-packages.sh`**, which stages the
man page (**`packaging/man/coddy.1`**), the shell completions (**`packaging/completions/`**),
the systemd user unit (**`packaging/systemd/coddy.service`**), the maintainer scripts
(**`packaging/scripts/postinstall.sh`**, **`preremove.sh`**),
**`config.example.yaml`** and **`LICENSE`** into one directory and runs
[nfpm](https://nfpm.goreleaser.com/) over it. nfpm is not a module dependency: the script uses the
**`nfpm`** on **`PATH`** when there is one and otherwise fetches the pinned version with
**`go run`**, so there is nothing to install first.

The user unit is installed but not enabled, and the post-install message says so; each user who
wants the service runs **`coddy serve install`**. No system service, system account or files under
**`/etc`** are created, because each user keeps state under **`~/.coddy`**. The unit is not
written by hand: **`serve.PackagedUnitFile()`** (**`internal/serve/systemd.go`**) renders it, the
same function that writes the unit for a script install, and **`TestPackagedUnitIsTheRenderedOne`**
fails until **`packaging/systemd/coddy.service`** matches it byte for byte. The **Distribution
packages** CI job then checks that both package formats carry that file and the maintainer
scripts.

The packages suggest **`tmux`** and require nothing. Coddy runs without it, and its console runs
well inside it, where a session outlives a closed terminal or a dropped SSH connection. A
suggestion is named and never installed - **`apt`** and **`zypper`** list it during the install,
**`dnf`** keeps it in the metadata (**`rpm -q --suggests`**) - so the choice stays with the user.
The **Distribution packages** CI job checks that the deb and the rpm suggest it and neither
requires nor recommends it.

Version strings are normalised for the two formats by **`scripts/package-version.sh`** - rpm forbids
**`-`** in a version and dpkg reads the last one as the start of the Debian revision, so
**`1.0.8-5-gb6b7d31-dirty`** is packaged as **`1.0.8+5.gb6b7d31.dirty`**, which both accept and both
sort after **`1.0.8`**.

### Homebrew cask

```bash
make brew VERSION=1.0.11
```

**`scripts/build-homebrew-cask.sh`** fills **`packaging/homebrew/coddy.rb.tmpl`** with the version
and the SHA-256 of both macOS archives, writing **`dist/coddy.rb`**. It hashes the archives in
**`DIST_DIR`** when they are there (which is the case in the release job, right after the
cross-compile) and otherwise reads their checksums from the **`SHA256SUMS`** of that GitHub release -
a cask pins checksums, so it can only be rendered for a version whose archives exist.

Current Homebrew refuses a cask given by its path, so a rendered file is tried out of a throwaway
local tap. On a Mac with Homebrew:

```bash
make brew-cask-check                                    # dist/coddy.rb from a local tap
make brew-cask-check BREW_TAP=coddy-project/coddy-agent # the published cask, by name
```

**`scripts/check-homebrew-cask.sh`** installs the cask, runs **`brew style`** on it, checks that
**`coddy -v`** prints its version, that the binary resolves into the **`Caskroom`** with no
**`com.apple.quarantine`** attribute left, that the man page and both completions are linked, and
that **`coddy update`** names **`brew upgrade --cask coddy`**, which it then runs. It uninstalls the
cask and removes the tap it added at the end; **`--keep`** leaves both. It never turns on Homebrew's
developer mode: it creates the local tap as a plain directory instead of running **`brew tap-new`**,
and gives **`brew style`** the **`HOMEBREW_DEVELOPER`** variable for that one command, because a
developer command run without it switches the mode on for good.

The cask links **`coddy`**, **`coddy.1`** and both completion scripts, which is why the release
**`darwin`** and **`linux`** archives carry those files beside the binary. It declares
**`depends_on macos: :monterey`**: Go 1.25 and later build binaries that need macOS 12. A
**`postflight_steps`** step removes the quarantine attribute Homebrew sets on the download from the
binary, which is ad-hoc signed and not notarised, so Gatekeeper does not stop its first run. It
recommends **`tmux`** in its **`caveats`** rather than declaring it: a cask's **`depends_on`** has no
optional form, and Coddy runs without tmux, so the cask names it and installs nothing.

### Homebrew tap

The cask reaches users through the tap
[coddy-project/homebrew-coddy-agent](https://github.com/coddy-project/homebrew-coddy-agent), installed with
**`brew install coddy-project/coddy-agent/coddy`**. Its **`Casks/coddy.rb`** is generated and never edited
by hand.

```bash
make brew-publish VERSION=1.0.11
```

**`scripts/publish-homebrew-tap.sh`** clones the tap, replaces **`Casks/coddy.rb`** with the rendered
cask and pushes a commit named **`coddy X.Y.Z`**. The tap only moves forward: a cask for a release
older than the one the tap carries is skipped, and a cask identical to the tap's changes nothing, so
a release job re-run for an old tag cannot take it back. A push that loses a race with another
release job is retried from the tap's new head.

The release job runs it after the assets are uploaded, with the private half of a deploy key that
can write to the tap and nothing else, from the secret **`HOMEBREW_TAP_DEPLOY_KEY`**; GitHub's SSH
host keys come from **`api.github.com/meta`**. Without the secret the release step stops with an
error after the upload, and the tap stays at the previous release until the job is run again; run by
hand without it, the script pushes with your own git credentials. Then the **Homebrew tap** workflow
([`homebrew.yaml`](../../.github/workflows/homebrew.yaml)) installs the cask by name on a macOS
runner with current Homebrew and checks that it carries the release just published or a newer one. The same
workflow runs on pull requests that touch the cask, the scripts or itself, where it also installs the
cask the checkout renders. It also runs by hand, and weekly on its own, to catch a Homebrew release
that stops installing the tap's cask.

An owner of the organization sets the key up once. First allow deploy keys for the organization's
repositories, in the organization settings under Member privileges, Deploy keys. Then generate a key
pair and give its public half write access to the tap.

```bash
ssh-keygen -t ed25519 -N "" -C "coddy-agent release workflow" -f ./tap_key
gh repo deploy-key add tap_key.pub --repo coddy-project/homebrew-coddy-agent --allow-write --title "coddy-agent release workflow"
```

Store the private half as the secret of this repository and delete both files.

```bash
gh secret set HOMEBREW_TAP_DEPLOY_KEY --repo coddy-project/coddy-agent < tap_key
rm tap_key tap_key.pub
```

Nothing else is needed: **`tag-on-merge.yaml`** calls the release workflow with **`secrets: inherit`**,
and Actions provides **`GITHUB_TOKEN`** itself. A new key replaces an old one the same way; remove the
old deploy key from the tap afterwards.

### Homebrew formula

```bash
make brew-formula VERSION=1.0.13
make brew-check VERSION=1.0.13
```

**`scripts/build-homebrew-formula.sh`** fills **`packaging/homebrew/coddy-formula.rb.tmpl`** with the
version and the SHA-256 of that tag's **source** archive, writing **`dist/formula/coddy.rb`**. It
downloads the archive to hash it, so the version has to be a published tag.

That file is the artefact a [homebrew/core](https://github.com/Homebrew/homebrew-core) pull request
carries. Homebrew routes open-source command-line software there as a formula built from source and
keeps homebrew/cask for native applications and binary-only software, so the cask above is our own
channel and the formula is the submission. The formula builds the release tag set, which is why
**`node`** joins **`go`** as a build dependency: the embedded SPA is generated rather than committed.

**`scripts/check-homebrew-submission.sh`** (**`make brew-check`**) is the preflight - token
availability, notability thresholds, the release, and the rendered formula. It exits non-zero when
something blocks the submission. The full path, including the notability arithmetic that blocks a
self-submission today, is in [homebrew.md](../getting-started/homebrew.md).

What the packages install, and how they interact with **`coddy update`**, is documented in
[install.md](../getting-started/install.md#linux-packages-deb-rpm) and
[update.md](../getting-started/update.md#installations-owned-by-a-package-manager).

## Update from GitHub Releases

See **[docs/getting-started/update.md](../getting-started/update.md)** for **`coddy update`**, release asset names, and how that differs from **`make install`**.

## Lean build (ACP-focused, smaller binary)

Plain **`make build`** (empty **`TAGS`**) omits **`external/httpserver`**, the embedded UI, **`external/scheduler`**, and **`external/memory`**. You still get **`coddy acp`**, core tools, and MCP.

```bash
make build
```

Use this when you only need stdio ACP and want fewer dependencies and no **`npm`** step.

## Android (Termux)

```bash
make android TAGS="http ui scheduler memory cli gateway swarm metrics"   # build/coddy-android-arm64, -amd64
make check-android                                              # go vet with GOOS=android, test files included
```

The Android build is the same sources with **`GOOS=android`** and cgo, linked by the Android NDK
against Bionic for arm64 and x86_64: a position-independent executable that names
**`/system/bin/linker64`** as its interpreter and needs only **`libc`**, **`libdl`** and
**`liblog`**. That is the one shape Termux can start where it runs every program through Android's
linker, which turns the static **`GOOS=linux`** binary away with **`has unexpected e_type: 2`**, and
Bionic gives the binary what libc gives any program: its own arguments under the linker and
Android's resolver. **`GOOS=android`** also keeps Go off the system calls Android's seccomp filter
forbids. Go links android/amd64 only through a C toolchain anyway, and a build without cgo would
ask **`127.0.0.1:53`** for every host, so **`internal/platform/android_nocgo.go`** stops it at
compile time. What users see is on [Android (Termux)](../getting-started/android.md).

**`scripts/android-cc.sh arm64|amd64`** prints the NDK clang both targets use, for API 24 (Android
7.0, the oldest Termux runs on): it takes the NDK from **`ANDROID_NDK_HOME`**, **`ANDROID_NDK_ROOT`**
or **`ANDROID_NDK`**, which the GitHub runners set, and otherwise the newest one under the SDK
(**`sdkmanager "ndk;27.3.13750724"`** installs the version the runners carry).

A binary built outside Termux carries none of the patches Termux applies to its own Go, so
**`internal/platform`** makes the remaining adjustments at run time. **`android_init.go`** keeps the
path of the binary when the linker started it, since **`/proc/self/exe`** names the linker then,
and points **`SSL_CERT_FILE`** and **`TMPDIR`** at the Termux prefix; **`platform.AdaptCommand`**
starts a child process the way **`termux-exec`** would, through the linker when Coddy itself came up
that way, with **`/usr/bin/env`** and **`/bin/sh`** in a shebang taken from the prefix. Every place
that builds an **`exec.Cmd`** calls **`AdaptCommand`** before **`Start`**, and
**`TestEverySpawnSiteAdaptsTheCommand`** fails on one that does not.

The decisions are plain functions in **`internal/platform/android.go`**, held on any Unix host by
**`features/android_termux.feature`** and **`internal/platform/android_test.go`**. The files behind
**`//go:build android`** are compiled only by **`make check-android`** and by the **Android
cross-build** job of the pull request checks, which also builds both release binaries, checks that
each is position-independent, names the Android linker and needs no library Android does not ship,
and uploads them as the **`coddy-android-arm64`** and **`coddy-android-amd64`** artifacts to try on
a device before the release.

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
  -tags=http,ui,scheduler,memory,cli,gateway,swarm,metrics \
  -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(make -s print-version)" \
  -o build/coddy \
  ./cmd/coddy/
```

## **`TAGS` vs `go build -tags`**

In **`Makefile`**, **`TAGS`** is **space-separated**:

```bash
make build TAGS="http ui scheduler memory cli gateway swarm metrics"
```

**`go build`** expects a **comma-separated** list (no spaces):

```bash
go build -tags=http,ui,scheduler,memory,cli,gateway,swarm,metrics ...
```

Order does not matter for these tags.

## Build tags reference

| Tag | Enables | Documentation |
|-----|---------|----------------|
| **`memory`** | The long-term memory subagent; with **`http`**, **`/coddy/sessions/{id}/memory/*`** REST; toggle runtime behavior with **`memory.enable`** | [`external/memory/README.md`](../../external/memory/README.md) |
| **`http`** | The OpenAI-shaped REST gateway `coddy serve` runs under **`httpserver.enable`**, **`/docs`**, **`/openapi.yaml`** | [`docs/reference/http-api.md`](../reference/http-api.md) · [`external/httpserver/`](../../external/httpserver) |
| **`ui`** | Embedded SPA on **`/`** (requires **`http`**; **`/`** returns **404** with **`http`** only) | [`docs/surfaces/web-ui.md`](../surfaces/web-ui.md) · [`DESIGN.md`](../../DESIGN.md) |
| **`scheduler`** | Scheduler daemon hooks, **`coddy_scheduler_*`** tools; with **`http`**, **`/coddy/scheduler`** REST | [`docs/operate/scheduler.md`](../operate/scheduler.md) · [`external/scheduler/README.md`](../../external/scheduler/README.md) |
| **`cli`** | Interactive console TUI: bare **`coddy`** on a terminal | [`docs/surfaces/console.md`](../surfaces/console.md) · [`external/cli/`](../../external/cli) |
| **`gateway.telegram`** | Telegram bot adapter, started by **`coddy serve`** under **`gateways.telegram.enable`**; per-user/group sessions, access control | [`docs/surfaces/gateway.md`](../surfaces/gateway.md) · [`external/gateway/`](../../external/gateway) |
| **`gateway`** | All messenger adapters (superset of **`gateway.telegram`**; includes future Discord, Slack adapters) | [`docs/surfaces/gateway.md`](../surfaces/gateway.md) |
| **`swarm`** | Stateless relay that aggregates nodes, started by **`coddy serve`** under **`swarm.enable`** | [`docs/operate/swarm.md`](../operate/swarm.md) · [`external/swarm/`](../../external/swarm) |
| **`metrics`** | Prometheus exporter, started by **`coddy serve`** under **`metrics.enable`** (off by default); serves **`/metrics`** on a port of its own | [`docs/operate/monitoring.md`](../operate/monitoring.md) · [`external/metrics/`](../../external/metrics) |

**`make test`** is the express run: the whole tree once with every optional module compiled in (**`http,ui,scheduler,memory,cli,gateway,swarm,metrics`**). **`make test-matrix`** walks every combination (the **`TEST_TAG_SETS`** list in [`Makefile`](../../Makefile)); CI runs that matrix on every pull request, one job per combination. The `cli` job also starts the built binary and drives the console through a real pty (`examples/cli/cli_e2e_startup.py`), and the **Telegram stand (tgfake)** job runs the Telegram bot against the release binary of [tgfake](tgfake.md) (`examples/gateway/tg_e2e_offline.sh`; the binary it runs is built with `gateway metrics`, and the script scrapes the exporter after the chat). The **Docker image** job builds the runtime image for **`linux/amd64`** and **`linux/arm64`**, the platforms a release pushes, and checks that each variant carries a binary of its own platform (**`make check-image`**, see [Checking the platforms of the image](../getting-started/docker.md#checking-the-platforms-of-the-image)). **`make test-race`** runs the whole tree under the Go race detector with every tag but `ui`; CI runs it on every pull request in the **Race detector** job, and `GOFLAGS=-count=3 make test-race` repeats every test locally for a race that shows up rarely. Two groups can be run on their own: **`make test-cache`** is the prompt-cache group (the `features/prompt_cache_*.feature` specs and the `TestPromptCache*` tests, part of `make test`: what every request sends the provider, held byte for byte across a session), and **`make test-perf`** is the performance group (the benchmarks of the `perf_test.go` files, never part of `make test`: timings of the work a turn repeats and the context a session over this repository's own rules costs, as `system-tokens`, `request-tokens` and `duplicate-rules`).

On Windows, run Make through Git Bash. The build and install targets use Go's
executable suffix (`coddy.exe`), including the binary that `make docs` runs.
Fixtures escape native paths in JSON and YAML, and packaging checks accept CRLF
checkouts. POSIX mode assertions do not apply on Windows; tests that induce
failures through Unix directory or file permissions skip there (and when run as
root). The ACP symlink scenario skips only when Windows denies the symlink
privilege; enable Developer Mode or run an elevated terminal to exercise it.
Docker asset checks also cover Git checkouts with `core.symlinks=false` by
reading link modes from the index.

Documentation checks accept CRLF code fences and measure SVG text with LF line
endings, so Windows checkout conversion does not change the generated inventory.

## Release binaries (CI)

On each SemVer git tag **`X.Y.Z`** that is on **`main`**, the [**Release binaries**](../../.github/workflows/release-binaries.yaml) workflow (separate from Docker CI) uploads archives to the matching **GitHub Release**:

| Archive | Platform |
|---------|----------|
| **`coddy_X.Y.Z_linux_amd64.tar.gz`** | Linux x86_64 |
| **`coddy_X.Y.Z_linux_arm64.tar.gz`** | Linux arm64 |
| **`coddy_X.Y.Z_android_arm64.tar.gz`**, **`coddy_X.Y.Z_android_amd64.tar.gz`** | Android arm64 and x86_64, under Termux (see [above](#android-termux)) |
| **`coddy_X.Y.Z_windows_amd64.zip`** | Windows x86_64 (**`coddy.exe`**) |
| **`coddy_X.Y.Z_darwin_amd64.tar.gz`** | macOS Intel |
| **`coddy_X.Y.Z_darwin_arm64.tar.gz`** | macOS Apple Silicon |
| **`coddy_X.Y.Z_linux_amd64.deb`**, **`coddy_X.Y.Z_linux_arm64.deb`** | Debian, Ubuntu and derivatives |
| **`coddy_X.Y.Z_linux_amd64.rpm`**, **`coddy_X.Y.Z_linux_arm64.rpm`** | Fedora, RHEL, openSUSE and derivatives |
| **`coddy.rb`** | Homebrew cask for the macOS archives of this tag; the same file is committed to the tap [coddy-project/homebrew-coddy-agent](https://github.com/coddy-project/homebrew-coddy-agent) |
| **`SHA256SUMS`** | Checksums for every archive and package above |

The **`.tar.gz`** archives carry the man page and the shell completions beside the binary; the
packages wrap the Linux binaries the same job just built rather than compiling their own, so the
**`.deb`**, the **`.rpm`** and the **`.tar.gz`** of one tag hold byte-identical executables.

Tags match the full feature set: **`http`**, **`ui`**, **`scheduler`**, **`memory`**, **`cli`**, **`gateway`**, **`swarm`**, **`metrics`**. Manual run after a tag exists:

```bash
gh workflow run "Release binaries" --ref X.Y.Z -f tag=X.Y.Z
```

## AppSec scan

**`make security`** runs the project's security gate over the checkout — trivy
(dependency vulnerabilities, secrets; misconfig report-only), semgrep (SAST)
and govulncheck (Go vulnerabilities the code reaches, the pinned toolchain's
standard library included) — with the same versions and thresholds the
[**Security scan**](../../.github/workflows/security.yaml) workflow uses on
pull requests. **`make sec-report`** scans without failing on findings. Reports
land in **`dist/security/`**. Runner, gate knobs and triage:
[AppSec scanning](security-scanning.md).

## **`go install` from upstream**

```bash
go install github.com/EvilFreelancer/coddy-agent/cmd/coddy@latest
```

That compiles whatever the module default is **without** your local **`TAGS`**. For a known set of features (HTTP, UI, scheduler, memory, console, messenger gateway), clone the repo and use **`make build TAGS="http ui scheduler memory cli gateway swarm metrics"`** (or **`go build -tags=...`** as above).
