# Coddy UI design

Choose the contract for the surface you are changing. The linked files are references to open when needed, not imports to load together. Keep this index under 8 KiB (`make docs-check`).

## Web UI (`external/ui/`)

Read the relevant SPA contract below before changing layout, styles, interactions, or localized copy.

- Show a fact or action once, in the surface that owns it. Keep accessible names where needed.
- Use shared tokens and component primitives. Put shared sizes, layers and typography in `_tokens.scss`, palettes in `themes/`, and component styles in their surface partial. Keep `styles.scss` as the cascade entry, with simplified mode last.
- Use theme tokens for accents and foreground contrast. Do not hardcode the dark palette in shared styles.
- Respect the responsive grid. Check browser geometry in a real browser when jsdom cannot measure it.
- Update every supported locale when changing user-facing copy. External products are references, not the contract.

### Web UI contracts

| Topic | Contract |
| --- | --- |
| Stylesheet, tokens, themes, localization and onboarding | [Foundations](specs/ui/foundations.md) |
| Settings, System tab, statistics and sparklines | [Settings](specs/ui/settings.md) |
| Sessions, scheduler routes, tasks, Files, Terminal, Edits, Canvas and plan cards | [Workspace](specs/ui/workspace.md) |
| Breakpoints, phone layout, history and navigation rail | [Layout](specs/ui/layout.md) |
| Tool rows, permissions, questions, tasks, attachments in the transcript and history rewind | [Transcript](specs/ui/transcript.md) |
| Composer, goals, model controls, attachments, context and transcript scrolling | [Composer](specs/ui/composer.md) |
| Markdown, memory, swarm, documentation, sign-in, desktop and PWA | [Components](specs/ui/components.md) |
| UI states, non-goals and local development | [Development workflow](specs/ui/workflow.md) |
| SPA file ownership, state and regression test notes | [Implementation notes](specs/ui/implementation.md) |

## Console TUI (`external/cli/`)

- For terminal layout, rendering, or input behavior, read the console [visual model](docs/surfaces/console.md#visual-model) and [commands and keys](docs/surfaces/console.md#commands-and-keys).
- For render caching, local shell, and question behavior, read the scoped console rule ([Cursor](.cursor/rules/console-tui.mdc), [Claude Code](.claude/rules/console-tui.md)). Use the [console test guide](docs/surfaces/console.md#testing) for terminal-output regressions.
- Do not apply SPA stylesheet or browser geometry rules to the console. If a change affects both surfaces, read both contracts.
