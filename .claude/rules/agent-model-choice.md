---
description: User-owned model choice and settings notices
paths:
  - "internal/agent/**/*.go"
  - "internal/tooling/**/*.go"
  - "internal/tools/switch_model*.go"
  - "internal/tools/compact_context*.go"
  - "internal/config/**/*"
  - "internal/session/**/*settings*.go"
  - "external/scheduler/**/*.go"
  - "external/cli/**/*.go"
---
# Agent model choice and settings notices

- The orchestrator calls `switch_model` or model-selecting `compact_context` only on explicit user request. Do not restore autonomous model switching through configuration.
- Spawned and scheduled subagents may choose or inherit model and reasoning through normal spawn precedence.
- SYSTEM notices represent settings changes made by the agent, not operator flags, commands, or selectors.
- Filter ambiguous legacy notices on read; do not rewrite stored sessions.
- Preserve command responses on surfaces that have no other acknowledgement.
