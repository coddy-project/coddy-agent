---
description: "Generate focused project rules under .coddy/rules from repository analysis"
---

# Generate Coddy rules

Use this skill when the user invokes `/coddy-generate-rules` to create or refresh project rules.

## Workflow

1. If the user did not describe scope, ask what to cover (style, tests, HTTP, UI, architecture).
2. Read `README`, `AGENTS.md`, `DESIGN.md`, `docs/`, existing `*/rules/`, `Makefile`, and representative source files.
3. Propose several small `.mdc` files (not one giant rule).
4. Write files under `.coddy/rules/<name>.mdc` by default (use `.cursor/rules/` only if the user asks for Cursor compatibility).
5. Frontmatter semantics:
   - Auto rules: `alwaysApply: true` plus meaningful `globs` (or `paths` for Claude-style files).
   - Manual rules: `alwaysApply: false` plus `description` (activated only via `@ruleName` in chat, not slash).
6. Report created files, globs, and how to verify with `coddy rules list` and a test turn with a matching file in context.

## Content guidelines

- Keep each rule under 200 lines when possible.
- Reference canonical examples in the repo instead of pasting large code blocks.
- English only in rule bodies.
