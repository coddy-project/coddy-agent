# Console TUI design

This contract applies to `external/cli/`. Use the [design index](../../DESIGN.md) for other surfaces.

## Layout and input

- Preserve the console's top-to-bottom order: header and session context, transcript, input, and footer. Use terminal cell widths for wrapping and clipping rather than browser breakpoints.
- Keep transcript states distinguishable: user messages have a full-width background, thinking is dim and collapsible, and tool rows show their outcome and a bounded preview.
- Before changing shortcuts or focus behavior, check [Commands and keys](../../docs/surfaces/console.md#commands-and-keys). The [Visual model](../../docs/surfaces/console.md#visual-model) describes the full layout, status lines, and rendering details.

## Rendering and dialogs

- The same backing slice and length returned from `Render` means unchanged. Stable components must not return fresh equal slices.
- Never mutate component-owned rendered slices during frame decoration. Invalidate or release caches on Clear, child removal, width changes, and zero-child transitions; test A -> A -> B at terminal output.
- Only the question modal uses `DescriptionBelow`; other selectors keep the default layout. Completed question tools render question and answer pairs through `questionReadout`, not raw JSON.

## Verification

Use terminal-output assertions for repaint regressions. Run `go test -tags=cli ./external/cli/...` for focused coverage. See the [console test guide](../../docs/surfaces/console.md#testing) for the BDD harness and real-PTY checks.
