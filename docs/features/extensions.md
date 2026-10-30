# JavaScript extensions

The web UI can load operator-installed JavaScript extensions. Open **Extensions** in the navigation menu, or **Settings → Extensions**, to install a ZIP and switch extensions on or off.

An extension can add pages, panels and menu actions, process incoming turn data, observe lifecycle events and every stream frame, render tool calls and transcript rows, decorate permission prompts, paint with and override the app's theme colours, translate its own strings, add fields and sections to Settings and narrow the session list. This first version covers the browser UI. Native executables, agent tools, composer controls and marketplaces from the [extension issue](https://github.com/coddy-project/coddy-agent/issues/347) and [expanded specification](https://gist.github.com/hijera/95b016e1721608e8c3974fe3b80e29dc) are not implemented.

Start with [Your first JavaScript extension](../tutorials/first-extension.md) for a short walkthrough, or use the [example index](../../examples/extensions/README.md).

## Install and enable

![Extensions settings with an installed package](../assets/extensions/extensions-settings-dark-1280.png)

*Extensions settings with the Hello package enabled.*

![Extensions settings on the light theme](../assets/extensions/extensions-settings-light-1280.png)

*The same settings on the light theme.*

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

The optional top-level `permissions` lists the scopes the package declares: `ui.page`, `ui.alter`, `sessions.read`, `sessions.assets.write`, `sessions.title`, `sessions.prompt`, `workspace.switch`, `events.publish`, `ide.actions`, `kv`, `config.read` and `llm.complete`. The vocabulary is closed - at most 32 dotted lowercase names, and an unknown one is refused at install. Enablement stays the authority gate: the declaration is the vocabulary a later phase enforces, and nothing checks the code against it today.

The optional `ui.compat_styles` lists CSS files of the ZIP that load only on a legacy engine, layered over the ordinary styles. A modern engine never applies them; both are covered under the engine tier below.

An existing id cannot be overwritten. Remove the old package, install the new ZIP and approve its new digest. There is no automatic update or rollback in this version.

## JavaScript API

![A page opened by the Hello extension](../assets/extensions/extensions-page-dark-1280.png)

*An extension-owned page with HTML, CSS, a JavaScript counter and the theme swatches.*

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
| `coddy.host` | The reserved host channel: `host.api` is `1`, `await host.capabilities()` names the manifest permissions this build honours, and `await host.fireEvent({type, data})` publishes one host event - `extension_event` on the shared server stream for hook handlers and an IDE bridge to answer, and `ext.<id>.<type>` to this page's listeners. An event type matches `^[a-z][a-z0-9-]{0,63}$`, and `data` is one JSON value. |
| `coddy.assets.text(path)` | Fetch a UTF-8 asset from the exact enabled package through the authenticated API. |
| `coddy.net.fetch(url, options?)` | Send an HTTP(S) request from the Coddy server and receive a buffered `Response`. |
| `coddy.events.on(type, listener)` | Observe `tabChanged`, `stepEnded` or `turnEnded`. Returns a `remove()` handle. |
| `coddy.theme.get()` | The active theme: its id, `light`/`dark` mode and the resolved colours an extension paints with. A frozen snapshot. |
| `coddy.theme.onChange(listener)` | Observe every theme switch; the listener gets the new frozen snapshot. Returns a `remove()` handle. |
| `coddy.theme.getEffects()`, `coddy.theme.onEffectsChange(listener)` | Reduced motion and the engine tier. The listener hears changes only; its first read is the baseline. |
| `coddy.theme.override(colors)` | Override theme colour tokens with CSS colours until the handle removes them. Returns an `update`/`remove` handle. |
| `coddy.ui.registerToolRenderer({id, server?, tool?, mount})` | Replace the presentation of matching MCP and built-in tool calls; original details remain available. |
| `coddy.ui.registerMessageRenderer({id, match, mount})` | Draw transcript rows: matched host item types and `ext_frame` rows of frames the host has no row for. |
| `coddy.ui.registerPermissionPreview({id, tool?, mount})` | Add a view under the host's own preview on a permission card. The prompt stays the host's. |
| `coddy.settings.registerField(field)` | Add a scalar or component field to a Settings section - the host's or this extension's own. Returns an update/remove handle. |
| `coddy.settings.registerSection({id, title, weight?, mount?})` | Add a Settings section of this extension's own. Fields of the same extension may name it in `section`. |
| `coddy.settings.get(id)`, `coddy.settings.set(id, value)` | Read or save an extension-owned configuration value. Both are asynchronous; `set(id, null)` removes the stored value. |
| `coddy.ui.registerPage({id, title, mount})` | Add a page to the Extensions menu. `mount(element, api)` may return a cleanup function. |
| `coddy.ui.registerMenuItem({id, title, onSelect})` | Add a menu action. |
| `coddy.ui.registerPanel({id, title, placement, mount})`, `coddy.ui.openPanel(id)` | Add a surface of this extension's own - `"slide-over"`, `"full"` or `"drawer"` - and open it. One panel is open at a time. |
| `coddy.ui.anchor(name)`, `coddy.ui.onAnchor(name, fn)` | A named box of the host surface, or `null` while its surface is closed. `onAnchor` reports attach and detach and returns a `remove()` handle. |
| `coddy.ui.registerSessionFilter({id, title, filter?, mount?})` | Narrow the session list and draw a chip beside the host's filter controls. |
| `coddy.ui.engine()` | The engine tier of the shell: a frozen `{tier, chromiumMajor}`. |
| `coddy.ui.addCompatStyles(css)` | Layer a stylesheet over the host's, on a legacy engine only. Returns a `remove()` handle. |
| `handle.update(patch)`, `handle.remove()` | Update a registered page, action, panel or contribution, or remove it. Ids stay fixed and are namespaced by extension. A patch is validated like the registration and throws on a blank title or a missing callback. |
| `coddy.ui.openPage(id)` | Open a page owned by this extension in a dialog. |
| `coddy.stream.registerIncoming({id, transform})` | Process incoming turn payloads before the browser applies them. Returns a `remove()` handle. |
| `coddy.stream.onFrame({id, listener})` | Observe every frame of the live turn stream, including kinds the host does not parse. Read-only. Returns a `remove()` handle. |
| `coddy.i18n.locale()`, `coddy.i18n.t(key, vars?)` | The active host locale, and a lookup of this extension's own strings under `ext.<id>.`. |
| `coddy.i18n.registerMessages(locale, dict)`, `coddy.i18n.onChange(fn)` | Register one locale's strings and observe host locale changes. Both return a `remove()` handle. |
| `coddy.React` | The host's React instance for authors who need it. The page mount contract also works with plain DOM code. |

