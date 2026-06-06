---
description: Formatting, lint, comments language
paths:
  - "**/*.go"
---

# Code style (Go)

1. **`gofmt`** / **`go fmt`** - format before committing when you touch files.
2. **`make lint`** - **`golangci-lint run ./...`** is the repo gate (see **`Makefile`**).
3. Comments in code - **English only**.
4. Follow existing patterns in neighboring files (imports grouping, naming, error handling).

## References

@Makefile
