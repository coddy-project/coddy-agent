# Devin

A `type: devin` provider gives Coddy the models of a Devin (Cognition) account: Claude, GPT, Gemini, SWE, Kimi, GLM and the rest of the families the account's plan serves. You sign in the way the Devin CLI does, through the browser, or reuse the login `devin auth login` already holds. Devin is only the backend here: the agent stays Coddy's own, with its system prompt, its tools, its permission gate and its ReAct loop, on every surface - the console, the web UI, ACP editors and the Telegram gateway.

Usage is billed to the Devin account the same way a Devin CLI session is.

## Sign in

```bash
coddy providers login devin
```

The command opens the Devin sign-in page in a browser and waits. After you sign in, the page sends the browser back to `http://127.0.0.1:<port>/callback` on this machine, where Coddy receives the authorization code, exchanges it for a session token (PKCE, the same exchange `devin auth login` makes) and proves the token by minting a user JWT with it. Then it prints the account and writes three things:

- the session token, to `$CODDY_HOME/providers/devin/devin-auth.json` with mode `0600`;
- the provider row and one model per family the account can use, into `config.yaml`;
- `agent.model`, when the config has none yet.

```text
$ coddy providers login devin
Open https://app.devin.ai/auth/cli/continue?...
Sign in there; the page then sends the browser back to this machine.
If the browser runs on another machine, that last page will not load: copy its address from the address bar and paste it here.
Waiting for the sign-in to finish...
Signed in as Jane <jane@example.com>. Credential stored at /home/jane/.coddy/providers/devin/devin-auth.json
Updated /home/jane/.coddy/config.yaml: provider devin, agent.model devin/claude-opus-5, 39 models (devin/claude-opus-5, devin/claude-fable-5-1, devin/claude-sonnet-5, ...)
```

On a machine reached over SSH there is no browser to open, and a browser on your laptop cannot reach the server's loopback port. Open the printed address on the laptop, sign in, and when the browser lands on the `127.0.0.1` page that does not load, copy that address from the address bar and paste it into the terminal: it carries the code, and the sign-in completes the same way. A line that is not such an address is refused with a note and the wait goes on. The wait ends after ten minutes, or sooner with Ctrl-C.

`--no-config` stores the token and leaves `config.yaml` alone. A second login only adds what is missing: rows and models already in the file stay as you wrote them.

### Reuse the Devin CLI login

```bash
coddy providers login devin --devin-cli
```

With a Devin CLI installed and signed in, no browser is needed: the command checks the CLI's own login by minting a user JWT with it and publishes the catalog into `config.yaml`. The token stays where the Devin CLI keeps it; Coddy reads it at every request and never writes to that file. `devin auth logout` therefore ends the login for Coddy as well.

## Where the session token comes from

A request uses the first of these that holds a token:

1. the row's `api_key`, `api_key_command`, or the `DEVIN_API_KEY` variable (for a row named `devin`; the name follows the usual `NAME_API_KEY` rule). The value is a Devin session token, `devin-session-token$...`; a bare token gets that prefix;
2. the Coddy-managed login, `$CODDY_HOME/providers/<name>/devin-auth.json`;
3. the Devin CLI login, `credentials.toml` under `$XDG_DATA_HOME/devin` or `~/.local/share/devin` (on macOS also `~/Library/Application Support/devin`, on Windows `%LOCALAPPDATA%\devin`). `CODDY_DEVIN_CLI_CREDENTIALS` names the file explicitly.

`coddy providers list` shows which one a row uses, with the token masked:

```text
  devin (devin): signed in to Devin (jane@example.com, devin-session-token$…Q2xw)
```

`coddy providers logout devin` deletes the Coddy-managed file only. When a Devin CLI login exists, the row keeps working through it, and the command says so. The session itself stays valid on Devin's side until you end it there.

At startup `coddy acp` and the HTTP server of `coddy serve` log one `devin credential` line per devin row, naming the source it will use or warning that there is none.

## Models and reasoning levels

