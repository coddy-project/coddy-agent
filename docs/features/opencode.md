# OpenCode

A `type: opencode` provider gives Coddy the models of an [OpenCode](https://opencode.ai) gateway - the **Go** subscription or the pay-as-you-go **Console** (Zen) - with one API key. OpenCode is only the backend here: the agent stays Coddy's own, with its system prompt, its tools, its permission gate and its ReAct loop, on every surface - the console, the web UI, ACP editors and the Telegram gateway.

OpenCode curates a catalog of coding models (Grok, GPT, Claude, Qwen, GLM, Kimi, DeepSeek, MiniMax, MiMo and the rest) and serves each over the wire protocol that fits it. Coddy picks that protocol per model, so one provider row reaches the whole catalog.

## Get a key

1. Sign in to the [OpenCode Console](https://opencode.ai/console).
2. Subscribe to **Go** (or **Go Plus**), or add credits for **Console**, and add your billing details.
3. Copy the API key.

## Use the key

Paste the key into the provider row, or let the login store it for you.

```yaml
# yaml-language-server: $schema=https://coddy.dev/config.schema.json
providers:
  - name: opencode
    type: opencode
    api_key: "${OPENCODE_API_KEY}"   # or a literal key, or empty to read OPENCODE_API_KEY
    # api_base: https://opencode.ai/inference/v1   # Console; omit for the Go default
models:
  - model: opencode/glm-5.3
  - model: opencode/gpt-6-luna
  - model: opencode/claude-haiku-5-5
agent:
  model: opencode/glm-5.3
```

An empty `api_key` reads the `OPENCODE_API_KEY` environment variable (the usual `NAME_API_KEY` rule, so a row named `opencode` reads `OPENCODE_API_KEY`), then the key a login stored. The `${OPENCODE_API_KEY}` form is expanded when the file loads; a bare empty value is read at call time.

### Store the key with a login

```bash
coddy providers login opencode
```

OpenCode has no browser or device flow - the key is copied from the web console - so the login is a paste. It reads the key from the terminal (without echo) or from `--api-key`, stores it at `$CODDY_HOME/providers/opencode/opencode-auth.json` with mode `0600`, and, unless `--no-config` is given, writes the provider row, the gateway's models and an `agent.model` into `config.yaml`:

```text
$ coddy providers login opencode
Copy your API key from https://opencode.ai/console, then paste it here.
API key:
Stored the OpenCode key for https://opencode.ai/inference/go/v1 at /home/jane/.coddy/providers/opencode/opencode-auth.json
Updated /home/jane/.coddy/config.yaml: provider opencode, agent.model opencode/glm-5.3-flash, 39 models (opencode/glm-5.3-flash, opencode/kimi-k3, opencode/deepseek-v4-flash, ...)
```

An explicit `api_key`, `api_key_command` or `OPENCODE_API_KEY` still wins over the stored login. `coddy providers logout opencode` deletes the stored file; the key stays valid until you revoke it in the console. `coddy providers list` shows which source a row uses, with the key masked.

## The two gateways

`api_base` picks the gateway; both take the same key shape, but a key is issued for one of them.

| Gateway | `api_base` | What it is |
|---------|-----------|------------|
| **Go** (default) | `https://opencode.ai/inference/go/v1` | the Go / Go Plus monthly subscription |
| **Console** | `https://opencode.ai/inference/v1` | pay-as-you-go credits (Zen) |

An empty `api_base` means Go. A value that names neither gateway falls back to Go, so an OpenCode key is never sent to an arbitrary host. In the web UI's **Settings → LLM providers**, the row shows a gateway picker instead of a free-text base. `CODDY_OPENCODE_BASE_URL` moves the whole process to another address, for a stand or a test.

## The wire protocol per model

The OpenCode gateway serves different models over different protocols, and its `/models` list carries no hint of which - only ids. Coddy chooses from the model id:

| Protocol | Endpoint | Model families |
|----------|----------|----------------|
| OpenAI Responses | `/responses` | `grok-*`, `gpt-*`, `muse-*` |
| Anthropic | `/messages` | `claude-*`, `qwen*`, `minimax-*` |
| OpenAI-compatible | `/chat/completions` | `glm-*`, `kimi-*`, `deepseek-*`, `longcat-*`, `mimo-*`, `hy*`, `space-bunny`, the free models, and anything else |

An id this table does not know falls back to `/chat/completions`, the most common shape. The mapping mirrors the catalog in the [OpenCode Go docs](https://opencode.ai/v2/docs/console/go/) and the [Console docs](https://opencode.ai/docs/zen/); when OpenCode adds a family on a new protocol, the map in `internal/llm/opencode.go` (`opencodeProtocolFor`) is what changes.

Reasoning levels work as they do for any provider: the level a model entry resolves to is sent as the OpenAI Responses `reasoning.effort`, the Anthropic thinking budget, or the OpenAI-compatible `reasoning_effort`, per the protocol that model uses.

## Required client headers

The gateway is abuse-monitored and asks every client to identify itself and to keep a conversation on one route. Coddy sends, on every request it makes to OpenCode - completions, the model list, all three protocols:

- `User-Agent: coddy/<version>`, its own name rather than a generic SDK or HTTP-library one;
- `x-opencode-session: <session id>`, the stable id of the conversation, so OpenCode can optimize routing and reuse its prompt cache. A call that is not a conversation (a model list, a `--dry-run` probe) sends a generated id.

Both are injected over the row's own transport, so they never open a route that bypasses `providers[].proxy`.

## Proxy

`providers[].proxy` routes every OpenCode request - completions on all three protocols, the model list and a login's catalog fetch - exactly as it does for any other provider: `inherit` follows the environment's proxy, `none` connects directly, and a URL goes through that proxy. See [Provider proxy](../getting-started/configuration.md#provider-proxy).

## Usage limits

OpenCode meters each model against a monthly dollar budget (with 5-hour and weekly sub-limits), and you can watch it in the [console](https://opencode.ai/console). OpenCode publishes no usage API for a client to read, so Coddy shows no usage panel for an `opencode` row: the `usage_limits_panel` switch has nothing to drive, and a model's context window falls back to `max_context_tokens` or 128000, since the gateway's `/models` reports none.

## Testing

- Unit tests: `internal/llm/opencode_test.go` pins the protocol map, the gateway resolution and its env override, the required headers and the per-model routing against an `httptest` stand that serves all three protocols, the credential round-trip and the reasoning-carrier isolation from the Codex one.
- The Responses transport is shared with the Codex backend (`internal/llm/responses_stream.go`); `internal/llm/codex_test.go` covers it.

## See also

- [Configuration](../getting-started/configuration.md) - providers, models and the credential order.
- [Environment variables](../reference/environment-variables.md) - `OPENCODE_API_KEY`, `CODDY_OPENCODE_BASE_URL`.
- [Config reference](../reference/config.md) - the `providers[]` and `models[]` fields.
