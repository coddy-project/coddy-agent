# JavaScript extensions

The web UI can load operator-installed JavaScript extensions. Open **Extensions** in the navigation menu, or **Settings → Extensions**, to install a ZIP and switch extensions on or off.

An extension can add pages and menu actions, process incoming turn data, observe lifecycle events, render MCP calls and add fields to existing Settings sections. This first version covers the browser UI. Native executables, agent tools, composer controls and marketplaces from the [extension issue](https://github.com/coddy-project/coddy-agent/issues/347) and [expanded specification](https://gist.github.com/hijera/95b016e1721608e8c3974fe3b80e29dc) are not implemented.

Start with [Your first JavaScript extension](../tutorials/first-extension.md) for a short walkthrough, or use the [example index](../../examples/extensions/README.md).

## Install and enable

![Extensions settings with an installed package](../assets/extensions/extensions-settings-dark-1280.png)

*Extensions settings with the Hello package enabled.*

1. Choose **Install ZIP** in **Settings → Extensions**. Installation checks the archive and stores it under `<home>/extensions/`. It leaves the extension disabled and executes no code.
2. Check the name, version and SHA-256. Read the source before enabling a package from someone else.
3. Choose **Enable**, then **Accept risk and enable**. The approval names the exact installed package digest. The page reloads and loads the extension.
4. Open the **Extensions** menu to use its pages and actions. **Manage extensions…** returns to the settings section.

**Enable extensions at your own risk.** Their JavaScript executes in the web app's origin, with access to chats, browser credentials and server APIs. It is not sandboxed. The warning describes the authority the code actually has; it does not turn the SDK into a permission boundary.

![Extension activation warning](../assets/extensions/extensions-approval-dark-1280.png)

*Enabling a package requires accepting the risk of running its code.*

Disabling or removing an extension reloads this page to unload executed code. Other open browsers check the list every 15 seconds and when focused; they reload after detecting revocation. Browser throttling can delay that check. Reload every affected browser yourself to revoke access immediately. A remote-environment switch also reloads the page once any extension code has been evaluated.

The packages and switches belong to the server, including when accessed through a remote environment or swarm relay. Assets use the same authenticated API as the rest of the application. JavaScript loading requires HTTPS or localhost, where the browser can verify SHA-256 with Web Crypto.

## Package format

A package is one ZIP, up to 8 MiB compressed, 16 MiB expanded and 128 entries. Put `manifest.json` at the root:

```json
{
  "api": 1,
  "id": "hello",
  "name": "Hello extension",
  "version": "1.0.0",
  "description": "A counter page",
  "ui": { "entry": "main.js", "styles": ["style.css"] }
}
```

The id uses lowercase ASCII letters, digits and hyphens, starts with a letter and has at most 32 characters. File names use ASCII letters, digits, underscores, hyphens, dots and `/` separators. Absolute paths, parent traversal, links and duplicate names ignoring case are refused. Archives are read in memory and never extracted into the workspace.

`ui.entry` names one prebuilt ES module exporting `activate(coddy)`. Bundle dependencies into that file: relative imports have no base inside its blob URL. HTML and CSS may be embedded as strings in the module, or shipped as files in the ZIP. `ui.styles` lists CSS files loaded with the extension; use an extension-specific class prefix to avoid changing unrelated UI.

An existing id cannot be overwritten. Remove the old package, install the new ZIP and approve its new digest. There is no automatic update or rollback in this version.

## JavaScript API

![A page opened by the Hello extension](../assets/extensions/extensions-page-dark-1280.png)

*An extension-owned page with HTML, CSS and a JavaScript counter.*

```js
export function activate(coddy) {
  const page = coddy.ui.registerPage({
    id: "hello",
    title: "Hello",
    mount(element, api) {
      element.textContent = "Hello from an extension";
      return () => element.replaceChildren();
    }
  });
  const action = coddy.ui.registerMenuItem({
    id: "open",
    title: "Open Hello",
    onSelect: () => coddy.ui.openPage("hello")
  });
  action.update({ title: "Show Hello" });
  return () => { action.remove(); page.remove(); };
}
```

| API | Behavior |
| --- | --- |
| `coddy.api`, `coddy.id` | SDK version `1` and the owning extension id. |
| `coddy.signal` | Aborted when the extension's lifetime ends. Pass it to listeners and requests. |
| `coddy.assets.text(path)` | Fetch a UTF-8 asset from the exact enabled package through the authenticated API. |
| `coddy.net.fetch(url, options?)` | Send an HTTP(S) request from the Coddy server and receive a buffered `Response`. |
| `coddy.events.on(type, listener)` | Observe `tabChanged`, `stepEnded` or `turnEnded`. Returns a `remove()` handle. |
| `coddy.ui.registerToolRenderer({id, server?, tool?, mount})` | Replace the presentation of matching MCP calls; original details remain available. |
| `coddy.settings.registerField(field)` | Add a scalar field to an existing Settings section. Returns an update/remove handle. |
| `coddy.settings.get(id)`, `coddy.settings.set(id, value)` | Read or save an extension-owned configuration value. Both are asynchronous; `set(id, null)` removes the stored value. |
| `coddy.ui.registerPage({id, title, mount})` | Add a page to the Extensions menu. `mount(element, api)` may return a cleanup function. |
| `coddy.ui.registerMenuItem({id, title, onSelect})` | Add a menu action. |
| `handle.update(patch)`, `handle.remove()` | Update a registered page or action, or remove it. Ids stay fixed and are namespaced by extension. |
| `coddy.ui.openPage(id)` | Open a page owned by this extension in a dialog. |
| `coddy.stream.registerIncoming({id, transform})` | Process incoming turn payloads before the browser applies them. Returns a `remove()` handle. |
| `coddy.React` | The host's React instance for authors who need it. The page mount contract also works with plain DOM code. |

`activate` may return a cleanup function or a promise for one. The host gives activation 10 seconds. Failed activation removes its registrations, requests, styles and blob URL; the error appears in Settings. The host does not retry failed code until a page reload. Closing a page calls its mount cleanup. Cleanup is cooperative and cannot revoke arbitrary same-origin JavaScript.

The [Hello example](../../examples/extensions/hello/manifest.json) includes a module, CSS and HTML. From `examples/extensions/hello`, run:

```sh
python -m zipfile -c hello.zip manifest.json main.js style.css panel.html
```

Install the resulting ZIP through Settings. No Node.js runtime is needed on the server or in the browser.

## Navigation and completion events

![Workflow events page with completion counters](../assets/extensions/extensions-events-dark-1280.png)

*The example counts navigation changes, completed model steps and completed turns.*

```js
coddy.events.on("tabChanged", ({ from, to, sessionId }) => {
  console.log("Opened", to, "from", from, "chat", sessionId);
});
coddy.events.on("stepEnded", ({ sessionId, usage }) => {
  console.log("Model call finished", sessionId, usage.totalTokens);
});
coddy.events.on("turnEnded", async ({ sessionId, at }) => {
  console.log("Agent turn finished", sessionId, at);
});
```

| Event | Payload and source |
| --- | --- |
| `tabChanged` | `{from, to, sessionId}` for an app hash-route change: chats, Settings sections, Files, tasks and other sections. `from` and `to` are hashes; `sessionId` is the destination chat or `null`. The initial route, browser focus and switching to another browser tab do not emit it. |
| `stepEnded` | `{sessionId, sequence, transport, turnStartedAt, usage}` when a completed model call's `token_usage` frame reaches this browser. This is the end of the model response, before any tools it requested finish. `sequence` is the frame id or count, `transport` is `post` or `relay`, `turnStartedAt` is the server timestamp when known or `null`, and `usage` holds the original model-call counters. |
| `turnEnded` | `{sessionId, at}` from the app's existing shared `/coddy/events` connection when the whole turn ends, including cancellation or failure. It also covers turns started by another surface or in another chat. `at` is the server's timestamp. |

These callbacks observe events; their return values do not cancel navigation or change the agent's control flow. They may start asynchronous work. The host consumes rejected promises, removes a failing listener and reports its error in Settings. Other listeners continue. Delivery runs by extension id in ascending ASCII order, then registration order. Each payload is a frozen copy.

Only events received while the listener is registered are delivered. Disconnecting or reaching EOF does not itself announce a completed turn. Step events follow streams this browser consumes; historical REST reads produce none. The host suppresses duplicate frame identities and turn-end timestamps within a bounded in-memory window. A reload resets it. Delivery order between the turn stream and shared server events is not guaranteed. Stream transforms do not rewrite the lifecycle payloads.

Remove a subscription with its `remove()` handle. The extension's lifetime signal removes its listeners too.

## Custom MCP cards

![Custom MCP card with original details below](../assets/extensions/extensions-mcp-card-dark-1280.png)

*The extension renders the demo status result and keeps the original call available.*

```js
coddy.ui.registerToolRenderer({
  id: "status-card", server: "demo", tool: "status",
  mount(element, call) {
    const title = document.createElement("strong");
    element.append(title);
    const update = (next) => {
      title.textContent = `${next.server}: ${next.status} - ${next.resultText}`;
    };
    update(call);
    return { update, dispose: () => title.remove() };
  }
});
```

`server` and `tool` match exact MCP names; omit either or use `"*"` to match all names in that position. Both `server__tool` and `mcp__server__tool` are recognised. Built-in tool calls keep their normal renderer. More-specific registrations win, then extension id in ASCII order, then registration order.

`mount(element, call, api)` receives this extension's SDK and a frozen call snapshot: `sessionId`, `toolCallId`, `name`, `server`, `tool`, `status`, `argsText`, `resultText`, `resultWasTruncated`, `permissionWaiting`, `durationMs` and `startedAtMs`. Times are numbers in milliseconds, or `null` when absent. `resultText` is the full result when loaded, otherwise the preview. A renderer does not automatically fetch a full result.

Return `{update(call), dispose()}` to keep the same DOM while input, status or result changes. `mount` and `update` must finish synchronously. A mount that returns only cleanup, or nothing, is mounted again for changed data. The host runs cleanup on replacement, transcript-window unmount or extension disposal. A thrown error removes the failing registration and restores the next matching renderer or the normal view. Errors appear in Settings.

The host keeps **Original tool details** below the custom view, including raw input/output and the ordinary attachments. Permission and question prompts stay under host control. A custom card changes presentation in this browser; it changes no tool arguments, results, permissions or stored history.

## Fields in existing Settings sections

![Extension field inside ReAct loop settings](../assets/extensions/extensions-field-dark-1280.png)

*The example adds MCP card badge to the existing ReAct loop section.*

```js
coddy.settings.registerField({
  id: "badge", section: "agent", title: "MCP card badge",
  type: "string", default: "CUSTOM MCP"
});
const badge = await coddy.settings.get("badge");
await coddy.settings.set("badge", "MY CARD");
```

The field id uses a lowercase letter followed by lowercase letters, digits or hyphens, at most 64 characters. `section` is an existing section id, such as `agent`, `mcp_servers`, `tools`, `appearance` or `extensions`. The title is required; `description` and `default` are optional. Types are `string`, `number` and `boolean`; a string field may declare an `enum` of choices. Defaults must match the type and choices.

Registered fields appear below the existing section controls, grouped under the extension id. They edit the same configuration draft as the normal fields and use its Save, Reload and unsaved-change handling. A default is displayed without writing it to disk. Removing a field or disabling its extension removes the control and keeps the saved value.

Values live in the server's `config.yaml`, under `extension_values.<extension-id>.<field-id>`. They are ordinary readable configuration, not write-only secrets. Each extension may store at most 128 scalar fields; all extension values together are limited to 256 KiB. SDK-written strings preserve literal dollar signs. Existing YAML environment references follow the normal loader rules.

`get` reads the saved value, or the registered default when none is stored; unsaved form edits are not returned. `set` uses the normal authenticated config transaction and revision merge, preserves neighbouring values, then refreshes the Settings cache. It accepts scalar variables even without a registered control. Calls stop with the extension lifetime. Saving reloads configuration as an ordinary Settings save does. Passing `null` removes a key, so a registered default applies again.

The [Workflow tools example](../../examples/extensions/workflow-tools/main.js) combines events, a `demo__status` card and fields in ReAct loop and MCP servers settings. From `examples/extensions/workflow-tools`, package it with the following command.

```sh
python -m zipfile -c workflow-tools.zip manifest.json main.js
```

## Server-side HTTP requests

Use `coddy.net.fetch` to call a local or external API through the Coddy server. The browser sends an authenticated request to Coddy; Go makes the target request and returns its status, headers and bytes inside JSON. The target needs no CORS headers. Target HTML and `Set-Cookie` headers never become an active response of the web app.

```js
const response = await coddy.net.fetch("http://127.0.0.1:8080/api/data", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    Authorization: "Bearer target-service-key"
  },
  body: JSON.stringify({ query: "hello" }),
  signal: coddy.signal
});
if (!response.ok) throw new Error(`Target returned HTTP ${response.status}`);
const data = await response.json();
```

`localhost` and private network addresses refer to the machine running the selected Coddy server. With a remote environment or swarm relay, the request runs on that node. A normal browser `fetch` still runs from the browser's machine and follows browser network rules.

`options` accepts `method`, `headers`, `body` and `signal`. Supported methods are `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE` and `OPTIONS`; the default is `GET`. Bodies may be strings, `Blob`, `ArrayBuffer`, `Uint8Array` or `URLSearchParams`. `GET` and `HEAD` have no body. Read the buffered result with `text()`, `json()`, `arrayBuffer()` or `blob()`, or inspect `status`, `ok` and `headers`. Target HTTP errors resolve to a response; proxy failures reject the promise. The reconstructed response has an empty `url`; it does not expose a network stream.

Each call requires the exact enabled package digest. Disabling or removing a package through this server cancels its in-flight proxy requests. The SDK also cancels them when its lifetime or the caller's signal ends. Another process sharing the extension store is checked before dispatch and after reading the response. Calls have a 30-second timeout, at most 4 MiB of request bytes and 8 MiB of response bytes. The JSON request envelope is limited to 6 MiB, request headers to 32 KiB and response headers to 64 KiB. Cancellation cannot undo effects the target already received.

Only absolute `http:` and `https:` URLs without embedded credentials or fragments are accepted. Loopback, private and external targets are allowed, with normal TLS certificate validation. Redirects return their `3xx` response and `Location` header without being followed. The proxy uses no cookie jar, environment proxy or provider proxy. It forwards only headers the extension supplies, including an explicit target `Authorization`. Coddy's outer bearer token and browser cookies stay on the Coddy request. Caller-supplied cookies, routing headers, hop-by-hop headers, `Origin`, `Referer` and `Sec-*` headers are refused. Target cookies and hop-by-hop headers are omitted from the result.

Enabling an extension grants access to services reachable from the server, including its loopback and private network. The risk warning in Settings names that access. This SDK remains part of the approved code's authority; it does not isolate extensions from each other.

Network calls are asynchronous. Use them in activation or UI callbacks, or to refresh a cache that a synchronous incoming-stream processor reads. Do not return a network promise from `transform`.

![Calling a local API from an extension](../assets/extensions/extensions-network-dark-1280.png)

*Calling a local API through the Coddy server.*

The [Local API example](../../examples/extensions/local-api/main.js) provides a URL field, GET/POST requests and cancellation. From `examples/extensions/local-api`, package it with the following command.

```sh
python -m zipfile -c local-api.zip manifest.json main.js
```

## Incoming stream data

![Extension loading and incoming data processing](../assets/diagrams/ui-extensions.png)

*Package approval, browser activation, the incoming payload chain and server-side HTTP requests.*

Register a processor to inspect, rewrite or enrich incoming SSE payloads. The chain runs for fresh responses and for the composer relay when you follow or resume a turn. It also processes the last payload if the connection ends without a blank line.

Before reading a turn stream, the browser waits for its initial extension load for up to 11 seconds. If loading fails or exceeds that bound, the reader continues with the processors registered so far. Frames already consumed are not replayed to extensions that activate later.

```js
export function activate(coddy) {
  const processor = coddy.stream.registerIncoming({
    id: "annotate",
    transform(data, context) {
      if (context.event !== "tool_call_update") return;
      const payload = JSON.parse(data);
      const result = payload.content?.[0]?.content;
      if (typeof result?.text === "string") {
        result.text += "\nProcessed by my extension";
      }
      return JSON.stringify(payload);
    }
  });
  return () => processor.remove();
}
```

`transform(data, context)` receives the SSE data string and a frozen context:

| Field | Meaning |
| --- | --- |
| `sessionId` | The chat whose turn this reader follows. |
| `transport` | `"post"` for a fresh response; `"relay"` for a followed or resumed turn. |
| `event` | The SSE event name, such as `tool_call_update`; an empty string for ordinary response deltas. |
| `id` | The relay event id, or an empty string when the server sends none. |
| `ageMs` | The frame's replay age, when the relay provides it. |

Return a string to replace the payload, or `undefined` to observe it without replacing it. For JSON input, return valid JSON with the schema the event expects. You can change text, add fields or derive data for your own page. Keep the ids and versions the host needs to match tool calls, prompts and snapshots. Returning an empty string does not cancel an event.

The host orders processors by extension id in ascending ASCII order, then by registration order within that extension. Each processor receives the previous one's output. A thrown error, a promise or an invalid result removes the failing processor and shows its error in **Settings → Extensions**. The next processor receives the last valid payload. The host consumes a rejected promise but does not await it. Use synchronous callbacks that finish promptly; slow code blocks the browser.

The processor's `remove()` handle and `coddy.signal` end its registration. Frames without data, `[DONE]` and `desync` bypass the chain. A processor cannot replace event names, ids or replay ages, or generate `[DONE]`.

Processing affects the live stream in this browser. The server keeps the original history and LLM context. A later history read can replace the transformed display with that original data. This API does not intercept outgoing requests, REST history, authentication, assets or the shared `/coddy/events` stream.

The [Stream tools example](../../examples/extensions/stream-tools/main.js) uppercases incoming assistant text and shows the last received frame on its own page.

![Processing incoming turn data in an extension](../assets/extensions/extensions-stream-dark-1280.png)

*Stream tools shows the incoming payload while the chat displays the transformed text.*

From `examples/extensions/stream-tools`, package it with:

```sh
python -m zipfile -c stream-tools.zip manifest.json main.js
```
