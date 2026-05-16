# UI

This folder documents the embedded web UI.

- Source code lives in `external/ui/` (TypeScript, React, Vite dev server)
- Build output is generated into `external/ui/dist/` and then synced into `external/ui/` as `index.html`, `styles.css`, `app.js` (embedded into the `coddy` binary when the **`ui`** Go build tag is set together with **`http`**)

## Go build tags (**`http`** and **`ui`**)

- **`http`** links the OpenAI-shaped HTTP gateway (**`coddy http`**, **`/v1/*`**, **`/coddy/*`**, Swagger). It does **not** embed the SPA. **`GET /`** returns a plain **404** hint.
- **`http`** **+** **`ui`** (for example **`make build TAGS="http ui"`**, equivalent to **`go build -tags=http,ui`**) runs **`make ui-build`** first and links **`go:embed`** assets for **`/`**, **`/index.html`**, **`/app.js`**, **`/styles.css`**.
- **`scheduler`** is independent (cron daemon and tools); combine with **`http`** or **`http,ui`** when you need **`coddy http`** and jobs in one binary.
- **`Dockerfile`** / **`docker-compose.yml`** pass the same list as **`BUILD_TAGS`** (comma-separated, default **`http,scheduler,ui`**). The Node stage always produces a fresh UI bundle; **`ui`** in **`BUILD_TAGS`** controls whether the Go linker includes it.

## Quick start

### Embedded same-origin

When UI is served from `coddy http` together with `/v1` and `/coddy`, the SPA uses relative paths. No proxy and no backend URL env is involved.

### Local development with separate processes

Backend

```bash
make build TAGS=http
./build/coddy http --config config.yaml --home /tmp/coddy-ui-dev-home --sessions-dir /tmp/coddy-ui-dev-sessions -H 127.0.0.1 -P 12345
```

Frontend

```bash
npm --prefix external/ui install
CODDY_UI_BACKEND=http://127.0.0.1:12345 npm --prefix external/ui run dev -- --host 127.0.0.1 --port 5173
```

Without `CODDY_UI_BACKEND`, Vite does **not** install a proxy. Requests to `/v1` hit the dev server unless you combine both under one hostname (reverse proxy).

Open

- `http://127.0.0.1:5173/`

### UI-only edits

After `npm install`, `npm --prefix external/ui run dev` is enough to iterate TypeScript/CSS. Rebuilding `coddy` with **`make build TAGS="http ui"`** is only needed when you want to validate `go:embed` bundles or CI.

## Specs

See `spec.md` for functional requirements and design constraints.

## Design references

Reference images should be stored under `assets/`.

## Brand assets (README and preview only)

Canonical SVG logos live in [`assets/coddy-logo-*.svg`](assets/) for the repo README and review page. The embedded SPA nav brand stays **text only** (see **`DESIGN.md`**).

```bash
npm --prefix external/ui run dev -- --host 127.0.0.1 --port 5173
# open http://127.0.0.1:5173/logo-preview.html
```
