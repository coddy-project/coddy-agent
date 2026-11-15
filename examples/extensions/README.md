# Create a JavaScript extension

Start with [first-extension](first-extension/). It contains the two files an extension needs: a root `manifest.json` and one `main.js` ES module exporting `activate(coddy)`. No Node.js or build step is required.

1. Copy `first-extension` to your own folder. Change the manifest's `id`, `name` and `version`. Keep `api: 1` and `ui.entry: "main.js"`. Declare the API your package needs in `required_features`, and optionally a Coddy range in `engines.coddy`. The host checks both before importing your code.
2. Edit `main.js`. `coddy.ui.registerPage` adds a page to the Extensions menu. Its `mount` callback creates DOM elements and returns a function that removes them when the page closes. `activate` returns a cleanup function for the registration.
3. From inside your folder, create the archive.

   ```sh
   python -m zipfile -c first-extension.zip manifest.json main.js
   ```

4. In the running web UI, open **Settings → Extensions**, install the ZIP, inspect its digest and accept the risk to enable it. The page reloads. Open **Extensions → My first extension** and click the counter.
5. To update the code, remove the installed package, rebuild the ZIP, install it and approve its new digest. Disabling it reloads the page.

The files must sit at the ZIP root. Bundle dependencies into `main.js`; relative imports cannot resolve from its blob URL. Enable only code you trust: extensions run in the app origin and can call services from the Coddy server.

The [manifest schema](manifest.schema.json) documents compatibility declarations. Public TypeScript types live in `external/ui/src/ui/extensions/sdk.d.ts`, and `coddy.host.info()` reports the host's SDK version, Coddy version and features.

| Example                                                | What to copy                                                                                                                                                                                                                                       |
| ------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [first-extension](first-extension/main.js)             | Minimal manifest, page, DOM event and cleanup.                                                                                                                                                                                                     |
| [hello](hello/main.js)                                 | Separate HTML/CSS assets, a counter and the theme colours.                                                                                                                                                                                         |
| [local-api](local-api/main.js)                         | `coddy.net.fetch` for GET/POST, binary-capable transport and cancellation. `localhost` means the Coddy server.                                                                                                                                     |
| [stream-tools](stream-tools/main.js) | A semantic text-delta processor and a page showing the last delta; raw SSE stays an explicit escape hatch. |
| [workflow-tools](workflow-tools/main.js) | Navigation/step/turn events, a custom `demo__status` MCP card, and fields in ReAct loop and MCP servers settings. |
| [theme-lab](theme-lab/main.js) | Theme Lab repaints the app through `coddy.theme.parseColor` and `formatColor`: hue, saturation and lightness sliders shift every colour token, and Reset restores the theme. |
| [ocean-theme](ocean-theme/manifest.json) | A manifest-only `themes[]` entry adds an Ocean swatch and loads `ocean.css` when selected; removing the package restores the built-in dark theme. |
| [ok-coddy](ok-coddy/main.js) | The OK, coddy mini-chat drawer: a rubber duck with twelve tricks, canned replies, a ticket toggle and the `tricks`/`footer` hooks other extensions fill. |
| [ok-coddy-addon](ok-coddy-addon/main.js) | Targeted placement after `host/model`, `renameRow` and `hooks.invoke`, without whole-screen alters or DOM access to the panel. |
| [neuraldeep-onboarding](neuraldeep-onboarding/main.js) | An onboarding dialog that writes the provider, optional proxy and `neuraldeep/qwen3.6-35b-a3b` model through one `coddy.providers.upsert` call by name. |
| [neuraldeep-stability](neuraldeep-stability/main.js)   | A declarative Settings block that appears only in the `neuraldeep` provider row and renders `status.neuraldeep.ru/api/v2/uptime.json` through the server-side `coddy.net.fetch` proxy: current status, per-component uptime and daily history.     |

For a local API, replace ordinary browser fetch with the SDK helper.

```js
const response = await coddy.net.fetch("http://127.0.0.1:8080/api/status");
if (!response.ok) throw new Error(`HTTP ${response.status}`);
const status = await response.json();
```

Use this in an asynchronous UI handler or activation, not inside `transform`. Refresh a cache asynchronously if a stream processor needs network data. Requests are buffered, limited to 30 seconds, 4 MiB upload and 8 MiB download. Target authentication must be supplied explicitly; redirects are returned without following them.

The [short tutorial](../../docs/tutorials/first-extension.md) walks through the files. The [extension API reference](../../docs/features/extensions.md) covers assets, menu actions, theme colours, incoming data and the network contract.

To try the contribution APIs, package `workflow-tools/manifest.json` and `workflow-tools/main.js` from inside that folder with `python -m zipfile -c workflow-tools.zip manifest.json main.js`. After enabling it, edit **MCP card badge** in **Settings → ReAct loop** and use Save. Open **Extensions → Workflow events** and choose **Read saved badge**. A call of the MCP server `demo`, tool `status`, uses its custom card; its original details remain available underneath. See the API guide for matching other MCP tools.
