# Agent Notes for Coddy

Short map for automation-friendly contributors.

## Repository navigation

| Area | Responsibility |
|------|------------------|
| `cmd/coddy` | CLI entry (bare `coddy` console, `cli`, `acp`, `http`, `gateway`, `sessions`, `skills`, `plugin`, `mcp`, `codex login`, `rules list`, `update`). |
| `internal/agent` | ReAct orchestration, MCP/tool wiring. |
| `internal/mcp` | MCP transports, merged server list, and the **workspace trust gate** for project-local **`.coddy/mcp.json`** (**`trust.go`**, **`gate.go`**; policy **`mcp.project_trust`**, approvals in **`<home>/mcp-trust.json`**). Guide: **`docs/mcp-integration.md`**. |
| `internal/remote` | Go client for a remote `coddy http` server: SSE frames back into ACP updates, `/coddy` REST, permission/question answers. Powers `--remote` on the console and `coddy acp`. Guide: **`docs/cli.md`** (Remote mode), **`docs/remote-control.md`**. |
| `internal/bgtask` | Background task pool for detached shell commands (**`run_command`** **`background: true`** plus the **`background_*`** tools, the Tasks drawer, and **`/coddy/sessions/{id}/background-tasks`**). **`Pool.Adopt`** takes over a foreground command that outlived its timeout instead of killing it. Guide: **`docs/background-tasks.md`**. |
| `internal/session` | Session manager, Filesystem persistence, Acp hooks, rules catalog. |
| `external/httpserver` | **`coddy http`** when built with **`tags=http`** (SSE bridge, Swagger statics, `/coddy` REST, ServeMux wiring). |
| `external/ui` | Embedded SPA (`go:embed`) when built with **`tags=http,ui`**. |
| `external/memory` | Long-term memory copilot (**`-tags memory`**; see README there). |
| `external/cli` | Interactive console TUI (**`-tags cli`**): bare **`coddy`** on a terminal, pi-style rendering in **`external/cli/tui`**. Guide: **`docs/cli.md`**. |
| `external/gateway` | Messenger gateway (**`-tags gateway.telegram`** or **`-tags gateway`**): Telegram bot adapter, session store, proxy support. Full guide: **`docs/gateway.md`**, rules: **`.cursor/rules/gateway.mdc`**. |

## Builds

Run **`make build TAGS=http`** for the HTTP gateway only (**`coddy http`** REST and **`/docs`**, no **npm**). Run **`make build TAGS=cli`** for the interactive console (**bare `coddy`** on a terminal; see **`docs/cli.md`**). Run **`make build TAGS="http ui"`** to link the embedded SPA (**Makefile** runs **ui-build** before **go build**). Recommended full image matches **`Dockerfile`** (**`make build TAGS="http ui scheduler memory cli"`**). Default **`make build`** omits HTTPServer, scheduler, and memory to keep dependency surface lean.

Primary conversational surface for bundled UI lives at **`POST /v1/responses`** with **`stream:true`**. Prefer it over **`POST /v1/chat/completions`** when shipping Coddy-hosted experiences.

Swagger lives at **`/docs/`**, OpenAPI YAML at **`/openapi.yaml`**.

## Pre-commit gate

A git **`pre-commit`** hook runs the linter before every commit, so nothing lands with lint errors. It is the single enforcement point for humans and coding agents alike. The full test matrix (**`make test`**) is slow, so it is **opt-in** on commit and belongs in CI / before push.

- Enable once per clone: **`make hooks`** (sets **`core.hooksPath=.githooks`**; this is local config and is not committed, so every clone runs it once).
- On commit, **`.githooks/pre-commit`** calls **`scripts/checks.sh`**, which runs **`make lint`** by default. Commits touching only non-code files (docs, etc.) skip the gate.
- Scope knobs: **`CODDY_HOOK_TESTS=fast`** also runs a quick **`go test ./...`**, **`CODDY_HOOK_TESTS=full`** the whole matrix; **`CODDY_HOOK_LINT=0`** skips the linter; **`CODDY_HOOK_SKIP=1`** bypasses everything.
- Emergency bypass for a single commit: **`git commit --no-verify`**.
- Both gates compile the **host** platform only. Changing a file behind **`//go:build windows`**, or a signature it shares with the rest of the tree, needs **`make check-windows`** (cross-build plus **`go vet`** over every non-**`ui`** tag combination, test files included) and **`make lint-windows`**. CI runs both, and additionally runs **`go test`** on a real **`windows-latest`** runner for **`internal/platform`**, **`internal/bgtask`**, and **`internal/tools/shell`**.

## Documentation contract

Human prose for HTTP lives in **`docs/http-api.md`**. Visual spec for SPA lives in **`DESIGN.md`** (this repo root). Architectural narrative remains under **`docs/architecture.md`**.