`activate` may return a cleanup function or a promise for one. The host gives activation 10 seconds. Failed activation removes its registrations, requests, styles and blob URL; the error appears in Settings. The host does not retry failed code on its own; the list refresh leaves a failed package alone. **Refresh** in **Settings → Extensions** asks again, and only for what failed before any code ran - a module whose code has already been evaluated still needs a page reload. Closing a page calls its mount cleanup. Disposal removes everything the extension holds: registrations, styles, compat styles and theme overrides. The first effects snapshot an extension reads is the baseline its `onEffectsChange` listeners are registered against; they hear a change, never the state the world was already in. Cleanup is cooperative and cannot revoke arbitrary same-origin JavaScript.

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

`server` and `tool` match exact names; omit either or use `"*"` to match all names in that position. Both `server__tool` and `mcp__server__tool` are recognised. A built-in call - a name with no `server__tool` spelling - matches renderers by its own name with an empty server, so a renderer can take a host tool over; in the snapshot its `server` is `""` and its `tool` the plain name. More-specific registrations win, then extension id in ASCII order, then registration order.

`mount(element, call, api)` receives this extension's SDK and a frozen call snapshot: `sessionId`, `toolCallId`, `name`, `server`, `tool`, `status`, `argsText`, `resultText`, `resultWasTruncated`, `permissionWaiting`, `durationMs` and `startedAtMs`. Times are numbers in milliseconds, or `null` when absent. `resultText` is the full result when loaded, otherwise the preview. A renderer does not automatically fetch a full result.

Return `{update(call), dispose()}` to keep the same DOM while input, status or result changes. `mount` and `update` must finish synchronously. A mount that returns only cleanup, or nothing, is mounted again for changed data. The host runs cleanup on replacement, transcript-window unmount or extension disposal. A thrown error removes the failing registration and restores the next matching renderer or the normal view. Errors appear in Settings.

The host keeps **Original tool details** below the custom view, including raw input/output and the ordinary attachments. Permission and question prompts stay under host control. A custom card changes presentation in this browser; it changes no tool arguments, results, permissions or stored history.

## Drawing transcript rows

`coddy.ui.registerMessageRenderer` draws transcript rows of two kinds: an item type the host already draws, which only a renderer naming that type takes over, and `ext_frame`, a live frame the host has no row for.

