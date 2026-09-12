# Slash commands

The built-in commands on each surface and how skills become commands. A slash command is one of four things: a client-side command of the console, which never leaves the terminal; a deterministic built-in (`/compact`, `/export`, `/plugin`), which the agent recognises before the prompt becomes a message and runs without a turn of the model; a Telegram bot command, handled by the adapter; or a skill, whose body is prepended to the message for the model. The first three run whatever the session mode is, because they are operator input rather than tool calls.

## The commands

| Command | Surfaces | What it does | Details |
|---|---|---|---|
| `/model [id]` | console; Telegram | Console: opens the model selector, or switches directly when a configured model id follows (`external/cli/slash.go`). Telegram: an inline keyboard over the configured `models`. | [Console](../surfaces/console.md#commands-and-keys), [Telegram gateway](../surfaces/gateway.md#commands) |
| `/mode [agent\|plan\|ask]` | console; Telegram | Console: opens the mode selector, or switches directly when a valid mode follows. Telegram: an inline keyboard. | [Operating modes](../features/modes.md#switching-on-each-surface) |
| `/resume` | console | Picker over the sessions of the current folder; the chosen one replaces the current session. | [Sessions](../features/sessions.md#resuming) |
| `/new` | console | Starts a new session in the same folder. | [Console](../surfaces/console.md#commands-and-keys) |
| `/theme` | console | Selector between the dark and the light palette. | [Console](../surfaces/console.md#flags) |
| `/hotkeys` | console | Prints the key list. | [Keyboard](keyboard.md) |
| `/usage` | console | Forces a fresh read of the active provider's account usage and prints the breakdown; under `--remote` the server's own key is read. | [Console](../surfaces/console.md#commands-and-keys) |
| `/quit`, `/exit` | console | Exits. | [Console](../surfaces/console.md#commands-and-keys) |
| `/compact [instructions]` | console, web UI, ACP editors, `POST /v1/responses` | Summarises the older history and keeps the recent turns verbatim; the words after the command steer the summariser. Listed only while `compaction.enable` is true; a manual compaction is always forced. | [Context compaction](../features/compaction.md#the-compact-command) |
| `/export [md\|html\|json\|jsonl] [path] [--no-tools] [--no-thinking]` | console, web UI, ACP editors, `POST /v1/responses` | Writes the transcript into the session workspace; a directory receives `coddy-export-<timestamp>.<ext>`, a file name sets the format by extension. Under `--remote` the file lands on the server. | [Session export](../features/session-export.md#usage) |
| `/plugin marketplace list\|add\|remove\|sync`, `/plugin install\|remove\|enable\|disable` | console, web UI, ACP editors, `POST /v1/responses` | Manages skill plugins and marketplaces, the chat twin of `coddy plugin`. | [Skills](../features/skills.md#the-plugin-command-cli-and-plugin-in-chat) |
| `/start`, `/help` | Telegram | The greeting and the command list of the bot. | [Telegram gateway](../surfaces/gateway.md#commands) |
| `/context` | Telegram | The context window usage of the chat's session by category. | [Telegram gateway](../surfaces/gateway.md#commands) |
| `/clear` | Telegram | Starts a new session for the chat; the old one stays on disk. | [Telegram gateway](../surfaces/gateway.md#session-lifecycle) |
| `/<skill>` | console, web UI, ACP editors, `POST /v1/responses` | Runs a skill: the full `SKILL.md` body is prepended to the message the model receives for this turn. | [Skills](../features/skills.md#how-skills-are-applied) |

Three boundaries follow from the code:

- the Telegram adapter answers only its six commands and drops any other message that starts with `/` (`external/gateway/telegram/bot.go`), so `/compact`, `/export` and skills are not reachable there;
- a subagent never runs a built-in: a child prompt that starts with `/export` is an ordinary task for the child (`internal/agent/react.go`);
- the console's client-side commands exist only in the console; the web UI and ACP clients have their own model and mode controls.

## Where each surface gets its list

| Surface | Source of the list | Notes |
|---|---|---|
| Console | A fixed client-side list (`model`, `mode`, `resume`, `new`, `theme`, `hotkeys`, `usage`, `quit`) merged with the rows the server advertises through the ACP `available_commands_update` notification (`slashCatalog` in `external/cli/app.go`). | Typing `/` opens the suggestion menu; Enter on a suggestion applies it and submits in one stroke. |
| Web UI | Two groups in the composer: the built-ins from `GET /coddy/commands`, loaded once when the composer mounts, and the skills from `GET /coddy/slash-commands`, paged and filtered by `prefix`, scoped to the session workspace through `X-Coddy-Session-ID`. | Both lists are re-read when the server announces a configuration reload (`event: config_reloaded` on `GET /coddy/events`), so a skill installed by `/plugin` shows up without a page reload. Picking a row inserts the plain `/name` token and nothing else. |
| ACP editors | `available_commands_update` after `session/new` and `session/load`: the built-ins first, then the skills sorted by name; rows carry `name` (without the slash) and `description` only. | The same function, `skills.BuiltinCommands` in `internal/skills/slash.go`, feeds the HTTP endpoint and the notification, so the two never disagree: `compact` only while compaction is enabled, `export` and `plugin` always. |
| Telegram | `start`, `help`, `mode`, `model`, `context` and `clear`, registered with `setMyCommands` at startup. | They appear in the client's command menu; nothing else is offered. |

## How skills become commands

Every skill file that the loader accepts is a command:

- the identifier is derived from the file location by `skills.CanonicalCommandName`: a `SKILL.md` inside a folder takes the folder name (`skills/code-review/SKILL.md` becomes `/code-review`), and a Markdown file at the root of a `skills.dirs` entry takes its stem (`skills/deploy.md` becomes `/deploy`);
- the `name` field of the frontmatter sets the skill's display name, while the command identifier stays the path-derived one;
- when two files map to the same identifier, the first one in `skills.dirs` order wins (`skills.ListSkills`), and a skill disabled in the configuration is not loaded at all;
- the description shown next to the command is the frontmatter `description`, or the first plain line of the body when there is none, capped at 160 characters.

The catalog reaches the model too: the system prompt carries a `## Slash commands` block listing every `/name` with its description (`skills.BuildSlashCatalogMarkdown`), and with `skills.auto_discovery` on, the model can pull a full body itself through the `load_skill` tool ([Tools](tools.md)).

## How a command in a prompt is parsed

The three built-ins are recognised on the whole prompt (`parseCompactCommand`, `parsePluginCommand` and `parseExportCommand` in `internal/agent`):

- after trimming, the text must be exactly `/compact`, `/plugin` or `/export`, or start with one of them followed by whitespace; a built-in in the middle of a sentence is not a command;
- the words after `/compact` are the summariser's instructions;
- the words after `/plugin` are split into the subcommand and its arguments;
- after `/export`, `--no-tools` and `--no-thinking` may appear anywhere, the first remaining word is the format when it names one, and the rest joined by single spaces is the target path;
- the command text is persisted as a user row, so the transcript shows it, and the outcome is stored as an assistant message.

Skills are found by `skills.ParseInvokedCommandNames` over the whole message, line by line:

- lines inside fenced code blocks and lines starting with `>` are skipped;
- on the remaining lines, the regular expression `invokedMidLineSlashRE`, `(?:^|[\t ])\/([a-zA-Z0-9][a-zA-Z0-9_-]*)`, takes every `/name` that stands at the start of the line or right after a space or a tab, so `/review` in the middle of a sentence counts, while `x/foo`, `path/to/file` and `https://` do not;
- the picker form of older SPA drafts, `[/name](coddy-skill:name)`, is accepted as well when both names agree;
- each name is kept once, in order of appearance, and a name with no matching skill is left alone as plain text, which is why a stray `/tmp` in a prompt is harmless;
- at LLM call time the bodies of the matched skills are prepended to the last user message (`augmentUserMessageWithInvokedSkills` in `internal/agent/react.go`); the stored history is not changed and the transcript does not show the injected text.

The console's client-side commands are dispatched before any of that: the first whitespace-separated field of the draft, with the leading `/` removed, is matched against the fixed list, and an unknown name falls through to the agent as an ordinary prompt (`dispatchSlash` in `external/cli/slash.go`).
