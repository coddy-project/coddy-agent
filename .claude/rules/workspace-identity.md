---
description: Canonical workspace identity across ownership and trust boundaries
paths:
  - "internal/session/**/*.go"
  - "internal/remote/**/*.go"
  - "internal/mcp/**/*.go"
  - "internal/subagents/**/*.go"
  - "internal/hooks/**/*.go"
  - "internal/skills/**/*.go"
  - "external/httpserver/**/*.go"
---
# Workspace identity

- Compare workspace ownership, filtering, trust, and cache identity through `SameWorkspacePath` or the canonical form, never raw strings.
- Account for symlinks, `/tmp` versus `/private/tmp`, and case-insensitive Windows and macOS paths.
- Keep the logical path for display and the canonical key for identity.
- Remote adapters forward workspace filters.
