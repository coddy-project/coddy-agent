---
description: Console TUI rendering, local shell, and question behavior
paths:
  - "external/cli/**/*.go"
  - "internal/tools/shell/operator.go"
  - "examples/cli/**/*.py"
  - "features/cli*.feature"
  - "docs/surfaces/console.md"
---
# Console TUI behavior

Read the console [visual model](../../docs/surfaces/console.md#visual-model) for rendering and layout changes and the [testing guide](../../docs/surfaces/console.md#testing) for regressions. The [design index](../../DESIGN.md) routes Web UI and TUI changes to separate contracts.

- The same backing slice and length returned from `Render` means unchanged. Stable components must not return fresh equal slices.
- Never mutate component-owned rendered slices during frame decoration. Invalidate or release caches on Clear, child removal, width changes, and zero-child transitions; test A -> A -> B at terminal output.
- `!!` stays local and remote-disabled. It uses no permission gate, session, or task pool, is not persisted, and is invisible to the model.
- Only the question modal uses `DescriptionBelow`; other selectors keep the default layout. Completed question tools render question and answer pairs through `questionReadout`, not raw JSON.
- TUI question stubs emit the real tool-call lifecycle.
- Never pass a real repository as `CoddyTUI.workdir`, because driver cleanup deletes home and workdir. Use temporary copies or `keep_dirs`.
