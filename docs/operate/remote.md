# Remote mode

A `coddy serve` on one machine can be driven from another. The console and `coddy acp` take `--remote`, the web UI has an environment chip in its composer, and all three talk to the server's HTTP API: the agent, its tools, its sessions and its permission policy live on the server, while the client renders the transcript and answers the prompts. This page is the user guide; the design record with the reasoning and the parts that were deferred is [Remote control design](../plans/remote-control.md).

## What remote mode is

| Client | How it connects | What it needs from the server |
|---|---|---|
| console (`coddy`, `coddy cli`, `coddy -p "..."`) | `--remote <target>` | the HTTP API (`http` build tag, `httpserver.enable`, on by default) |
| editor over ACP (`coddy acp`) | `--remote <target>` | the same |
| web UI | the environment chip above the composer | the same, plus CORS on the server |

With `--remote` the local process keeps no session store, runs no scheduler and no agent loop; every session call is proxied to the server over `/v1/*` and `/coddy/*`, the streamed answer comes back over SSE and is translated into the updates the console and the ACP client already understand. Specs: `features/remote_client.feature` and `features/cli_remote.feature`; the live checks are `examples/cli/cli_e2e_remote.py`, `examples/acp/acp_remote.py` and `examples/httpserver/http_e2e_remote.py`.

## Naming a remote

`--remote` accepts three spellings:

- a **configured name** from `httpserver.remotes` in the client's own `config.yaml` (matched case-insensitively);
- a bare **`host:port`**, which gets `http://` in front;
- a full **`http://` or `https://` URL**, optionally with a path prefix.

The address must be a bare origin: a query string, a fragment or user credentials in the URL are refused. Named remotes hold a name and a URL only:

```yaml
httpserver:
  remotes:
    - name: nas02
      url: "https://nas02.example:12345"
```

```bash
coddy --remote nas02                 # by name
coddy --remote 192.168.1.20:12345    # bare host:port, plain http
coddy acp --remote https://nas02.example:12345
```

The same `remotes` list is what a server offers in its web UI's environment menu, so the entry does double duty: put it in the config of the machine you type on.

## The token

On the server, authentication is off until a token is set. `httpserver.auth_token` in `config.yaml` (with `${ENV}` expansion), `--auth-token` on `coddy serve`, or the `CODDY_HTTP_TOKEN` variable turn it on; then every `/v1/*` and `/coddy/*` route requires `Authorization: Bearer <token>` and answers `401` otherwise. The SPA shell and its static assets stay public so a browser can load the page and ask for the token; `/docs` and `/openapi.*` are protected unless `httpserver.public_docs: true`. `GET /coddy/config` never returns the token (it reports `auth_configured` only), and a token given by flag or environment survives a `PUT /coddy/config` hot reload without being written to the file. A server bound off loopback without a token logs a warning at startup unless `httpserver.allow_insecure: true`.

A token is what an API client presents. A browser has no field to type one into, which is why a token alone leaves the web UI showing `Unauthorized (401)`; the sign-in form below is the browser's half of the same gate.

On the client, the token comes from `--remote-token` or, when the flag is absent, from `CODDY_REMOTE_TOKEN`. It is deliberately never read from `config.yaml`, which is why `httpserver.remotes` has no token field. The web UI takes the token in its **+ Add remote...** form and keeps it in the browser only (`localStorage`, per remote).

`coddy serve` has no TLS of its own, so a token sent to a non-loopback `http://` address travels in clear; the console and `coddy acp` print a warning when that is about to happen. Put a TLS-terminating reverse proxy in front of the server, or reach it through an SSH tunnel, which keeps the address on loopback:

```bash
ssh -N -L 12345:127.0.0.1:12345 nas02   # in one terminal
coddy --remote 127.0.0.1:12345          # in another
```

## The sign-in form

A `coddy serve` on a network is readable by everyone who finds the port: transcripts, tool output, the workspace browser, the configuration editor. `httpserver.login` closes it with one operator account, and the form is what a browser signs in at. It is off by default; a configuration that does not mention it behaves exactly as before.

