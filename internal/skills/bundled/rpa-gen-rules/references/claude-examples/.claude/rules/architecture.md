---
paths:
  - "src/**/*.py"
  - "main.py"
---

# Architecture (example globs)

Adjust `paths` to your layout (`lib/**`, `app/**`, or a single package name). Path-specific rules reduce noise. See [path-specific rules](https://code.claude.com/docs/en/memory#path-specific-rules).

## Intent

- Clear module boundaries. No business logic leaking across layers in the wrong direction.
- Prefer dependency direction from outer adapters inward to domain code.
- One class or module at a time when extending the system. Lower layers first.

## Replace this section

Document real packages, allowed imports, and forbidden cycles for your repository. Link `@docs/ARCHITECTURE.md` from `CLAUDE.md` if it exists.
