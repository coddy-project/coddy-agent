# Long-term memory

In the LLM sense, memory is whatever reaches the context: the chat history is the short-term part, and it ends with the session. Long-term memory in Coddy is a set of markdown and plain-text notes on disk that a dedicated pass, the memory copilot, consults before the main model answers and updates when you ask it to remember or forget something. The main agent never sees the memory tools; it sees the result of that pass as a block in its system prompt, next to the session's own notes, and the notes outlive any session.

## What the copilot does

When memory is on, every user message triggers one copilot run before the main ReAct agent starts. The run is a small tool-calling loop with its own model and its own instructions (`external/memory/prompts/copilot.md`, embedded in the binary, so changing them means a rebuild). It reads your message and chooses one of two modes, never both in the same turn:

- **Recall** loads context for the main assistant: it searches the notes, lists folders and reads files, then answers with plain facts under `Already on disk` and, optionally, `Not in notes` - no paths, no file names - or exactly `(no memory hits)` when nothing matched. Recall is the default whenever the copilot is unsure, and it retries with English keywords and a folder listing when a first search finds nothing;
- **Persist** updates the notes when you clearly ask to remember, save, forget or delete something, or when writing durable notes from what you said is the evident intent. It reads existing notes first to avoid duplicates, creates a folder before the first save into it, and reports what it verified, saved, skipped or deleted. Keys, tokens and passwords are never written.

If you tell it not to consult the notes for this message, it skips recall and answers with one short line. The final text of the pass is merged into the `{{.Memory}}` slot of the system prompt template (`agent.md`, `plan.md`, `ask.md`) together with the session notes ([Configuration](../getting-started/configuration.md)); it lives for that turn only and is not written to `session.json`. The main ReAct loop does not receive the `coddy_memory_*` tool definitions and cannot call memory as a normal tool.

## When it runs

Two switches: the binary must be built with the `memory` tag (the release binaries and the Docker image carry it; from source, `make build TAGS="... memory ..."`, see [Build from source](../contributing/build.md)) and `memory.enable` must be true in `config.yaml`. With both, the copilot runs on every turn; in agent and plan mode it has the full tool set. In **ask mode** the pass is recall-only: the copilot gets the search, list and read tools, its prompt says that saving is unavailable, and it never creates, saves or deletes a note, so a read-only session cannot change what is stored. Without the tag the memory keys are accepted and nothing runs.

The copilot uses `memory.model` when set, otherwise the session's effective model, through the same retry policy as the main agent; its completions are capped at `copilot_max_tokens` and the loop runs at most the larger of `recall_max_turns` and `persist_max_turns` model rounds. A pass that fails is logged and the main agent answers without memory context.

## The tools

| Tool | Arguments | Recall | Persist |
|---|---|---|---|
| `coddy_memory_search` | `query`, `scope` (`global`, `project`, `both`) | yes | yes |
| `coddy_memory_list` | `path` (`global:` or `project:notes`; one level) | yes | yes |
| `coddy_memory_read` | `path` | yes | yes |
| `coddy_memory_mkdir` | `path` | | yes |
| `coddy_memory_save` | `title`, `body`, `scope`, optional `relative_path` | | yes |
| `coddy_memory_delete` | `path` (a file, or a folder with everything under it; never a root) | | yes |

Paths are `scope:relative/path.md` - `global:preferences.md`, `project:architecture/api.md` - and links inside a note body should use the same form (or a Markdown link with such a target) so they stay unambiguous across the two roots. Search ranks every file under the chosen roots by word overlap between the query and the file's path plus body, returns at most `max_search_hits` snippets of up to 1,200 characters, and is meant as an entry point: the copilot opens the hits with `coddy_memory_read` and follows the links inside. A save without `relative_path` gets a flat file name derived from the title; with it the note lands in that folder.

## Storage layout

| Root | Where | Scope of the notes |
|---|---|---|
| global | `memory.dir`, or `$CODDY_HOME/memory` (`~/.coddy/memory`) when unset; `${CODDY_HOME}` and `~` expand | shared by every session |
| project | `<session cwd>/memory`, not configurable | the workspace the session runs in |

Only `.md` and `.txt` files count. Nested folders are encouraged for thematic grouping, and the copilot calls `coddy_memory_mkdir` before saving into a new branch of the tree. Both roots are plain directories: they can be edited with any editor, and the project root can be committed with the repository or ignored, as the notes deserve.

## Seeing what it did

