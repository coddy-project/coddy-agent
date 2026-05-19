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

Override the image tag if needed:

```bash
export CODDY_IMAGE=ghcr.io/coddy-project/coddy-agent:0.2.0
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

## Build and run from source (Compose)

Use [`docker-compose.dev.yml`](../docker-compose.dev.yml) when you want to build the image from the local **`Dockerfile`**:

```bash
docker compose -f docker-compose.dev.yml build coddy
docker compose -f docker-compose.dev.yml up -d --build coddy
```

Optional build args (same variables as in the compose file):

```bash
export CODDY_VERSION="$(git describe --tags --dirty 2>/dev/null || echo dev)"
export CODDY_BUILD_TAGS="http,scheduler,ui,memory"
docker compose -f docker-compose.dev.yml build coddy
```

**`CODDY_BUILD_TAGS`** must stay **comma-separated** with **no spaces**, matching **`go build -tags=`**.

After **`up`**, open the UI the same way: **`http://127.0.0.1:12345/`** (or **`${CODDY_HTTP_PORT}`** if you change the host mapping in a custom override).

## What the image contains by default

**`Dockerfile`** **`ARG BUILD_TAGS`** defaults to **`http,scheduler,ui,memory`** (comma-separated, same meaning as **`go build -tags=`**).

- **`http`** - **`coddy http`** and REST gateway (see **[docs/http-api.md](http-api.md)**).
- **`ui`** - embedded SPA on **`/`** (needs **`http`**).
- **`scheduler`** - scheduler subsystem (**[docs/scheduler.md](scheduler.md)**).
- **`memory`** - long-term memory copilot and session memory REST (**[external/memory/README.md](../external/memory/README.md)**); toggle runtime behavior via **`memory.enabled`**.

To build an image **without** memory or the embedded UI, override **`BUILD_TAGS`** (for example **`http,scheduler,ui`** or **`http,scheduler`**) via **`docker compose` `args`** or **`docker build --build-arg`**.

## Volumes and environment

Both compose files mount:

| Mount | Purpose |
|-------|---------|
| **`${CODDY_CONFIG:-./config.yaml}`** → **`/home/user/.coddy.yaml`** | Read-only config (**`CODDY_CONFIG`**) |
| **`${CODDY_CWD:-./workspace}`** → **`/workspace`** | Workspace (**`CODDY_CWD`**) |
| **`${CODDY_HOME:-./coddy_home}`** → **`/home/user/.coddy`** | Sessions, skills, scheduler data (**`CODDY_HOME`**) |

Override host paths:

```bash
export CODDY_CONFIG="$PWD/my-coddy.yaml"
export CODDY_CWD="$PWD/myproject"
export CODDY_HOME="$PWD/coddy-state"
docker compose up -d
```

The compose files set **`CODDY_CONFIG`** inside the container to the **mounted file** path (**`/home/user/.coddy.yaml`**). On a normal host install without **`CODDY_CONFIG`**, the loader prefers **`$CODDY_HOME/config.yaml`** (see **`docs/config.md`**).

Optional provider keys can be passed as environment variables (see compose **`environment`**). Prefer **mounted config** or your secret manager for production; **do not** commit real keys.

Change the published HTTP port on the host:

```bash
export CODDY_HTTP_PORT=8080
docker compose up -d
# UI: http://127.0.0.1:8080/
```

## How the Dockerfile stages work

1. **`ui-builder` (Node)** - runs **`npm ci`** and **`npm run build:go`** under **`external/ui`**, producing the static bundle copied into the Go tree for **`go:embed`** when **`ui`** is in **`BUILD_TAGS`**.
2. **`build` (Go)** - **`CGO_ENABLED=0`**, **`GOOS`/`GOARCH`** from BuildKit **`TARGETOS`/`TARGETARCH`** (CI builds **`linux/amd64`** and **`linux/arm64`**), **`go build -tags="$BUILD_TAGS"`** with **`-trimpath`** and **`-ldflags "-s -w -X ...Version=..."`**, writes **`/out/coddy`**, copies **`ca-certificates.crt`** for HTTPS clients.
3. **`scratch`** - only the binary and CA bundle; **`ENTRYPOINT`** **`/bin/coddy`**, default **`CMD`** **`http -H 0.0.0.0 -P 12345`**.

## Automated smoke test

```bash
./examples/httpserver/docker.sh
```

The script builds a temporary **`config.yaml`**, brings up **`coddy`** with **`docker-compose.dev.yml`**, waits for **`/v1/models`**, then runs **`examples/httpserver/http_smoke_gateway.py`**.