A frame whose SSE event name the host has no row for becomes one live transcript item of type `ext_frame`, but only when a registered message renderer matches it; a frame nothing matches is dropped. An empty `event` names a text frame and is never one of these. `ext_frame` rows are live only: the server keeps the original history, and a later history read replaces them.

```js
coddy.ui.registerMessageRenderer({
  id: "usage-rows",
  match: "ext_frame:usage",
  mount(element, item) {
    element.textContent = `usage: ${item.data}`;
    return () => element.replaceChildren();
  }
});
```

`match` is a string or an array of item type names, `ext_frame:<event>`, `ext_frame` or `*`. `*` covers `ext_frame` rows only: a host row is drawn by the host unless a renderer names its type. The selection prefers `ext_frame:<event>`, then `ext_frame`, then an exact item type, then `*`; a tie goes to the extension id in ascending ASCII order and then to registration order.

`mount(element, item, api)` receives this extension's SDK and a frozen item: `sessionId`, `itemId`, `type`, `event` - the SSE event name of an `ext_frame` row, empty otherwise - and `data`, the item as JSON text or the raw frame payload. It follows the mount contract of a tool renderer: return a cleanup function or `{update(item), dispose()}`, and finish synchronously. A thrown error removes the failing renderer and the row falls back: a host row returns to the host's own drawing, an `ext_frame` row leaves the transcript.

## Decorating the permission card

```js
coddy.ui.registerPermissionPreview({
  id: "bash-note",
  tool: "bash",
  mount(element, call) {
    element.textContent = `About to run: ${call.argsText}`;
    return () => element.replaceChildren();
  }
});
```

`coddy.ui.registerPermissionPreview` decorates the host's permission card: the view sits under the host's own preview of the pending call. The prompt, its options and the choice stay the host's - a preview is presentation in this browser and changes no permission.

`tool` matches the call name, `"*"`, or is omitted for every prompt. A named tool beats `"*"`, which beats an unqualified preview; a tie goes to the extension id in ascending ASCII order and then to registration order.

`mount(element, call, api)` receives the frozen `ExtensionToolCall` snapshot of the pending call - `status` is `"pending"` and `permissionWaiting` is `true` - and follows the mount contract of a tool renderer. A thrown error removes the registration and the card falls back to the host's own view.

## Theme colours

![The Hello extension page on the light theme](../assets/extensions/extensions-page-light-1280.png)

*The same page on the light theme: its swatches are painted from `coddy.theme`, its button and labels from the theme's CSS variables.*

An extension's styles are injected into the app's document, so its CSS variables are the theme's colours whatever theme is active. Paint with these variables and every one of the seven themes follows.

| Variable | The colour of |
| --- | --- |
| `--bg` | The app ground and the page of an extension window. |
| `--text` | Primary text. |
| `--muted` | Secondary text and captions. |
| `--accent` | The accent: a selected control, a primary button. |
| `--coddy-danger` | Destructive actions and failures. |
| `--coddy-link` | Links in prose. |
| `--coddy-glass-panel-border` | The border of a card or panel. |
| `--coddy-glass-panel-bg` | The frosted ground of a card or panel. |

Drawing outside CSS - a canvas, a colour computed in code - reads the same palette through `coddy.theme`.

```js
const paint = (theme) => {
  label.textContent = `Theme: ${theme.id}`;
  context.fillStyle = theme.colors.accent;
};
paint(coddy.theme.get());
const watched = coddy.theme.onChange(paint);
```

`coddy.theme.get()` returns a frozen snapshot. `id` names the theme (`dark`, `light`, `midnight`, `solarized-dark`, `monokai`, `nord` or `rose-pine`), `mode` is `light` or `dark`, and `colors` maps `bg`, `text`, `muted`, `accent`, `danger`, `link`, `border` and `surface` to computed colours (`rgb()` or `rgba()`, and `color(srgb ...)` for a token written with `color-mix()`) that a canvas and an element style can both use. `surface` is the frosted ground of `--coddy-glass-panel-bg`, `border` its border.

`coddy.theme.onChange(listener)` hands the new frozen snapshot on every theme switch and returns a `remove()` handle. Delivery runs by extension id in ascending ASCII order, then registration order. The host consumes rejected promises, removes a failing listener and reports its error in Settings, as it does for the lifecycle events. The lifetime signal removes listeners, and `get` and `onChange` stop with the lifetime the way registrations do. The [Hello example](../../examples/extensions/hello/main.js) paints its swatches this way.