Each pass streams to the client as it runs. Over ACP that is a `memory_phase` update (`started`, then `completed` with `durationMs` and, when a note was written, `persistSaved`, `persistTitle`, `persistRelativePath` and the saved body, cut at 12,000 characters) and `memory_message_chunk` text deltas; the HTTP stream carries the same as `memory_phase` and `memory_chunk` events ([ACP protocol](../reference/acp-protocol.md), [HTTP API](../reference/http-api.md)). The web UI shows a foldout row in the turn, `memory...` while it runs and `memory` with the duration when done, holding the copilot's text, `Marked saved (<title>).` when a note was written and `No relevant notes matched this turn.` when nothing matched. The console prints a dim `memory: memory...` line, sets its status to `Working with memory`, and streams the copilot text as a dim block that folds with `ctrl+t` like thinking.

The pass is also recorded in `memory_trace.json` next to `messages.json` - mode, duration, the context text, the paths read, what was saved - and `GET /coddy/sessions/{id}/messages` returns those rows as `memoryTurns`, so a reopened transcript shows the memory rows in place; an ACP `session/load` replays them the same way. None of it is part of the transcript sent to the model.

## Browsing the notes over HTTP

A binary built with both `http` and `memory` serves the two roots of a session as a tree under `/coddy/sessions/{id}/memory/*`; a plain `http` build answers `404` there.

| Method | Path | Notes |
|---|---|---|
| GET | `/coddy/sessions/{id}/memory/tree` | without `root`, the roots `global` and `workspace`; with `root` and optional `path`, the `.md` and `.txt` children one level down |
| GET | `/coddy/sessions/{id}/memory/file` | `root` and `path`; UTF-8 content |
| PUT | `/coddy/sessions/{id}/memory/file` | `{"root","path","content"}` |
| POST | `/coddy/sessions/{id}/memory/dir` | `{"root","path"}` creates a folder |
| DELETE | `/coddy/sessions/{id}/memory/file` | `root` and `path`; a file, or a folder recursively; `400` for the root itself |

`workspace` is the project root of that session's cwd. Traversal outside a root is rejected. The session's own notes (`agentMemory` in `session.json`) are not editable here. These routes are the contract written down under [Long term memory](../surfaces/web-ui.md#long-term-memory) in the web UI notes; the memory keys themselves are the **Long-term memory** section of the web UI's Settings.

## Configuration

```yaml
memory:
  enable: false
  model: ""                # models[].model for the copilot only; empty = the session's model
  dir: ""                  # global root; empty = ${CODDY_HOME}/memory
  recall_max_turns: 6
  persist_max_turns: 12
  copilot_max_tokens: 4096
  max_search_hits: 8
```

| Key | Default | Meaning |
|---|---|---|
| `enable` | `false` | run the copilot at all (needs the `memory` build tag) |
| `model` | `""` | pin the copilot to one `models[].model`; the main agent is unaffected |
| `dir` | `""` | the global root |
| `recall_max_turns`, `persist_max_turns` | `6`, `12` | bound the model rounds of a pass; the effective cap is the larger of the two |
| `copilot_max_tokens` | `4096` | completion cap for the copilot's calls |
| `max_search_hits` | `8` | snippets returned by `coddy_memory_search` |

The field table is in the [config.yaml reference](../reference/config.md#memory); `config.example.yaml` carries the same block with comments.

## Cost and latency

Every user turn with memory on adds one copilot run - one model call at least, more when it uses tools - before the main agent starts, so the reply is delayed by that pass and the account is charged for it; latency is bounded by the pass plus the main ReAct loop. The bounds are the round caps and `copilot_max_tokens`; pinning a small, fast model with `memory.model` keeps the cost of recall low while the main agent stays on the model you chose for it.

## Testing

- `examples/acp/acp_e2e_memory.py` drives `build/coddy` over ACP with an isolated `CODDY_HOME`: a pre-seeded global note is recalled and shapes the reply without a file read, a second turn persists a new note that a third turn recalls, and an optional prune step checks that a note the user asked to forget disappears (`--skip-prune`).
- Unit tests: `external/memory/copilot_test.go` (a disabled pass, the recall-only tool set of ask mode, a save the read-only pass refuses), `sequential_chain_test.go` (search, then read along the links), `external/memory/storage/storage_test.go` (search, nested writes, traversal, deletion, link targets), `external/memory/tools/register_test.go` (the tool definitions), `external/httpserver/memory_http_test.go` (traversal over REST).
