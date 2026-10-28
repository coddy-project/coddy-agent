# JavaScript extensions

The web UI can load operator-installed JavaScript extensions. Open **Extensions** in the navigation menu, or **Settings → Extensions**, to install a ZIP and switch extensions on or off.

An extension can add pages and menu actions, update or remove its own entries, and process incoming turn data before the browser uses it. This first version covers the browser UI. Native executables, agent tools, composer controls, settings alterations and marketplaces from the [extension issue](https://github.com/coddy-project/coddy-agent/issues/347) and [expanded specification](https://gist.github.com/hijera/95b016e1721608e8c3974fe3b80e29dc) are not implemented.

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

## Incoming stream data

![Extension loading and incoming data processing](../assets/diagrams/ui-extensions.png)

*Package approval, browser activation and the incoming payload chain.*

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
