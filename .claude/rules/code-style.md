---
description: Formatting, lint, comments language
paths:
  - "**/*.go"
---

# Code style (Go)

1. **`gofmt`** / **`go fmt`** - format before committing when you touch files.
2. **`make lint`** - the repo gate (see **`Makefile`**): **`make fmt-check`** (`gofmt -s -l` over every Go file git tracks, whatever its build tags or platform), `golangci-lint` over the untagged tree, over every tag but `ui` and over the shipped tag set, then `tsc --noEmit` over the SPA sources (`make ui-typecheck`). **`.golangci.yml`** enables the `gofmt` formatter (`gofmt -s`) beside the linters it names (golangci-lint's five `standard` ones, `gosec` and `nolintlint`), so an unformatted file fails the gate; `gofmt -s -w <file>` or `golangci-lint fmt` fixes it. A `//nolint` names the linter and the gosec rule it silences and says why (`//nolint:gosec // G115: ...`), and `nolintlint` reports one that silences nothing; the classes gosec reports on what Coddy does by design (running commands, reading files, ordinary file modes, tests and test doubles) are excluded in `.golangci.yml` with the reason, not line by line.
3. Comments in code - **English only**.
4. Follow existing patterns in neighboring files (imports grouping, naming, error handling).

## References

@Makefile