`coddy.theme.getEffects()` returns a frozen `{reducedMotion, engine}` snapshot. `reducedMotion` is on when `prefers-reduced-motion` asks for it or the host root carries `data-effects="reduced"`; `engine` is the engine tier. `coddy.theme.onEffectsChange(listener)` returns a `remove()` handle and hands over the new snapshot only when it changes - the first read is the baseline listeners are registered against.

`coddy.theme.override(colors)` overrides the theme tokens live and returns a handle with `update(colors)` and `remove()`. `colors` is a partial map of the eight colour names - `bg`, `text`, `muted`, `accent`, `danger`, `link`, `border` and `surface` - to CSS colour strings; a value carrying `;`, `{`, `}` or `url(` is refused. The override holds until the handle removes it, disposal removes it too, and a later registration of another extension wins the shared tokens.

The [Theme Lab example](../../examples/extensions/theme-lab/main.js) opens a slide-over panel of lightness sliders that repaint the app in real time; its manifest declares `"permissions": ["ui.page"]`. From `examples/extensions/theme-lab`, package it with the following command.

```sh
python -m zipfile -c theme-lab.zip manifest.json main.js style.css
```

## Engine tier and compatibility styles

The host tags the page root with `data-coddy-engine="modern|legacy"` at boot, before the first paint decision. A legacy engine is Chromium 110 or below: `:has()` needs 105, `color-mix()` needs 111, and the JCEF panels of IntelliJ 2022 ship Chromium 98-104. An engine that reports no Chromium version - Safari or Firefox - is kept modern. `coddy.ui.engine()` returns the frozen `{tier, chromiumMajor}` the detection found, `chromiumMajor` null when the engine names none.

`coddy.ui.addCompatStyles(css)` layers a stylesheet over the host's and returns a `remove()` handle. It applies on a legacy engine only; on a modern one nothing is injected and the cleanup belongs to the handle all the same. Disposal removes these stylesheets with the rest of the extension. The `ui.compat_styles` files of the manifest load the same way at activation: the correction a Chromium 110 shell needs, never applied where the ordinary stylesheet already draws what it would correct.

## Panels and anchor points

```js
const panel = coddy.ui.registerPanel({
  id: "theme-lab",
  title: "Theme Lab",
  placement: "slide-over",
  mount(element, api) {
    element.textContent = "Sliders go here";
    return () => element.replaceChildren();
  }
});
coddy.ui.registerMenuItem({
  id: "open-lab", title: "Theme Lab",
  onSelect: () => coddy.ui.openPanel("theme-lab")
});
```

`coddy.ui.registerPanel` gives an extension a surface of its own, and `coddy.ui.openPanel(id)` opens it. `placement` is `"slide-over"`, `"full"` or `"drawer"`; one panel is open at a time. Escape and the close button run the mount cleanup. The handle updates and removes the panel the way a page's does.

An anchor is a named box of the host surface: `composer.actions`, `chat.header`, `nav.rail`, `statusline`, `sessions.toolbar` or `settings.footer`. `coddy.ui.anchor(name)` returns the box, or `null` while its surface is closed - the host mounts a box only while its surface is open. `coddy.ui.onAnchor(name, fn)` returns a `remove()` handle and calls `fn(boxOrNull)` on attach and detach, immediately with the box when it is already there.

## Strings of an extension

```js
coddy.i18n.registerMessages("en", { greeting: "Hello, {name}" });
coddy.i18n.registerMessages("ru", { greeting: "Здравствуйте, {name}" });
label.textContent = coddy.i18n.t("greeting", { name: title });
```

`coddy.i18n` holds the extension's own strings. `locale()` is the active host locale. `t(key, vars?)` looks up `ext.<id>.<key>` - keys are forced under that prefix - falling back from the active locale to `en` and then to the key itself, and fills `{name}` placeholders from `vars`. `registerMessages(locale, dict)` returns a `remove()` handle; re-registering a locale replaces its dictionary. `onChange(fn)` returns a `remove()` handle and fires when the host locale changes.

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

The field id uses a lowercase letter followed by lowercase letters, digits or hyphens, at most 64 characters. `section` is an existing section id - the host's, such as `agent`, `mcp_servers`, `tools`, `appearance` or `extensions`, or one this extension registered (below). The title is required; `description` and `default` are optional. Types are `string`, `number` and `boolean`; a string field may declare an `enum` of choices. Defaults must match the type and choices.

Registered fields appear below the existing section controls, grouped under the extension id. They edit the same configuration draft as the normal fields and use its Save, Reload and unsaved-change handling. A default is displayed without writing it to disk. Removing a field or disabling its extension removes the control and keeps the saved value.

