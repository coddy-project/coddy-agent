---
paths:
  - "main.py"
---

# HTTP API layer (example)

This file shows a **narrow** `paths` list so rules apply only to the entry module. Rename `main.py` or add paths such as `src/api/**/*.py` for your project.

## Principles

1. Thin layer: validation and mapping only, no core business rules duplicated here.
2. Return consistent error shapes and status codes documented for clients.
3. Keep request and response models next to the router or in a dedicated `schemas` module per project norms.