```bash
coddy serve set-password --user pasha
```

The command asks for the password twice, writes an argon2id hash into `config.yaml` in place (comments and key order survive) and switches the form on. From then on an anonymous browser gets a sign-in screen instead of the app, and nothing under `/v1/*` or `/coddy/*` is readable until it passes. The page itself and its static assets stay public, so the screen can load; `/docs` follows `public_docs` as before.

For a container or a systemd unit, the account can live in the environment instead, as `CODDY_HTTP_USER` and `CODDY_HTTP_PASSWORD` (typically in `$CODDY_HOME/.env`). They enable the form on their own, they win over an account in the file, and they are never written into it. An explicit `login.enable: false` switches the form off with the variables still set.

```yaml
httpserver:
  host: 0.0.0.0
  auth_token: "${CODDY_HTTP_TOKEN}"   # for coddy --remote, coddy acp --remote, a relay, scripts
  login:
    enable: true
    user: "pasha"
    password_hash: "$$argon2id$$v=19$$..."
    session_ttl_hours: 720            # 0 = until the browser closes
```

**The two credentials are not interchangeable.** The form is for browsers; everything that is not a browser - `coddy --remote`, `coddy acp --remote`, a swarm relay mounting this node, the Python harnesses, Swagger's **Authorize** - still presents a bearer token. Closing a server with a password alone and no `auth_token` is what breaks those, and `coddy serve` says so in its log at startup. Set both when anything but a browser talks to the server.

