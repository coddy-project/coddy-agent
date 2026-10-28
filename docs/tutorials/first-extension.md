# Your first JavaScript extension

Create a page with a counter, package it as one ZIP and enable it in Coddy. You need Python to make the archive and a running Coddy web UI over HTTPS or localhost. You do not need Node.js or a model call.

## Create two files

Make a folder with `manifest.json` and `main.js`. The complete files are in [examples/extensions/first-extension](../../examples/extensions/first-extension/).

`manifest.json` identifies the package and its module.

```json
{
  "api": 1,
  "id": "first-extension",
  "name": "My first extension",
  "version": "1.0.0",
  "ui": { "entry": "main.js" }
}
```

`main.js` exports the function Coddy calls after you approve the package.

```js
export function activate(coddy) {
  const page = coddy.ui.registerPage({
    id: "counter",
    title: "My first extension",
    mount(element) {
      let count = 0;
      const button = document.createElement("button");
      button.textContent = "Clicked 0 times";
      button.addEventListener("click", () => {
        button.textContent = `Clicked ${++count} times`;
      }, { signal: coddy.signal });
      element.append(button);
      return () => button.remove();
    }
  });
  return () => page.remove();
}
```

The page appears in **Extensions**. Coddy calls `mount` when you open it and its cleanup when you close it. The counter starts at zero on each opening. `coddy.signal` ends listeners and SDK requests when the extension stops. Keep dependencies inside the module; relative imports cannot resolve from its blob URL.

## Package and enable

Run this command inside the folder containing the two files.

```sh
python -m zipfile -c first-extension.zip manifest.json main.js
```

Open **Settings → Extensions**, choose **Install ZIP**, then select `first-extension.zip`. Installation leaves it disabled. Check its name and digest, choose **Enable** and **Accept risk and enable**. The page reloads. Approved code can access chats, credentials, server APIs and services reachable from the Coddy server; enable it at your own risk.

Open **Extensions → My first extension** and click the button. It should say `Clicked 1 times`. Close and reopen the page; it should say `Clicked 0 times` again.

## Add a network call

In an asynchronous UI handler, use `coddy.net.fetch` to call a service from the Coddy server without target CORS headers.

```js
const response = await coddy.net.fetch("http://127.0.0.1:8080/api/status", {
  signal: coddy.signal
});
if (!response.ok) throw new Error(`HTTP ${response.status}`);
const status = await response.json();
```

Start your API on that address first, or change the URL to your service. `localhost` refers to the server running Coddy. The [Local API example](../../examples/extensions/local-api/main.js) has a URL field, GET/POST requests and cancellation. Target HTTP errors are returned as responses; network and proxy errors reject the promise. The helper supports HTTP(S), buffers the response and returns redirects without following them. See the [network contract](../features/extensions.md#server-side-http-requests) for headers and limits. Incoming-stream `transform` callbacks must stay synchronous; read cached network data from them.

## Change or remove the extension

After editing the files, remove the installed package, create the ZIP again, install it and approve its new digest. Existing ids cannot be overwritten. To stop it, choose **Disable and reload** in **Settings → Extensions**.

If the page does not appear, check that the files are at the ZIP root, the package is enabled and `activate` is exported. Settings shows activation errors. Use HTTPS or localhost so Web Crypto can verify the entry digest. The [API guide](../features/extensions.md) and [example index](../../examples/extensions/README.md) cover the next steps.
