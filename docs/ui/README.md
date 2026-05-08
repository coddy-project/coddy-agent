# UI

This folder documents the embedded web UI.

- Source code lives in `external/ui/` (TypeScript, React, Vite dev server)
- Build output is generated into `external/ui/dist/` and then synced into `external/ui/` as `index.html`, `styles.css`, `app.js` (embedded into the `coddy` binary)

## Quick start

Backend

```bash
make build TAGS=http
./build/coddy http --config config.yaml --home /tmp/coddy-ui-dev-home --sessions-dir /tmp/coddy-ui-dev-sessions -H 127.0.0.1 -P 12345
```

Frontend

```bash
npm --prefix external/ui install
npm --prefix external/ui run dev -- --host 127.0.0.1 --port 5173
```

Open

- `http://127.0.0.1:5173/`

## Specs

See `spec.md` for functional requirements and design constraints.

## Design references

Reference images should be stored under `assets/`.
