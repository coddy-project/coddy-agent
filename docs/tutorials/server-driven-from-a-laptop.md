# A server driven from a laptop

`coddy serve` on a server with a workspace and a model behind it; on the laptop only a client. The console, an editor over ACP and the web UI all speak to the same server, and the sessions stay there. The complete guide is [Remote mode](../operate/remote.md); the sources for the flags below are [Console](../surfaces/console.md), "Remote mode (`--remote`)", and [Authentication](../reference/http-api.md#authentication-optional).

1. **Give the server a token and bind it off loopback.** Bearer auth is off by default. Set the token through the environment or `--auth-token` rather than writing it into the file; a `${ENV}` reference in `httpserver.auth_token` works too. `host` defaults to `127.0.0.1` because one process starts every enabled subsystem, so asking for a bot must not open the API to the network as a side effect. `cors` is for the browser in step 4.

   ```yaml
   httpserver:
     host: "0.0.0.0"
     port: 12345
     auth_token: "${CODDY_HTTP_TOKEN}"
     cors:
       enable: true
       allowed_origins: ["http://localhost:12345"]   # the origin the laptop's UI is served from
   ```

   ```bash
   export CODDY_HTTP_TOKEN="a-long-random-string"   # or a line in ~/.coddy/.env
   coddy serve --dry-run    # binds the address once, names a port somebody else holds
   coddy serve --daemon     # foreground `coddy serve` under systemd or Docker
   ```

   The startup banner says what is reachable and how: `httpserver  http://0.0.0.0:12345  (bearer auth)`. Binding a non-loopback address without a token logs a warning at start, and clients warn when they send a token over plain http to an address that is not loopback: put TLS in front of the server, a reverse proxy is enough, when the path crosses a network you do not control.

2. **Drive it from the laptop's console.** `--remote` takes a bare `host:port` (scheme defaults to http), a full URL, or the name of a remote listed under `httpserver.remotes` in the laptop's own `config.yaml`. The token comes from `--remote-token` or `CODDY_REMOTE_TOKEN`, never from the file.

   ```bash
   export CODDY_REMOTE_TOKEN="a-long-random-string"
   coddy --dry-run --remote box.example:12345   # the target has to accept the token
   coddy --remote box.example:12345
   coddy -p "What changed in the last release?" --remote box.example:12345
   ```

   ```yaml
   httpserver:
     remotes:
       - name: "box"
         url: "https://box.example:12345"
   ```

   With the entry above, `coddy --remote box` is enough. The turn runs on the server in its workspace: tool boxes, thinking and token counts stream back, permission and question modals answer through the server, `/resume`, `-c` and `--session-id` pick from the server's session list, and the banner reads `remote: <url>`. Two things are refused remotely on purpose: `--permission-mode` (the server's configuration governs it) and the `!!` local shell (the workspace is not on this machine).

3. **Drive it from an editor.** `coddy acp` takes the same two flags, so an ACP client such as Zed points its agent command at the remote server; put `CODDY_REMOTE_TOKEN` in the editor's environment or pass `--remote-token`.

   ```bash
   coddy acp --remote box.example:12345
   ```

4. **Drive it from the browser.** The environment chip sits in the composer's workspace row and reads `Local` or the name of the remote in use. Its menu lists the remotes of the `coddy serve` that served the page (`httpserver.remotes`) and `+ Add remote…`, which takes a name, a URL and the bearer token; choosing an entry connects at once and reloads, and the token stays in the browser, never in a config file. The remote server has to allow the page's origin through `httpserver.cors`, which is what step 1 set; the dot on each entry turns green when a cross-origin `GET /v1/models` succeeds with the token ([Cross-origin access and the remote UI](../reference/http-api.md#cross-origin-access-and-the-remote-ui), [Web UI](../surfaces/web-ui.md)). Opening the server's own address instead loads the SPA shell without a token, since the shell is public, and the page asks for the token before it calls the API.

5. **Remember what stays on the server.** Sessions, the trust receipts and the child sessions of subagents are the server's, so a project file is approved there: `coddy mcp trust`, `coddy hooks trust` and `coddy agents trust` on the server itself, or `POST /coddy/mcp/{name}/trust`, `POST /coddy/hooks/trust` and `POST /coddy/subagents/{name}/trust` over the API. A dropped connection leaves the server turn running; `/resume` shows the outcome once it ends.
