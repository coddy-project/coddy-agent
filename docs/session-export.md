# Session export (`/export`)

The built-in `/export` command writes the current conversation to a file inside the session workspace. It runs the way `/compact` and `/plugin` do: deterministically, without a model turn, on every prompt surface (the console TUI, ACP editors, the web UI composer, `POST /v1/responses`). The command follows qwen-code's `/export`; the trimming options follow the export dialog of OpenCode-based tools such as Kilo Code.

## Usage

```text
/export [md|html|json|jsonl] [path] [--no-tools] [--no-thinking]
```

| Command | Result |
|---------|--------|
| `/export` | `coddy-export-<timestamp>.md` in the workspace |
| `/export json` | `coddy-export-<timestamp>.json` in the workspace |
| `/export md chat.md` | `chat.md` |
| `/export chat.html` | `chat.html`; the extension picks the format |
| `/export notes/` | markdown into `notes/`, created when missing |
| `/export md chat.md --no-tools --no-thinking` | the conversation text only |

The reply names the file relative to the workspace and gives its full path. The timestamp is UTC, for example `coddy-export-2026-09-06T12-34-56Z.md`. A generated name is reserved with an exclusive create, so when it is already taken (a second export within the same second, even from another session sharing the workspace) the file gets the next free `-2`, `-3`, ... suffix before the extension. Words after the command are whitespace-separated, so consecutive spaces or tabs inside a path collapse to one space.

### Format

The first word after `/export` is the format when it names one: `md` (or `markdown`), `html`, `json`, `jsonl`. Without it the target's extension decides (`.md`, `.markdown`, `.html`, `.htm`, `.json`, `.jsonl`); an empty target or a directory exports markdown. A bare word that is neither a format nor an existing directory is refused, so `/export yaml` reports the supported formats instead of creating a file called `yaml`.

### Path

