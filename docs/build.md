# Building Coddy from source

This page is the detailed reference for local builds. For a short version, see [Installation](../README.md#installation) in the root **README**.

## Prerequisites

- **Go** - match `go` in [`go.mod`](../go.mod) (currently **1.25**).
- **Git** - the Makefile embeds a version string from tags or `git describe` when available.
- **Node.js and npm** - required only when you build with both **`http`** and **`ui`**, because the Makefile runs **`ui-build`** (see [`Makefile`](../Makefile)) to produce the assets that **`go:embed`** picks up.

Optional:

- **`golangci-lint` v2.x** (built with Go **1.25** or newer) - for **`make lint`**. v1.x releases compiled with older Go toolchains cannot load configs when **`go.mod`** targets **1.25**.

## Recommended full binary (HTTP, UI, scheduler, memory)

Build with **`memory`** to link long-term memory (`external/memory`). Enable behavior at runtime with **`memory.enabled`** in config (see [`external/memory/README.md`](../external/memory/README.md)).

The **HTTP gateway**, **embedded SPA**, **scheduler**, and **memory** are controlled by Go build tags. For a single binary that matches the default **Docker** image and includes every optional feature:

```bash
make build TAGS="http ui scheduler memory"
```

Output: **`build/coddy`**.

Equivalent **`go build`** (after `ui-build` when you use **`ui`**, or use **`make build`**, which runs **`ui-build`** automatically when **`TAGS`** contains both **`http`** and **`ui`**):

```bash
make ui-build   # only when using -tags=...,ui,... with http; Makefile runs this for you on `make build`
VERSION="$(make -s print-version)"
go build -tags=http,ui,scheduler,memory \
  -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=${VERSION}" \
  -o build/coddy \
  ./cmd/coddy/
```

The [**Dockerfile**](../Dockerfile) uses the same idea: comma-separated tags via **`BUILD_TAGS`** (default **`http,scheduler,ui,memory`**) and strips debug symbols with **`-ldflags "-s -w ..."`** in addition to the version **`X`** flag.

## Install on your PATH

**`make install`** depends on **`build`**, then copies **`build/coddy`**:

- **root** - **`/usr/local/bin/coddy`**
- **non-root** - **`~/.local/bin/coddy`** (ensure that directory is on **`PATH`**)

```bash
make install TAGS="http ui scheduler memory"
```

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
  -tags=http,ui,scheduler,memory \
  -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(make -s print-version)" \
  -o build/coddy \
  ./cmd/coddy/
```

## **`TAGS` vs `go build -tags`**

In **`Makefile`**, **`TAGS`** is **space-separated**:

```bash
make build TAGS="http ui scheduler memory"
```

**`go build`** expects a **comma-separated** list (no spaces):

```bash
go build -tags=http,ui,scheduler,memory ...
```

Order does not matter for these tags.

## Build tags reference

| Tag | Enables | Documentation |
|-----|---------|----------------|
| **`memory`** | Long-term memory copilot; with **`http`**, **`/coddy/sessions/{id}/memory/*`** REST; toggle runtime behavior with **`memory.enabled`** | [`external/memory/README.md`](../external/memory/README.md) |
| **`http`** | **`coddy http`**, OpenAI-shaped REST gateway, **`/docs`**, **`/openapi.yaml`** | [`docs/http-api.md`](http-api.md) · [`external/httpserver/`](../external/httpserver/) |
| **`ui`** | Embedded SPA on **`/`** (requires **`http`**; **`/`** returns **404** with **`http`** only) | [`docs/ui/README.md`](ui/README.md) · [`DESIGN.md`](../DESIGN.md) |
| **`scheduler`** | Scheduler daemon hooks, **`coddy_scheduler_*`** tools; with **`http`**, **`/coddy/scheduler`** REST | [`docs/scheduler.md`](scheduler.md) · [`external/scheduler/README.md`](../external/scheduler/README.md) |

**`make test`** exercises tag combinations (see **`test`** target in [`Makefile`](../Makefile)).

## **`go install` from upstream**

```bash
go install github.com/EvilFreelancer/coddy-agent/cmd/coddy@latest
```

That compiles whatever the module default is **without** your local **`TAGS`**. For a known set of features (HTTP, UI, scheduler, memory), clone the repo and use **`make build TAGS="http ui scheduler memory"`** (or **`go build -tags=...`** as above).
