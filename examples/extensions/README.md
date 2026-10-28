# Create a JavaScript extension

Start with [first-extension](first-extension/). It contains the two files an extension needs: a root `manifest.json` and one `main.js` ES module exporting `activate(coddy)`. No Node.js or build step is required.

1. Copy `first-extension` to your own folder. Change the manifest's `id`, `name` and `version`. Keep `api: 1` and `ui.entry: "main.js"`.
2. Edit `main.js`. `coddy.ui.registerPage` adds a page to the Extensions menu. Its `mount` callback creates DOM elements and returns a function that removes them when the page closes. `activate` returns a cleanup function for the registration.
3. From inside your folder, create the archive.

   ```sh
   python -m zipfile -c first-extension.zip manifest.json main.js
   ```

4. In the running web UI, open **Settings → Extensions**, install the ZIP, inspect its digest and accept the risk to enable it. The page reloads. Open **Extensions → My first extension** and click the counter.
5. To update the code, remove the installed package, rebuild the ZIP, install it and approve its new digest. Disabling it reloads the page.

The files must sit at the ZIP root. Bundle dependencies into `main.js`; relative imports cannot resolve from its blob URL. Enable only code you trust: extensions run in the app origin and can call services from the Coddy server.

| Example | What to copy |
| --- | --- |
| [first-extension](first-extension/main.js) | Minimal manifest, page, DOM event and cleanup. |
| [hello](hello/main.js) | Separate HTML/CSS assets and a counter. |
| [local-api](local-api/main.js) | `coddy.net.fetch` for GET/POST, binary-capable transport and cancellation. `localhost` means the Coddy server. |
| [stream-tools](stream-tools/main.js) | A synchronous incoming-stream processor and a page showing the last frame. |

For a local API, replace ordinary browser fetch with the SDK helper.

```js
const response = await coddy.net.fetch("http://127.0.0.1:8080/api/status");
if (!response.ok) throw new Error(`HTTP ${response.status}`);
const status = await response.json();
```

Use this in an asynchronous UI handler or activation, not inside `transform`. Refresh a cache asynchronously if a stream processor needs network data. Requests are buffered, limited to 30 seconds, 4 MiB upload and 8 MiB download. Target authentication must be supplied explicitly; redirects are returned without following them.

The [short tutorial](../../docs/tutorials/first-extension.md) walks through the files. The [extension API reference](../../docs/features/extensions.md) covers assets, menu actions, incoming data and the network contract.