All **code comments** plus **technical markdown authored for this repo** (including `docs/`, `DESIGN.md`, `AGENTS.md`) stay **English** unless an operator explicitly asks for another natural language.

## Codex, OpenCode, Cursor and ZCode rules

Codex uses this **`AGENTS.md`** file as its repo instruction entrypoint, and ZCode resolves its `AGENTS.md` chain the same way. The detailed project rules live in **`.cursor/rules/*.mdc`**, which is their single source of truth. Do not copy rules into `.codex/`, `.claude/`, or `.zcode/` by hand.

Codex resolves its `AGENTS.md` chain once per session, walking from the repository root down to the launch directory, so nested instruction files never load for a session started at the root. A lifecycle hook covers that gap and delivers the Cursor rules deterministically instead of asking the model to fetch them:

- **`.codex/hooks.json`** wires **`.codex/hooks/attach_rules.py`** to `SessionStart` and to `PreToolUse` on `apply_patch` / `Edit` / `Write`.
- On session start the hook injects every rule with `alwaysApply: true`.
- Before each patch it injects the rules whose `globs` cover the files being touched, at most once per rule per session.
- The hook parses `.mdc` frontmatter directly, so adding, renaming, or removing a rule file needs no change here. It fails open, and a malformed rule never blocks an edit.

Codex requires explicit approval for hooks and tracks them by content hash. Run **`/hooks`** once per clone, and again after editing `attach_rules.py`, otherwise the hook is skipped silently. Project-local hooks also load only when the `.codex/` layer is trusted. **`.codex/rules.md`** keeps the human-readable index of the rule files; the full guide, including how to probe the hook by hand, is **`docs/codex-hooks.md`**.

When working in OpenCode, the project plugin **`.opencode/plugins/project-rules.js`** delivers the same rule set without copying it. It injects `alwaysApply: true` rules into every model request, activates scoped rules when a tool touches a path covered by their `globs`, and preserves the active set through compaction. If a write tool is the first operation to reveal a new scoped rule, the plugin rejects that one call so OpenCode can retry with the rule in its model context. See **`docs/opencode-hooks.md`** and run **`make test-opencode-rules`** after changing the plugin.

ZCode loads its `AGENTS.md` chain the same way and has the same gap, so a parallel hook delivers the Cursor rules there too:

- **`.zcode/config.json`** wires **`.zcode/hooks/attach_rules.py`** to `SessionStart` and to `PreToolUse` on `Edit` / `Write` / `MultiEdit` / `ApplyPatch`, under `hooks.events` with `hooks.enabled: true`.
- The behaviour matches the Codex hook (always-on rules at session start, scoped rules per edit, once per rule per session, fail open). The single difference is how edited paths are recovered: ZCode carries them as JSON fields of `tool_input` (`file_path`, `path`, ...), so this variant walks those fields instead of parsing an `apply_patch` blob.
- Configuration-file hooks need no per-hook approval: a workspace config with `enabled: true` runs unconditionally. The command invokes `python` (not `python3`) because the `python3` name is a no-op Microsoft Store stub on Windows. The full guide, including how to probe the hook by hand and the Windows interpreter note, is **`docs/zcode-hooks.md`**.

## Code Review Rules

Codex code review reads this section and applies it to changed files. Keep entries behavioural and repository-specific; formatting and lint stay in CI, where the pre-commit gate already runs **`make lint`**.

### HTTP surface

- Do not change routes, request or response shapes, or status codes in `external/httpserver/server.go` without updating `external/httpserver/openapi.go` in the same change. The served spec is what `/docs/` and generated clients consume, so drift is a silent API break. Safe path: update both, then reconcile `docs/http-api.md`.

### Build tags

- Do not let a package that builds by default import one that lives behind the `http`, `ui`, `scheduler`, `memory`, or `gateway` tags. Plain `make build` must keep compiling with the lean dependency set. Safe path: put the new code behind the same tag, or invert the dependency into an interface owned by the core package.

### Project-local configuration

- Do not read, merge, or execute project-local configuration (`.coddy/mcp.json`, MCP server definitions, hook scripts) without routing it through `TrustGate` in `internal/mcp`. Opening an untrusted checkout must not by itself grant code execution. Safe path: gate the read on the workspace trust decision and persist the approval through `TrustStore`.

### Embedded UI

- Do not treat a UI change as complete when only `external/ui/src/` moved. The binary serves `go:embed` assets, so unrebuilt sources ship the previous SPA. Safe path: run `make build TAGS="http ui"` and include the regenerated assets in the change.

## HTTP API development flow

When changing behavior for the OpenAI-compatible HTTP gateway or bundled UI:

