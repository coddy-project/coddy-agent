---
description: Skill execution semantics and bundled skill delivery
paths:
  - "internal/skills/**/*.go"
  - "internal/agent/plugin_command.go"
  - "internal/tools/**/*skill*.go"
  - "external/ui/src/ui/skills/**/*"
  - "docs/features/skills.md"
  - "docs/reference/slash-commands.md"
  - "scripts/bundled-skills.json"
  - "scripts/vendor-bundled-skills.sh"
  - "internal/skills/bundled/**/*"
---
# Skills and bundled delivery

- A skill is instruction content for the current agent, never a hidden child run or `context:fork`.
- Isolated execution uses an explicit subagent definition and `spawn_agent`.
- Handle context size with concise skills, progressive disclosure, references, delegation, and compaction.
- Bundled skills are embedded and seeded locally; startup never clones upstream. Keep `//go:embed all:bundled` so dot-directories remain included.
- Tests that are not about the system marketplace use the offline `SystemSources` helper.
- Replace an installed bundled skill only when the bundled version is newer. A missing version is legacy; preserve same-version, newer, or unreadable user copies.
- Vendor through the established vendor and check targets, not manual generated-file edits.
