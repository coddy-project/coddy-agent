---
description: Streaming completion safety and protocol termination
paths:
  - "internal/llm/**/*.go"
  - "internal/agent/**/*.go"
  - "external/httpserver/**/*stream*.go"
  - "features/llm_stream*.feature"
  - "features/llamacpp_openai_stream.feature"
  - "features/openai_stream_compat.feature"
---
# LLM streaming

- Never retry after any visible delta has been emitted.
- Cancellation, truncation, and idle-stall errors retain partial content.
- Require the protocol terminal event where specified; clean EOF alone is not success.
- Accept legal SSE comments and blank frames. Malformed non-empty frames produce bounded diagnostic errors.
- A filtered view emits keepalive comments for swallowed frames or owns an independent idle timer.