- Add or update tests first (red), then implement (green). The **happy path** of a feature (and a bug's reproduction) is a Gherkin spec in the repo-root **`features/`** directory, run by a godog harness (e.g. `external/httpserver/bdd_*_test.go`); **edge/error cases go in ordinary unit tests**, not `features/`. See **`.cursor/rules/workflow.mdc`** / **`.claude/rules/workflow.md`**.
- If the external HTTP surface changes, update `external/httpserver/openapi.go` so the served OpenAPI matches handlers in `external/httpserver/server.go`.
- Keep `docs/http-api.md` aligned with the live behavior.
- For UI changes, update sources under **`external/ui/src/`** and rebuild embedded assets via **`make build TAGS="http ui"`** (runs **npm** via **make ui-build**).
- **Every** UI edit in a PR ships with a screenshot of the surface it changed (before/after when the surface already existed) — see step 5 of **`.claude/rules/workflow.md`**. If a surface cannot be captured, say so in the PR instead of omitting it.
- Run full regression `make test`, then `make lint`.

## UI sources (`external/ui/`)

**`DESIGN.md`** is the contract for layout, tokens, and SPA component behavior. After changing **`external/ui/src/`**, rebuild embedded assets with **`make build TAGS="http ui"`** before relying on **`go:embed`**.

The composer exposes **`Mode`** (**`agent`** / **`plan`**) and a separate **`Model`** YAML backend selector (**`metadata.model`**; list rows with **`owned_by`** other than **`coddy`** from **`GET /v1/models`**). Default YAML id comes from **`default_agent_model`**; persisted preference uses cookie **`coddy_llm_model`**. Parallel **`POST /v1/responses`** per session, **Stop** (**cancel** + partial assistant persistence), and transcript merge after **`GET .../messages`** are specified in **`DESIGN.md`** (**Multi-session streaming and Stop**) and **`docs/ui.md`**.

**`MarkdownLineEditor`** (`external/ui/src/ui/markdown/`) is the shared markdown body editor (line gutter, wrap-aware numbering, active-line highlight, content-driven height). Used in the plan document card and scheduler job body. Visual and behaviour contract: **`DESIGN.md`** (**Markdown line editor**, **Plan mode plan document card**); functional checklist: **`docs/ui.md`**.

**UI localization** lives in **`external/ui/src/ui/i18n/`** (**`translate`/`t`**, **`I18nProvider`** + **`useT()`**). **`locales.ts`** is the single registry that connects each supported locale to its picker label and dictionary; English and Russian ship today. Add a locale by adding its dictionary and one registry entry — picker options, locale validation, bootstrap, and parity coverage derive from that registry. When user-facing copy is added or changed on a localized surface, add the key to **every registered dictionary in the same change**; **`messagesParity.test.ts`** enforces key and interpolation-token parity. Counted copy ("N tasks") goes through **`translatePlural`** / **`tp(key, count)`** with one entry per CLDR category (**`key.one`** / **`key.few`** / **`key.many`** / **`key.other`**) so each language declines by number; a locale must carry exactly the categories its own **`Intl.PluralRules`** produces. **`main.tsx`** bootstraps locale (**`?lang=` > cookie > `navigator.language`**) and wraps **`<App/>`** plus the shared confirmation provider in **`<I18nProvider>`**. The language picker is in **Settings → Appearance**, under the theme grid (**`AppearanceLanguagePicker`**); cookie **`coddy_ui_lang`** mirrors **`coddy_ui_theme`** (client-side only, no config save). Appearance, Settings, shared destructive confirmation dialogs, and the conversation surfaces (nav, hero, composer, messages, prompts, plan card, History, scheduler, background tasks, env banner) are translated. Visual contract: **`DESIGN.md`** (**Localization and language**); functional checklist: **`docs/ui.md`**.

## Python samples (`examples/`)

See **`examples/README.md`** for layout (**`examples/httpserver/`**, **`examples/acp/`**, **`examples/shared/`**). Scripts may use a project-local interpreter (`.venv` recommended); follow each script header for prerequisites.

- **`examples/build_coddy.sh`** - runs **`make build TAGS="http scheduler memory"`** (override **`TAGS`** as needed) and prints **`coddy -v`**.
- **`examples/test_acp.sh`** - wrapper that runs **`examples/acp/test_acp.sh`** (all ACP **`acp_*.py`** demos in one pass).
- **`examples/test_httpserver.sh`** - wrapper that runs **`examples/httpserver/test_httpserver.sh`** (temp **`coddy http`**, all HTTP harnesses including **`http_e2e_scheduler_api`** and **`http_e2e_scheduler_agent`**).

Example HTTP scripts that call completion endpoints expect a reachable provider and return non-zero on HTTP errors.
