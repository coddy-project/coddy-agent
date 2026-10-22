# Browser extension

Connect selected Chrome, Firefox or Yandex Browser tabs to your own `coddy serve`.
The extension connects directly to the browser listener on port 12348. There is no
hosted browser bridge. Page contents used by the agent still reach the model provider
configured in Coddy.

## Set up the server

Generate a random key of at least 32 characters and keep it in `<home>/.env` as
`CODDY_BROWSER_KEY`. Configure the listener in `config.yaml`.

```yaml
browser:
  enable: true
  host: 127.0.0.1
  port: 12348
  token: ${CODDY_BROWSER_KEY}
```

Start `coddy serve`. Browser tools work without an optional build tag. The chat in
the extension requires a binary built with `http`. For a remote server, bind the
listener to a reachable address and configure `cert_file` and `key_file`, or put it
behind an HTTPS reverse proxy that forwards WebSocket upgrades. The extension
accepts plain HTTP only for localhost, 127.0.0.1 and [::1].

The extension key is independent of `httpserver.auth_token`. It grants access to
browser registration, browser file transfers and the extension's conversation API.
It does not grant direct access to settings or workspace file routes. Anyone holding
the key can start agent conversations, which use the server's normal tool permissions.
The key reaches only the conversations it started: the chat list shows those alone,
and a permission prompt, question or transcript of a conversation somebody else
started is refused. Changing the key rebuilds the listener and disconnects
extensions using the old key.

## Install the extension

Build the extension from this checkout with Node.js.

```bash
node external/browser-extension/build.mjs
```

The build writes three directories under `external/browser-extension/dist/`.
In Chrome, open `chrome://extensions`, turn on Developer mode and load `dist/chrome`
as an unpacked extension. In Yandex Browser, use `browser://extensions` and load
`dist/yandex`. In Firefox, open `about:debugging`, select This Firefox, choose Load
Temporary Add-on and select `dist/firefox/manifest.json`. A temporary Firefox
installation lasts until the browser closes; permanent distribution requires signing.

Open Coddy from the browser toolbar. Enter the listener address, extension key and
a name such as `Firefox - laptop`, then click Connect. Check the tabs you want to
share. Other tabs are not sent to the server. Open keeps the interface in a sidebar
where supported, or opens a separate extension window in Yandex Browser.

![Connected extension with a shared tab](../assets/browser-extension/extension-connected-dark-1280.png)

*The extension connected to a local Coddy listener with one selected tab.*

## Work from a chat

Ask Coddy to list connected browsers and select one. The `browser` tool remembers
that choice in the session bundle. Every chat on the server can select a browser.
One chat owns a tab until its turn ends, it calls `release`, changes browsers or the extension
withdraws the tab. Another chat cannot change that tab during the lease.

The tool supports `list`, `select`, `release`, `snapshot`, `screenshot`, `navigate`,
`click`, `type`, `press`, `scroll`, `drag`, `upload_file` and `download_file`.
Snapshots return text and element refs; use a new snapshot after navigation.
Coordinates are viewport CSS pixels. Screenshots are tool-owned images, shown only
to models that accept images. Page contents are untrusted data.

The extension asks before a new website is used. Allow now applies to the pending
action; Always allow site stores that origin in the extension; Decline refuses it.
Origins include the scheme and port. Sharing a tab and allowing a website are
separate requirements. A saved website grant does not share other tabs at that site.
Stop cancels pending work and clears shared tabs. Select them again to resume.

The chat panel lists the conversations this key started, starts a new conversation,
streams the answer and shows tool permission requests and questions. Conversations
are stored on the server and can be continued from Coddy's other surfaces. The list
reads one page of the server's session store, so on a server with very many sessions
an older extension conversation may be missing from it; start a new chat or continue
the old one from another surface.

## Files and browser differences

`upload_file` reads a workspace-relative server file and assigns it to the file input
identified by `params.ref`. `download_file` fetches `params.url` in the browser and
saves the bytes to a new workspace-relative `path`. Files are limited to 32 MiB.
Downloads refuse redirects; use the final approved URL. Browser cookie policies and
container isolation can affect authenticated downloads. Transfers use expiring,
single-use HTTP tickets on the browser listener, never arbitrary server paths.

Chrome and Yandex Browser use the debugger API for keyboard and mouse input.
Firefox uses page APIs without a local component. Editors that require trusted
keyboard or mouse events need the optional Windows input adapter. Build and install
it on the machine running Firefox.

```powershell
go build -o build/coddy-browser-host.exe ./cmd/coddy-browser-host
./external/browser-extension/install-firefox-host.ps1 -Executable ./build/coddy-browser-host.exe
```

Enable Use the local Firefox input adapter in the extension and reconnect.
Firefox brings the target tab to the foreground. The adapter checks the foreground
application and tab title before sending input. It has no network listener.
Other operating systems currently support Firefox page input only.

Native input does not guarantee every website supports automation. Direct frame
selection is unavailable; use screenshots and native input for embedded editors.
The extension does not bypass login or CAPTCHA. A closed browser, extension restart
or lost connection may leave an action's outcome unknown. Coddy does not replay it
automatically; inspect the page before trying again.

## Verify a build

```bash
node external/browser-extension/build.mjs
node --test external/browser-extension/tests/*.test.mjs
go test ./internal/browser ./internal/tools/browser ./cmd/coddy-browser-host
```

The optional `TestExtensionBrowser` runs a real Chromium extension against the
browser server. Install Playwright and its Chromium browser, set `NODE_PATH` when
needed, then run `CODDY_BROWSER_E2E=1 go test ./internal/browser -run TestExtensionBrowser -v`.
