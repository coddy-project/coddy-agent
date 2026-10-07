# BDD-style project instructions across coding-agent hosts

This reference explains how to encode repository behavior in project instructions without treating one coding-agent vendor's directory as universal policy.

## BDD in project rules

Behavior belongs in executable tests. Project instructions tell the coding agent how to preserve that behavior:

1. New behavior begins with a failing test that describes the observable result;
2. a bug fix begins with a regression test that reproduces the failure;
3. the smallest implementation makes the focused test pass;
4. the repository's full test and lint gates run before completion;
5. user-visible behavior, APIs, configuration, examples, and generated artifacts are updated in the same change.

Use the repository's actual commands and test layout. Do not paste Python examples into a Go project or invent a Gherkin workflow where the repository does not use one.

Layered architecture and TDD answer different questions:

- `architecture` and `implementation-order` decide where a change belongs and which dependency layer comes first;
- `workflow` and `testing` decide the delivery order, starting with RED and ending with full verification.

## Common baseline

Put requirements that every host must see in root `AGENTS.md`:

- repository and package boundaries;
- dependency direction;
- test and lint gates;
- security and trust boundaries;
- documentation and generated-file obligations;
- language and commit conventions;
- Rules Sync when the project maintains native mirrors.

A critical invariant must not live only in path-scoped model context. Path discovery, host trust, tool payloads, and timing differ. Enforce critical behavior through root policy, code, permission checks, tests, or CI.

When Claude Code support is required, use a root `CLAUDE.md` symlink to `AGENTS.md` where the checkout preserves symlinks. Otherwise use a short import or synchronized compatibility file and verify it explicitly.

## Cursor

Cursor project rules live under `.cursor/rules/` as `.mdc` files. Frontmatter can contain:

- `description`;
- `alwaysApply`;
- `globs`.

Use an always-on rule only for policy needed in every Cursor session. Use `globs` with `alwaysApply: false` for path-scoped topics.

Example:

```markdown
---
description: HTTP API contracts
globs: src/api/**/*.py
alwaysApply: false
---

# HTTP API

Update handlers, schemas, tests, and API documentation together.
```

Do not assume that another host reads `.mdc` merely because Cursor does.

## Claude Code

Claude Code project instructions use root `CLAUDE.md` and Markdown files under `.claude/rules/`.

- A rule without `paths` is always loaded;
- a rule with `paths` is path-scoped;
- confirm exact behavior against the installed Claude Code version when it matters.

Example:

```markdown
---
description: HTTP API contracts
paths:
  - src/api/**/*.py
---

# HTTP API

Update handlers, schemas, tests, and API documentation together.
```

Keep reusable procedures in skills and repository-specific policy in project instructions.

## Cursor and Claude Code mapping

Normalize the intent before translating the format:

| Intent | Cursor | Claude Code |
|---|---|---|
| Always-on | `alwaysApply: true`, no narrowing glob | no `paths` |
| Path-scoped | Cursor `globs` with `alwaysApply: false` | `paths` |

Keep paired Markdown bodies equivalent. Adapt only native frontmatter and host-specific links. Do not widen a Claude `paths` rule into Cursor `alwaysApply: true`.

Neither vendor tree is automatically a universal source of truth. They are native compatibility representations maintained by the target repository.

## OpenAI Codex

Root `AGENTS.md` is the common baseline for Codex. When the project needs its Cursor topic rules in Codex too, install the bundled project hook:

- `.codex/hooks.json` wires session and pre-tool events;
- `.codex/hooks/attach_rules.py` reads `.cursor/rules/*.mdc` directly;
- `alwaysApply: true` rules are attached at `SessionStart`;
- scoped rules whose `globs` match recognized edit paths are attached at `PreToolUse`;
- rule bodies are not copied into a third tree.

The hook parser accepts scalar, flow-list and block-list `globs`, including quoted commas, blank lines and YAML comments. Probe it against representative add, update, delete and rename payloads before reporting support.

State is keyed by canonical repository path and session. `resume` preserves scoped dedupe; `startup`, `compact` and `clear` reset it to the always-on set. Concurrent hook processes claim rule ids under a per-session lock and persist state with atomic replacement.

This is a project adapter, not native Codex rule discovery. Its limits must remain visible:

- project hooks require trust and can be disabled or skipped;
- a fail-open hook is not a security boundary;
- opaque shell writes may expose no path to the hook;
- host event and payload behavior can change by version;
- root `AGENTS.md`, code, tests, permission checks, and CI still own critical constraints.

Tell the user to run `/hooks` once per clone and again after changing either hook file. Record the required Python interpreter and any unsupported platform behavior.

Codex execpolicy `.rules` files are unrelated. They decide command execution policy rather than model instructions.

## Coddy, OpenCode, ZCode, and other hosts

Do not infer support from a directory name.

For each additional host:

1. Inspect the mechanism already used by the repository;
2. verify whether it reads root and nested instruction files;
3. verify whether scoped instructions arrive before a mutation;
4. verify trust, reset, and compaction behavior;
5. record unsupported behavior instead of inventing a manifest, plugin, or hook.

A project may deliberately use `.coddy/rules`, `.agents/rules`, an OpenCode plugin, or a ZCode hook. This skill does not choose one as a new cross-host standard. Keep existing integrations only when their behavior is documented and tested.

## Language

Write rule files and root briefs in English unless the user requests another language or the repository already has a deliberate language policy.

Keep mirrored policy in one language. Translating one tree independently makes parity impossible to review. The chat continues in the user's language.

## Rules Sync

Rules Sync applies to the instruction surfaces the repository actually supports.

1. Inventory root `AGENTS.md`, `CLAUDE.md`, `.cursor/rules`, `.claude/rules`, the Codex hook, and any existing verified host integration;
2. decide which files represent shared policy and which are intentionally host-specific;
3. update deliberate Cursor and Claude Code counterparts in the same change;
4. keep shared bodies equivalent and translate only native metadata or links;
5. map always-on and path-scoped behavior correctly;
6. verify the `CLAUDE.md` compatibility path after changing `AGENTS.md`;
7. keep the Codex hook template unchanged unless its parser or event contract itself changes; it reads Cursor rules directly and has no manual rule index to synchronize;
8. list every synchronized file and every unsupported host limitation in the report.

When one host needs unique mechanics, keep that section small and label the difference. Do not copy host-specific setup into every policy body.

## Recommended topic split

Use only topics the repository needs:

| Topic | Owns |
|---|---|
| `workflow` | feature and bugfix delivery, final verification, Rules Sync |
| `testing` | test layout, naming, fixtures, commands |
| `architecture` | layers, modules, dependency direction |
| `code-style` | formatter, types, comments, language |
| `implementation-order` | layer-by-layer implementation sequence |
| `api-layer` | handler, schema, transport, and API docs parity |
| `core-modules` | domain-specific package responsibilities |

Keep rules focused. A very large always-on file consumes context for unrelated tasks and becomes stale faster.

## Bundled examples

- `references/cursor-examples/.cursor/rules/` contains Cursor templates;
- `references/claude-examples/.claude/` contains Claude Code templates;
- `references/codex-examples/.codex/` contains the Codex hook template that consumes Cursor rules.

Adapt them to the target repository. They are examples, not proof that a host integration exists.