An extension can own a section of its own: `coddy.settings.registerSection({id, title, weight?, mount?})` returns an update/remove handle. The id follows the field-id pattern and the title is required. The section renders after the host's sections, and `weight` orders it among the extension sections. `mount(element, api)` draws free content above the section's fields. A field of the same extension may name the section in `section`; a field of another extension may not.

A field may draw itself: `type: "component"` takes `mount(element, api, state)` instead of a scalar type, where `state` is a frozen `{value, setValue}` of the ordinary scalar value. A component field edits the same configuration draft and persists like any other field - its values are ordinary scalars in `extension_values` - and `default` is optional.

Values live in the server's `config.yaml`, under `extension_values.<extension-id>.<field-id>`. They are ordinary readable configuration, not write-only secrets. Each extension may store at most 128 scalar fields; all extension values together are limited to 256 KiB. SDK-written strings preserve literal dollar signs. Existing YAML environment references follow the normal loader rules.

`get` reads the saved value, or the registered default when none is stored; unsaved form edits are not returned. `set` uses the normal authenticated config transaction and revision merge, preserves neighbouring values, then refreshes the Settings cache. It accepts scalar variables even without a registered control. Calls stop with the extension lifetime. Saving reloads configuration as an ordinary Settings save does. Passing `null` removes a key, so a registered default applies again.

The [Workflow tools example](../../examples/extensions/workflow-tools/main.js) combines events, a `demo__status` card and fields in ReAct loop and MCP servers settings. From `examples/extensions/workflow-tools`, package it with the following command.

```sh
python -m zipfile -c workflow-tools.zip manifest.json main.js
```

## Narrowing the session list

```js
coddy.ui.registerSessionFilter({
  id: "hide-archived",
  title: "Active only",
  filter: (row) => !row.archived
});
```

`coddy.ui.registerSessionFilter` narrows the History listing after the host's own filters and returns an update/remove handle. `filter(row)` receives the frozen row metadata - `id`, `title`, `cwd`, `repoRoot`, `archived`, `pinned`, `origin`, `tags` and `updatedAt` - and returns `false` to hide the row. Every registered filter must pass; a filter that throws loses its own say and the row stays.

`title` names the chip the filter shows, and an optional `mount(element, api)` draws that chip beside the host's filter controls, following the mount contract of a tool renderer. A filter narrows the rows already listed; it reloads nothing.

The host narrows first: the listing opens on the project of the open chat ([Web UI](../surfaces/web-ui.md#session-list)), and extension filters run on what is left.

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

`coddy.stream.onFrame({id, listener})` observes the same stream at frame granularity and returns a `remove()` handle. Every frame of the live turn stream reaches the listener - including event kinds the host does not parse - as a frozen `{sessionId, transport, event, id, ageMs?, data}` copy, in registration order. Observation is read-only: a listener cannot rewrite or stop a frame. A listener that throws is removed and reported like a failing processor. This is how an extension sees an event kind that shipped after this SDK; a frame the host has no row for can also become a live `ext_frame` transcript row, which a message renderer draws.

Processing affects the live stream in this browser. The server keeps the original history and LLM context. A later history read can replace the transformed display with that original data. This API does not intercept outgoing requests, REST history, authentication, assets or the shared `/coddy/events` stream.

The [Stream tools example](../../examples/extensions/stream-tools/main.js) uppercases incoming assistant text and shows the last received frame on its own page.

![Processing incoming turn data in an extension](../assets/extensions/extensions-stream-dark-1280.png)

*Stream tools shows the incoming payload while the chat displays the transformed text.*

From `examples/extensions/stream-tools`, package it with:

```sh
python -m zipfile -c stream-tools.zip manifest.json main.js
```

## Known limitations

- `ext_frame` rows are live only. They are never persisted, and a later history read replaces them with the original data.
- The host channel answers `host.capabilities()` with the manifest permissions this build honours, and today that is `ui.page` alone. The sessions and messages host API lands on the same object later.
- A declared `permissions` list is vocabulary, not enforcement: every scope, `ui.alter` included, is declared but not yet checked, and enablement stays the authority gate. `ide.actions` is reserved for an IDE action bridge that no build honours yet.
- One panel is open at a time, whatever extension owns it.
- An anchor box is an empty div the host keeps at a named place, and the host mounts it only while its surface is open: `ui.anchor(name)` is `null` otherwise, and `ui.onAnchor` is how an extension waits for one.
