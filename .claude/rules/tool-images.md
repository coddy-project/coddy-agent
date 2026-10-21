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
  - "internal/mcp/client.go"
---
# Tool images

- An image read by a tool stays `role:tool` through `ImageParts`; never turn it into a synthetic user message.
- Provider projection may combine images later, but persisted history retains tool ownership.
- Read a copied session asset only while its digest matches; never reread the original arbitrary path.
- Prompt and tool images share one selection policy (`pictureWindowStart`): a run of the newest pictures that only grows until it overflows a request, then loses its oldest together down to half of it, so the request prefix and the prompt cache hold between evictions. Decide it from the part sizes alone, never from which copies load.
- Non-multimodal models receive no image parts.
- An MCP tool's image blocks take the same path (`callMCPTool` -> `recordToolImage`): checked by `fs.PrepareImageForModel` with `read`'s limits and told by content, never by the declared type; kept for a text-only model too, which is sent only their names.
- Surfaces render `_meta.coddy.images`; there is no preview configuration switch.