The Devin catalog lists every variant of a model as its own id: Claude Opus 5 comes as `claude-opus-5-low`, `-medium`, `-high`, `-xhigh` and `-max`, plus paid variants such as `-fast`. Coddy offers one model per family and reaches the variants through the reasoning level, so the login writes entries like this one:

```yaml
models:
  - model: devin/claude-opus-5
    reasoning_levels: [low, medium, high, xhigh, max]
    reasoning_default: medium
    max_context_tokens: 1000000
    multimodal: true
```

A turn on `devin/claude-opus-5` at level `high` is sent as `claude-opus-5-high`. The levels, the default, the context window and the image support all come from the catalog, per family; a family with one variant gets `reasoning_levels: []`, and the reasoning selector is hidden for it. Where a family has a `none` variant, the selector offers `off` next to `none`, and both pick that variant. A level the family lacks falls back to the family's default variant; `minimal` takes `minimal`, `none` or `low`, the first the family has.

Any full variant id works as a model as well, `devin/claude-opus-5-high-fast` for instance: an id the catalog knows is sent as written, whatever the reasoning level. So does an id the catalog does not list, and the Devin API server then answers for it.

`max_tokens` on a model entry caps the output; without it the variant's own limit from the catalog is used. The cap is lowered when the prompt plus the cap would not fit the context window, because the server answers an overshoot with an opaque internal error that no retry fixes. A `temperature` configured on the model is sent unless a reasoning level is in effect; without one the request carries `1.0`, as the Devin clients do.

Thinking streams into the transcript like any other provider's. Signed reasoning blocks (Claude, Gemini) are kept with the assistant message and replayed on the next request of the same variant, so the model continues its own chain of thought across tool calls.

## Configuration

```yaml
providers:
  - name: devin
    type: devin
    # api_key: devin-session-token$...   # optional, wins over the logins
    # proxy: none                        # the route of every request of the row

models:
  - model: devin/claude-sonnet-5
    reasoning_levels: [low, medium, high, xhigh, max]
    reasoning_default: medium
```

`api_base` is ignored for `type: devin`: a session token only ever goes to the Devin API server. The server comes from the login itself: an account served by a dedicated deployment records it with the token, and the user JWT can move chat to the account's own server. `proxy` routes the sign-in, the catalog and chat alike ([Provider proxy](../getting-started/configuration.md#provider-proxy)).

`coddy --dry-run` asks each devin row for its catalog and checks the configured models against the family list; a full variant id there is reported as a warning, since the family list does not name variants.

## How it works

The provider speaks the protocol the Devin CLI and Devin Desktop use with the Devin API server: Connect-RPC with protobuf messages. The session token is exchanged for a user JWT (`GetUserJwt`, cached until shortly before it expires), the catalog comes from `GetCliModelConfigs` (cached for thirty minutes per account), and a turn is one `GetChatMessage` server stream of gzip-compressed frames carrying text, thinking, tool-call fragments, usage and the stop reason. Coddy's system prompt travels as the request's system prompt, its tools as tool definitions, and tool results as tool messages. Usage counts the prompt cache: the reported input adds the uncached part, the cache write and the cache read, and the cache read is reported as cached input.

The protocol is not published by Cognition. Coddy follows what the official clients send, so a change on Devin's side can break the provider until Coddy follows it.

A stream the server ends with an error before any text reached the caller is retried under the usual rules (a rate limit, `resource_exhausted`, counts as HTTP 429); once text has streamed it is not, so no delta is shown twice. A stream cut before its end frame keeps the text it delivered and drops the tool calls of the unfinished answer.

## Stands and tests

`CODDY_DEVIN_API_SERVER_URL`, `CODDY_DEVIN_WEBAPP_URL` and `CODDY_DEVIN_API_URL` move the API server, the sign-in page and the code exchange for the whole process; `internal/devinfake` is the offline stand that plays all three, and `features/devin_provider.feature` drives the sign-in, the Devin CLI login and a full agent turn against it with no account and no network.
