# Docker

Run Coddy as **`coddy http`** inside a minimal **`scratch`** image. The default image ships the embedded web UI (**`ui`** build tag), OpenAI-compatible REST (**`http`**), scheduler (**`scheduler`**), and long-term memory (**`memory`**).

Related files:

- [`Dockerfile`](../Dockerfile) - multi-stage build (**Node** UI bundle, **Go** binary, **`scratch`** runtime)
- [`docker-compose.yml`](../docker-compose.yml) - run **`ghcr.io/coddy-project/coddy-agent`** (default **`docker compose`**)
- [`docker-compose.dev.yml`](../docker-compose.dev.yml) - build from source, publish port **12345**, volumes
- [`.dockerignore`](../.dockerignore) - keeps context small; never commit **`config.yaml`** with secrets
- [`examples/httpserver/docker.sh`](../examples/httpserver/docker.sh) - automated smoke test

Published images: **[coddy-agent on GHCR](https://github.com/coddy-project/coddy-agent/pkgs/container/coddy-agent)** (`ghcr.io/coddy-project/coddy-agent`). CI builds **multi-arch** manifests (**`linux/amd64`**, **`linux/arm64`**) on SemVer tags and pushes floating aliases (**`latest`**, **`MAJOR.MINOR`**, **`MAJOR`**) when appropriate - see [`.github/workflows/docker-build-push.yaml`](../.github/workflows/docker-build-push.yaml).

On Apple Silicon or arm64 Linux hosts, pull the image as usual; Docker selects **`arm64`** automatically. To pin a platform explicitly:

```bash
docker pull --platform linux/arm64 ghcr.io/coddy-project/coddy-agent:latest
docker pull --platform linux/amd64 ghcr.io/coddy-project/coddy-agent:latest
```

General build instructions without Docker - **[docs/build.md](build.md)**.

## Prerequisites

- **Docker** with **Compose V2** (**`docker compose`**, not only legacy **`docker-compose`**)
- A **`config.yaml`** you mount read-only into the container (start from **`config.example.yaml`**). Do not commit secrets.
- For the web UI, a browser on the machine that can reach the published host port (default **12345**)

## Docker Compose

Compose is the recommended way to run the published GHCR image or a locally built one. Both files define a **single service** named **`coddy`** - no database or Redis sidecar. The process is **`coddy http`** bound to **`0.0.0.0:12345`** inside the container; Compose maps that port to the host.

| File | When to use |
|------|-------------|
| [`docker-compose.yml`](../docker-compose.yml) | Pull a release from GHCR (**`docker compose pull`**) - day-to-day and production-like runs without building on the host |
| [`docker-compose.dev.yml`](../docker-compose.dev.yml) | **`build:`** from the repo [`Dockerfile`](../Dockerfile) - hacking on Coddy, reproducing CI, or running **`examples/httpserver/docker.sh`** |

Compose V2 merges an optional **`docker-compose.override.yml`** in the same directory (git-ignored by convention) so you can pin an image tag, change ports, or add **`environment`** without editing the tracked file.

### What the `coddy` service runs

| Setting | Default compose | Dev compose |
|---------|-----------------|-------------|
| **Image** | **`${CODDY_IMAGE:-ghcr.io/coddy-project/coddy-agent:latest}`** | **`coddy-agent:${CODDY_VERSION:-dev}`** (built locally) |
| **Command** | Image **`CMD`**: **`http -H 0.0.0.0 -P 12345`**, overridable with **`CODDY_COMMAND`** | Same |
| **Published port** | **`${CODDY_HTTP_PORT:-12345}:12345`** | Same |
| **Working dir** | **`/workspace`** (**`CODDY_CWD`**) | Same |

**Healthcheck** - **`coddy --version`** every 5s (container "healthy" once the binary responds; HTTP readiness is separate - use **`curl /v1/models`** after **`up`**).

**Logging** - **`json-file`** driver with **`max-size: 100k`** per file to avoid unbounded log growth on long-lived hosts.

### Host paths and environment

Bind mounts (override host paths with env vars before **`up`**):

| Host (default) | Container | Variable |
|----------------|-------------|----------|
| **`./config.yaml`** | **`/home/user/.coddy.yaml`** (read-only) | **`CODDY_CONFIG`** |
| **`./workspace`** | **`/workspace`** | **`CODDY_CWD`** |
| **`./coddy_home`** | **`/home/user/.coddy`** | **`CODDY_HOME`** |

Fixed inside the container (set by compose **`environment`**):

| Variable | Value | Role |
|----------|-------|------|
| **`CODDY_CONFIG`** | **`/home/user/.coddy.yaml`** | Points the loader at the mounted file (not **`$CODDY_HOME/config.yaml`**) |
| **`CODDY_HOME`** | **`/home/user/.coddy`** | Sessions, skills, scheduler store |
| **`CODDY_CWD`** | **`/workspace`** | Default cwd for new sessions and tool paths |

Provider keys can be injected from the host shell (optional, empty if unset):

- **`OPENAI_API_KEY`**, **`ANTHROPIC_API_KEY`**, **`DEEPSEEK_API_KEY`**

Prefer mounting secrets via config or your orchestrator; do not commit real keys.

### Compose commands

**Published image** (from repo root or any directory where you keep **`config.yaml`**, **`workspace/`**, **`coddy_home/`**):

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs -f coddy
curl -sS http://127.0.0.1:12345/v1/models | head
docker compose restart coddy
docker compose down
```

Pin a GHCR tag without editing YAML:

```bash
export CODDY_IMAGE=ghcr.io/coddy-project/coddy-agent:0.2.0
docker compose pull
docker compose up -d
```

Change only the host port:

```bash
export CODDY_HTTP_PORT=8080
docker compose up -d
# UI: http://127.0.0.1:8080/
```

**Build from source** (dev compose file):

```bash
docker compose -f docker-compose.dev.yml build coddy
docker compose -f docker-compose.dev.yml up -d --build
docker compose -f docker-compose.dev.yml logs -f coddy
docker compose -f docker-compose.dev.yml down
```

Optional build args on the dev file:

```bash
export CODDY_VERSION="$(git describe --tags --dirty 2>/dev/null || echo dev)"
export CODDY_BUILD_TAGS="http,scheduler,ui,memory,gateway"
docker compose -f docker-compose.dev.yml build coddy
```

**`CODDY_BUILD_TAGS`** must stay comma-separated with **no spaces**, matching **`go build -tags=`**. The dev file defaults to **`http,scheduler,ui,memory,gateway`** (matching the [`Dockerfile`](../Dockerfile) **`BUILD_TAGS`** default) so the built image can run the messenger gateway; drop **`gateway`** to trim it.

### Run another mode (messenger gateway)

Both compose files run **`coddy http`** by default. Override the subcommand with **`CODDY_COMMAND`** (shell-split into args) to run any other mode - for the [messenger gateway](gateway.md):

```bash
# Build from source: the dev image already includes the `gateway` tag.
export TELEGRAM_BOT_TOKEN="<bot-token>"          # or leave in $CODDY_HOME/.env
export CODDY_COMMAND="gateway --cwd /workspace"
docker compose -f docker-compose.dev.yml up -d --build
docker compose -f docker-compose.dev.yml logs -f coddy   # expect: "telegram bot connected"
```

Notes:

- The **published GHCR image is built without the `gateway` tag** (CI [`docker-build-push.yaml`](../.github/workflows/docker-build-push.yaml) sets **`BUILD_TAGS=http,scheduler,ui,memory`**), so **`docker-compose.yml`** with **`CODDY_COMMAND=gateway`** needs an image built with it - use the dev file or a custom **`CODDY_IMAGE`**.
- The bot token is read from **`TELEGRAM_BOT_TOKEN`** (passed through by both compose files) or **`$CODDY_HOME/.env`**; keep it out of git.
- If your **`gateways.telegram.proxy`** points at a host-local proxy (e.g. **`socks5://127.0.0.1:7890`**), it is unreachable from inside the container - use **`host.docker.internal`** or add **`network_mode: host`** in a **`docker-compose.override.yml`**.
- Gateway mode uses no inbound port (Telegram long-polling); the mapped **`12345`** is simply unused.

### Override example

Create **`docker-compose.override.yml`** next to **`docker-compose.yml`** (Compose loads it automatically):

```yaml
services:
  coddy:
    image: ghcr.io/coddy-project/coddy-agent:0.2.0
    ports:
      - "8080:12345"
    environment:
      OPENAI_API_KEY: ${OPENAI_API_KEY}
```

Use overrides for machine-specific ports, pinned tags, or extra **`environment`** - keep secrets out of git.

### Upgrade and rollback

```bash
export CODDY_IMAGE=ghcr.io/coddy-project/coddy-agent:NEW_TAG
docker compose pull
docker compose up -d
```

Sessions and config on the host (**`./coddy_home`**, mounted **`config.yaml`**) survive image swaps. If a new binary fails health checks, inspect **`docker compose logs coddy`** and roll back **`CODDY_IMAGE`** to the previous tag.

## Quick start (Compose + web UI)

From a checkout of this repository (or any folder where you keep **`config.yaml`**, **`workspace/`**, and **`coddy_home/`**):

**1. Prepare config and directories**

```bash
cp config.example.yaml config.yaml
mkdir -p workspace coddy_home
```

Edit **`config.yaml`**: configure at least one entry under **`providers`** and **`models`**, and set **`agent.model`** to a listed model id. You can leave **`api_key`** empty and pass **`OPENAI_API_KEY`** (or **`NAME_API_KEY`** for provider **`name`**) through compose **`environment`** instead.

Optional: set **`httpserver.host`** to **`0.0.0.0`** and **`httpserver.port`** to **12345** in YAML. The container **`CMD`** already runs **`coddy http -H 0.0.0.0 -P 12345`**, so flags apply even if **`httpserver`** is omitted from the file.

**2. Start Coddy** (published image from GHCR):

```bash
docker compose pull
docker compose up -d
```

**3. Connect with the bundled UI**

Open in a browser on the host (same machine as Docker, unless you forwarded the port):

```text
http://127.0.0.1:12345/
```

What you get:

| URL | Purpose |
|-----|---------|
| **`/`** | Embedded SPA (chat composer, sessions, tools in transcript) |
| **`/#/settings`** | Live **`config.yaml`** editor (**`GET/PUT /coddy/config`**) |
| **`/docs/`** | Swagger UI |
| **`/v1/models`** | Model and mode list (also used by the SPA model picker) |

Typical first-time flow in the UI:

1. Confirm the page loads (static assets from **`go:embed`**).
2. In the composer toolbar, select a **backend model** (rows with **`owned_by`** other than **`coddy`** come from your YAML **`models`** list).
3. Switch **agent** vs **plan** mode if needed (session operating profiles).
4. Type a message and send. The UI calls **`POST /v1/responses`** with **`stream: true`** and shows streaming assistant output.
5. Files created or edited by tools land under the mounted workspace (**host `./workspace`** → container **`/workspace`**, **`CODDY_CWD`**).

Sessions and skills state persist under **`./coddy_home`** on the host (**`CODDY_HOME`** in the container).

**4. Sanity check (optional)**

```bash
curl -sS http://127.0.0.1:12345/v1/models | head
docker compose logs -f coddy
```

**Security:** **`coddy http`** has no application-level auth. Treat port **12345** like any admin API - bind to localhost, use a firewall, or put a reverse proxy with TLS and authentication in front for remote access.

For a local **`Dockerfile`** build, use **`docker-compose.dev.yml`** - see [Docker Compose](#docker-compose) above.

## What the image contains by default

**`Dockerfile`** **`ARG BUILD_TAGS`** defaults to **`http,scheduler,ui,memory`** (comma-separated, same meaning as **`go build -tags=`**).

- **`http`** - **`coddy http`** and REST gateway (see **[docs/http-api.md](http-api.md)**).
- **`ui`** - embedded SPA on **`/`** (needs **`http`**).
- **`scheduler`** - scheduler subsystem (**[docs/scheduler.md](scheduler.md)**).
- **`memory`** - long-term memory copilot and session memory REST (**[external/memory/README.md](../external/memory/README.md)**); toggle runtime behavior via **`memory.enabled`**.

To build an image **without** memory or the embedded UI, override **`BUILD_TAGS`** (for example **`http,scheduler,ui`** or **`http,scheduler`**) via **`docker compose` `args`** or **`docker build --build-arg`**.

Volume and environment details for Compose are in [Docker Compose](#docker-compose). On a bare-metal install without **`CODDY_CONFIG`**, the loader prefers **`$CODDY_HOME/config.yaml`** (see **`docs/config.md`**).

## How the Dockerfile stages work

1. **`ui-builder` (Node)** - runs **`npm ci`** and **`npm run build:go`** under **`external/ui`**, producing the static bundle copied into the Go tree for **`go:embed`** when **`ui`** is in **`BUILD_TAGS`**.
2. **`build` (Go)** - **`CGO_ENABLED=0`**, **`GOOS`/`GOARCH`** from BuildKit **`TARGETOS`/`TARGETARCH`** (CI builds **`linux/amd64`** and **`linux/arm64`**), **`go build -tags="$BUILD_TAGS"`** with **`-trimpath`** and **`-ldflags "-s -w -X ...Version=..."`**, writes **`/out/coddy`**, copies **`ca-certificates.crt`** for HTTPS clients.
3. **`scratch`** - only the binary and CA bundle; **`ENTRYPOINT`** **`/bin/coddy`**, default **`CMD`** **`http -H 0.0.0.0 -P 12345`**.

## Automated smoke test

```bash
./examples/httpserver/docker.sh
```

The script builds a temporary **`config.yaml`**, brings up **`coddy`** with **`docker-compose.dev.yml`**, waits for **`/v1/models`**, then runs **`examples/httpserver/http_smoke_gateway.py`**.
