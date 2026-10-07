---
description: Tool-owned image persistence and provider projection
paths:
  - "internal/agent/tool_images.go"
  - "internal/session/tool_images.go"
  - "internal/tools/fs/**/*"
  - "internal/llm/**/*"
  - "external/httpserver/coddy_coddy.go"
  - "external/httpserver/coddy_session_asset*.go"
  - "external/gateway/telegram/pictures.go"
---
# Tool images

- An image read by a tool stays `role:tool` through `ImageParts`; never turn it into a synthetic user message.
- Provider projection may combine images later, but persisted history retains tool ownership.
- Read a copied session asset only while its digest matches; never reread the original arbitrary path.
- Prompt and tool images share one selection policy, with newer images preferred.
- Non-multimodal models receive no image parts.
- Surfaces render `_meta.coddy.images`; there is no preview configuration switch.
