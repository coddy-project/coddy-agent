# OpenCode hooks for project rules

This page is for contributors running OpenCode against this repository. It does not describe Coddy's own rule discovery: the `coddy` binary injects those rules through `{{.Rules}}`, as documented in [Rules](rules.md).

## Why a plugin is needed

OpenCode reads the root `AGENTS.md`, so the repository map and always-relevant contributor notes already reach a session. OpenCode can also load instruction globs from `opencode.json`, but every matching instruction file is combined into the prompt; Cursor's per-file `globs` semantics are not applied to `.mdc` frontmatter.

This repository keeps detailed instructions in `.cursor/rules/*.mdc`. The project plugin in `.opencode/plugins/project-rules.js` reads that frontmatter directly and attaches only the rules needed by the current session. `.cursor/rules/` remains the single source of truth.

OpenCode automatically loads JavaScript and TypeScript files under `.opencode/plugins/`. No `opencode.json` entry or package installation is required for this plugin. It uses only Node-compatible APIs provided by the OpenCode runtime.

## Hook mapping

| OpenCode hook | Behaviour |
|---|---|
| `experimental.chat.system.transform` | Adds every `alwaysApply: true` rule and the session's active scoped rules to each model request. |
| `tool.execute.before` | Extracts file paths from tool arguments and patch headers, then activates rules whose `globs` match those paths. |
| `experimental.session.compacting` | Adds the selected rules to the compaction context so they remain explicit in the continuation summary. |
| `session.deleted` event | Releases the in-memory scoped-rule set for the deleted session. |

Reading a governed file activates its scoped rules for the next model request. This normally means the rules are present before the model edits the file.

OpenCode's stable `tool.execute.before` hook can block or modify a tool call, but it cannot add a model-visible message to the current call. Therefore, if `edit`, `write`, `apply_patch`, or another recognized write tool is the first operation that reveals a scoped rule, the plugin intentionally rejects that call once. The error asks OpenCode to retry; the following model request receives the newly activated rule through `experimental.chat.system.transform`, and the repeated edit is allowed. This makes the first write deterministic instead of relying on the model to open a referenced rule file.

## Rule parsing and matching

The implementation reads `description`, `globs`, and `alwaysApply` from the flat Cursor frontmatter used in this repository. A glob translator keeps `*` inside one path segment and treats `**/` as zero or more directories, so `external/httpserver/**/*.go` matches both `external/httpserver/server.go` and deeper files.

Tool arguments are inspected recursively. Recognized path keys include `filePath`, `path`, `filename`, `target`, and their common variants. Unified patch payloads recognize `*** Add File:`, `*** Update File:`, `*** Delete File:`, and `*** Move to:` headers. Absolute paths outside the repository are ignored.

Rules are read again for each relevant hook, so editing or adding an `.mdc` file does not require plugin wiring. A malformed or unreadable rule file is skipped without disabling the remaining rules.

## Session state and compaction

Scoped activation is held in memory by OpenCode's plugin process and keyed by session id. It survives normal compaction and is cleared when OpenCode reports `session.deleted`. Restarting OpenCode clears this cache. After a restart, reading a governed file activates its rule again; a direct first edit may receive the one-time retry guard again.

The plugin deliberately adds selected rules to every system transform instead of tracking whether their text was already sent. Model requests are independent after compaction, and repeated system context is the reliable place for durable constraints.

## Verification

The unit suite covers always-on injection, recursive Cursor glob matching, activation by `read`, the first-write retry, patch path extraction, and session cleanup:

```bash
make test-opencode-rules
```

Or run the underlying command directly:

```bash
node --test .opencode/tests/project-rules.test.js
```

No provider connection or OpenCode installation is required for the tests. To verify the real integration, start a new OpenCode session in the repository, ask it to read `external/httpserver/server.go`, and inspect the next model request with OpenCode debug logging.

## Compatibility and security

The system-transform and compaction hooks are currently marked `experimental` by OpenCode. Re-run the unit suite and a real smoke test when upgrading OpenCode.

Project plugins are executable code and OpenCode loads them automatically. Review `.opencode/plugins/` when opening an untrusted checkout. This plugin does not spawn commands, access the network, or load third-party dependencies; it only reads rule files inside the current worktree and keeps per-session rule names in memory.

Upstream references: [OpenCode rules](https://opencode.ai/docs/rules/) and [OpenCode plugins](https://opencode.ai/docs/plugins/).