Signing in sets an `HttpOnly`, `SameSite=Strict` cookie that opens the same routes a token opens, streams included. Signing out (the entry at the bottom of the nav rail) drops the session on the server, so a copy of the cookie taken elsewhere stops working too. Sessions live in memory: saving settings from the page keeps them, restarting the process ends them, and rotating the password ends every session it opened. Full behaviour, including the CSRF rule for cookie-authenticated writes and the throttle on wrong passwords, is in [HTTP API](../reference/http-api.md#web-ui-sign-in-optional).

`coddy serve` still has no TLS of its own, so a password typed into a non-loopback `http://` page travels in clear exactly as a token does. Put a TLS-terminating reverse proxy in front of it, or reach it through the SSH tunnel above.

## CORS for the web UI

A browser that loaded the UI from one origin and calls the API of another needs the remote server to opt into CORS:

```yaml
httpserver:
  auth_token: "${CODDY_HTTP_TOKEN}"
  cors:
    enable: true
    allowed_origins: ["http://localhost:12345", "https://my-ui.example"]   # or ["*"]
  remotes:                       # optional: offered in this server's own UI
    - name: "prod box"
      url: "https://box.example:12345"
```

With `cors.enable` on, a preflight from an allowed origin gets `204` with `Access-Control-Allow-Origin` (the origin echoed, or `*` when configured) and `Access-Control-Allow-Headers: Authorization, Content-Type, X-Coddy-Session-ID`; an origin that is not listed gets no CORS headers, which the browser reports as a blocked request. The bearer token still applies to the real request. Because `EventSource` cannot send a header, the two SSE subscription routes, `GET /coddy/sessions/{id}/composer-stream` and `GET /coddy/events`, also accept `?access_token=`; the bundled UI fetches those streams instead, so its header applies and no token lands in a URL. Reference: [HTTP API](../reference/http-api.md#authentication-optional).

## The environment chip

The chip sits in the composer's workspace row, next to the folder, branch and worktree chips, and reads **Local** or the name of the active remote. Its menu has an **Environment** section with Local and a **Remote** section with the server's configured `remotes` plus **+ Add remote...** (name, URL, token). Choosing an entry connects at once and reloads the page, so the session list, the models and the defaults are re-read from the chosen backend; the SPA shell always comes from the local origin, so **Local** stays reachable from the chip even when the remote is down.

Each remote shows a status dot probed on menu open with a cross-origin `GET /v1/models`: green when reachable and authorised, red when unreachable, blocked by CORS or unauthorised, amber while probing. The active environment is probed again on load, every 30 seconds and on window focus; when it fails, a banner offers **Switch to Local** rather than leaving an empty screen. The choice and the per-remote tokens live in the browser (`coddy_env`, `coddy_env_tokens`) and are never written to the server's config; folder recents are kept per environment.

## What runs where

| On the server | On the client |
|---|---|
| the ReAct loop and the model calls, with the server's providers and keys | rendering the transcript, thinking, tool boxes, plan updates, token and context stats |
| file and shell tools, in the server's workspace | the model selector, filled from the server's `GET /v1/models` |
| MCP servers, hooks, subagents and their trust receipts | permission and question prompts, answered through `POST /coddy/sessions/{id}/permission` and `.../question` |
| the session bundles, the config file and the agent's self-configuration commits | cancel (`POST /coddy/sessions/{id}/cancel`), `ctrl+o` fetching full tool output |

A session a remote console or ACP client creates lives in the server's default working directory (`coddy serve --cwd`, `CODDY_CWD`, else where the server was started); the remote console footer still shows the local folder. The web UI can move a session to another server-side folder from its folder chip. `/export` writes into the server's workspace. `/resume`, `-c` and `--session-id` operate on the server's session list, and the local folder filter does not apply. The settings commands (`/model`, `/reasoning`, `/agent`, `/plan`, `/ask`, `/permissions`, with `--once` or `--count=N`) change the server's session through the same `PATCH /coddy/sessions/{id}` the web UI uses, the permission mode included, and every client of that session sees the change ([Session settings](../features/session-settings.md)). The console's `/reasoning [level]` and `shift+tab` persist the selected reasoning level on the server session. The startup banner shows `remote: <url>`, and the exit hint prints a reconnect command with `--remote` in it.

Trust decisions are the server's too. A project MCP server, hooks file or subagent definition is approved on that host, either with the CLI there (`coddy mcp trust`, `coddy hooks trust`, `coddy agents trust`) or over the API with the bearer token (`POST /coddy/mcp/{name}/trust`, `POST /coddy/hooks/trust`, `POST /coddy/subagents/{name}/trust`, each with the server-side workspace as `cwd`); the local subcommands do not take `--remote`. A subagent's permission prompt reaches the remote client under the parent session, prefixed `[subagent <name>]`, even when the server itself runs with `tools.permission_mode: bypass`. See [Subagents](../features/subagents.md#remote-mode).

## Limitations

- One bearer token per server, no users or roles: whoever holds it has the whole API, including the config editor and the workspace files.
- No TLS inside `coddy serve`; encryption comes from a proxy or a tunnel.
- A permission mode a remote client switches (`/permissions`, `--permission-mode`, the dialog's session switch) applies to the server's session for every client of it, and lives in the server's memory: a restart of `coddy serve` returns the session to `tools.permission_mode`.
- A dropped connection leaves the server turn, its children and any open prompt running; `/resume` shows the outcome once the turn ends, and an answer to a prompt the server has already withdrawn is ignored. Quitting the console mid-turn waits briefly for the cancel to reach the server.
- A session is one turn at a time: a second client prompting the same session gets `409` while the first turn holds the lock.
- The web UI reaches a remote only when that server lists the UI's origin in `cors.allowed_origins`; a red dot with a correct token usually means CORS.

## Checking a remote from the command line

```bash
coddy --dry-run --remote nas02
curl -sS -H "Authorization: Bearer $CODDY_REMOTE_TOKEN" https://nas02.example:12345/v1/models
```

`--dry-run` (on the console and on `coddy acp`) runs the config check and then probes what the file points at, the `--remote` target included, and exits with status 1 when a probe fails; the `curl` line is the same request the UI's status dot makes. Server-side, `coddy serve --dry-run` reports the listen address it would bind and whether a port is already taken. See [coddy serve and the daemon](serve.md).

## History

The design record, [Remote control design](../plans/remote-control.md), documents the direction that shipped - the authenticated API, CORS, the environment selector and the Go client behind `--remote` - and the parts that did not: TLS inside the server, and a reverse tunnel with an agent registry, which the [swarm relay](swarm.md) later covered with its reverse HTTP/2 tunnel.
