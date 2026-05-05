# OpenAI-compatible HTTP API

The `coddy http` subcommand is available only when the binary is built with **`go build -tags=http`** (or `make build TAGS=http`). It exposes a subset of the [OpenAI REST shape](https://github.com/openai/openai-openapi/blob/manual_spec/openapi.yaml) backed by the same session manager and ReAct agent as **`coddy acp`**.

## Endpoints

| Method | Path | Notes |
|--------|------|--------|
| GET | `/v1/models` | Lists entries from config **`models`** (`id` equals **`model`** selector). |
| POST | `/v1/chat/completions` | Chat; supports **`stream: true`** (SSE) or non-streaming JSON. |
| POST | `/v1/responses` | MVP: JSON body `{"model":"...","input":"user text"}` (simplified vs full OpenAI Responses API). |
| GET | `/v1/responses/{id}` | MVP: returns metadata if **`id`** is an active session id. |

## Session behavior

- Without header: each `chat.completions` request that needs a new session calls ACP-style **`session/new`** (default cwd from **`--cwd`** / `CODDY_CWD`).
- **`X-Coddy-Session-ID`**: use an existing in-memory session (returns **404** if unknown).
- On the first response for a newly created session, the server may add **`X-Coddy-Session-ID`** (non-streaming and streaming) so clients can continue server-side history.

**Stateless mode (full `messages` every time)**: send the full OpenAI `messages` array; the last message must be **`user`**. Earlier messages become session prefix; the last user line is the new turn (same as the HTTP integration path in the agent).

## Permissions over HTTP

There is no interactive permission UI on HTTP. **`tools.permission_master_key`** bypasses prompts for both ACP and HTTP. Without it, gated tools that require confirmation will fail the turn unless session **`permission_grants.json`** already contains matching **`allow_always`** grants from a prior ACP session on disk.

## CLI

Flags match **`coddy acp`** where applicable (`--config`, `--home`, `--cwd`, `--sessions-dir`, `--disable-session`, `--session-id`, `--log-*`), plus:

- **`-H` / `--host`**: bind address (built-in default **`0.0.0.0`** unless **`httpserver.host`** overrides when flags stay at **`0.0.0.0`** and **`12345`**)
- **`-P` / `--port`**: port (built-in default **`12345`** unless **`httpserver.port`** overrides in the same case)

YAML **`httpserver.host`** and **`httpserver.port`** apply only when **`--host`** and **`--port`** are still exactly **`0.0.0.0`** and **`12345`**. Passing `-H`/`-P` always wins.

## Official client (Python)

```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:12345/v1", api_key="dummy")
client.models.list()
client.chat.completions.create(model="openai/gpt-4o", messages=[{"role": "user", "content": "hi"}])
```

Clients may still send **`api_key`**; Coddy ignores it for HTTP.

## Build

```bash
make build TAGS=http
# binary: build/coddy
```

Default **`make build`** does not include HTTP; `go test ./...` also skips the HTTP package unless you run **`go test -tags=http ./...`**.
