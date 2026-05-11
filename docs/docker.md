# Docker

This repository can be built into a minimal `scratch` runtime image that contains only the `coddy` binary and CA certificates bundle.

## Files

- `Dockerfile` - multi-stage build, `scratch` runtime
- `docker-compose.yml` - runs `coddy http` on port 12345

## Run

Create a local `config.yaml` (do not commit it, it may contain secrets).

Minimal workflow

- Copy `config.example.yaml` to `config.yaml`
- Fill provider keys in `providers` (for example `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `DEEPSEEK_API_KEY`)
- Pick a default `agent.model` that exists in `models`

Start the server

```bash
docker compose up -d --build coddy
```

Check API shape

```bash
curl -sS http://127.0.0.1:12345/v1/models | head
```

## Example smoke test

```bash
./examples/httpserver/docker.sh
```
