---
description: Devin transport framing, identity, and response semantics
paths:
  - "internal/llm/devin*.go"
  - "internal/devinfake/**/*.go"
  - "cmd/coddy/devin.go"
  - "features/devin*.feature"
---
# Devin provider protocol

- Preserve Connect/protobuf framing and per-frame gzip; this is not ordinary JSON or HTTP gzip.
- Parse the end-stream trailer even with HTTP 200, and map application errors before success.
- Use `windsurf` identity for the full catalog and `devin-desktop` for chat.
- Usage input includes cache read/write.
- Tool calls select `tool_use` even when the numeric stop value differs.
- Update binary golden fixtures and `devinfake` whenever decoder behavior changes.