- A relative path resolves against the session workspace (the folder shown in the composer's workspace chip or the console footer). An absolute path is accepted when it points inside that workspace.
- An existing directory, a path ending with `/`, or `.` receives the generated file name. Any other path names the file; missing parent directories are created.
- Targets that leave the workspace, through `..` or through a symlink, are refused. Every filesystem step runs through an `os.Root` opened on the workspace: Go refuses a symbolic link that leads outside the root at the time of each operation, so a link that appears between the pre-check and the write is refused as well. Per the `os.Root` contract, filesystem boundaries and bind mounts below the workspace are not checked.
- An explicit name is written to a fresh temporary file that is renamed over the target, so an existing regular file is replaced and never keeps wider permissions. A target that is a directory, a symlink, or any other non-regular file is refused.
- On POSIX systems the file is created with owner-only permissions (`0600`). Windows has no equivalent mode bits: there the file inherits the ACL of its directory.

### Options

- `--no-tools` leaves out tool calls and their results. An assistant row that only called tools disappears with them.
- `--no-thinking` leaves out the model's reasoning.
- Options may appear anywhere after the command. An unknown `--option` is refused with the usage line.

## What the file holds

The export is built before the `/export` row lands in the transcript, so the file holds the conversation up to the command. The command and its reply are appended afterwards like every other built-in; export again to include them.

**Header.** Session id, title (pinned, or derived from the first prompt), workspace path, git branch when the workspace is a checkout, model, time of the first message, export time, message and user-turn counts, and the token totals from the session's `stats.json` when present.

**Entries**, in transcript order:

| Type | Content |
|------|---------|
| `user` | The prompt text, verbatim. Hydrated `@` mentions (`<coddy_attachment>` blocks) and uploaded files are reduced to an attachment list of paths and names; their bodies are not copied, and the gap they leave is closed. |
| `assistant` | Reasoning, text, and tool calls paired with their results by `tool_call_id`. A row with no text, reasoning, or calls (a cancelled turn) is skipped. |
| `tool_result` | A result whose call is not in the transcript. |
| `compaction_summary` | A summary row inserted by `/compact` or automatic compaction. |
| `plan_document` | A plan document row with its slug, name, and file path. |

## Formats

**Markdown** (`md`): a header list, then `## User` and `## Assistant · <model> · <time>` sections. Reasoning sits in a collapsed `<details>` block, each tool call becomes `### Tool call: <name>` with fenced input (pretty-printed JSON) and result. Fences grow when the content itself holds backticks, so code in tool output stays intact.

**HTML** (`html`): one self-contained page with inline CSS, light and dark palettes through `prefers-color-scheme`, no scripts. Every value is escaped and text is shown in pre-wrapped blocks; markdown in assistant replies is kept verbatim rather than rendered.

**JSON** (`json`): one document.

```json
{
  "version": 1,
  "session": {
    "id": "sess_...", "title": "...", "cwd": "/path", "git_branch": "main", "model": "openai/gpt-5.6-terra",
    "started_at": "2026-09-06T10:00:00Z", "exported_at": "2026-09-06T12:00:00Z",
    "message_count": 8, "user_turns": 3,
    "token_usage": {"input_tokens": 1200, "output_tokens": 300, "total_tokens": 1500}
  },
  "entries": [
    {"type": "user", "created_at": "...", "text": "...", "attachments": [{"path": "README.md", "name": "README.md"}]},
    {"type": "assistant", "created_at": "...", "model": "...", "reasoning": "...", "text": "...",
     "tool_calls": [{"id": "call_1", "name": "read", "input": {"path": "README.md"}, "result": "..."}]},
    {"type": "compaction_summary", "created_at": "...", "model": "...", "text": "..."},
    {"type": "plan_document", "created_at": "...", "text": "...", "plan": {"slug": "...", "name": "...", "path": "..."}}
  ]
}
```

Empty fields are omitted, with one exception: `tool_calls[].result` is present as `""` when the tool returned nothing and absent when the transcript holds no result for the call (a cancelled or still running turn). `tool_calls[].input` is the call's JSON arguments, or a JSON string when the model produced arguments that are not valid JSON. `tool_result` entries carry `tool_call_id` and `text`.

**JSON Lines** (`jsonl`): the first line is the header, `{"type":"session","version":1,...}` with the same fields as `session` above; every following line is one entry.

## From the shell: `coddy sessions export`

The same export is available without a running agent, the way `coddy plugin` twins `/plugin`:

```bash
coddy sessions export <session-id> [--format md|html|json|jsonl] [--out <path>] [--no-tools] [--no-thinking] [--sessions-dir <path>]
```

- `<session-id>` is a folder id from `coddy sessions list`, or a prefix that matches exactly one stored session; an ambiguous prefix lists the candidates.
- The file lands in the current directory by default. `--out` names a file or a directory anywhere (absolute paths included); missing directories are created. The format follows `--format`, else the `--out` extension, else markdown, with the same rules as the chat command.
- `--no-tools` and `--no-thinking` trim the document exactly like the chat options. `--sessions-dir` points at another sessions root.
- The command prints `Session exported to <format>: <full path> (<n> transcript entries)` and exits non-zero on an unknown session, a bad format, or a write failure. Flags may come before or after the id.
- The document is the same as the chat export: the title, model and git branch come from the stored `session.json` and the workspace it names, the token totals from its `stats.json`.

## Where the command is listed

`skills.BuiltinCommands` publishes `export` next to `compact` and `plugin`, so it reaches the ACP `available_commands_update` catalog, `GET /coddy/commands`, the composer's **Commands** group, and a console in `--remote` mode without further wiring.

## Limitations

- The file lands on the machine that runs the agent. With `coddy serve` on another host, or the console in `--remote` mode, look for it in the server's workspace; the reply prints the full path.
- The Telegram gateway forwards only its own commands (`/clear`, `/help`, `/mode`, `/model`, `/context`, `/start`) and drops other slash commands, so `/export` is unavailable there.
- Subagent sessions never run built-in commands: a child prompt that starts with `/export` is an ordinary task for the child.

## Code

- `internal/session/transcript_export.go`: the document (`BuildExportDocument`), the renderers (`RenderExport`), target resolution (`ResolveExportRequest`, `ResolveExportTarget`), and the guarded write (`WriteExportFile`, `ExportSession`).
- `internal/agent/export_command.go`: parsing and the reply; `Agent.Run` dispatches the command before the ReAct loop.
- `cmd/coddy/sessions.go`: the `coddy sessions export` subcommand (`PrepareExportOutput` maps `--out` onto an output root, `FileStore.ResolveSessionID` accepts a unique id prefix); spec `features/session_export_cli.feature`, harness `cmd/coddy/bdd_sessions_export_test.go`.
- `features/session_export.feature`: the happy-path specification, driven over `POST /v1/responses` by `external/httpserver/bdd_session_export_test.go` (runs under `-tags http`); escapes, symlinks, unknown formats and options are covered by the unit tests next to the code.
