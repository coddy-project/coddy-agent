# ACP agent as a model

A `type: acp` provider runs a local program that speaks the [Agent Client Protocol](https://agentclientprotocol.com) on stdio - `claude-agent-acp`, `codex-acp`, `opencode acp`, `zcode-acp-server`, another `coddy acp` - and serves it as a model. You pick it in the model selector like any other model, and every surface shows its answer as it streams: the console, the web UI, ACP editors, the Telegram gateway.

The agent behind the model is a whole agent. It brings its own system prompt, its own tools and whatever sandbox it has, and it runs them in the session's workspace. Coddy sends it the conversation, shows what it reports, and asks you when it asks for permission. Coddy's own tools, rules and skills stay out of it: the agent never calls a Coddy tool, so a turn on such a model is one request and one answer.

## Configure

```yaml
providers:
  - name: claude
    type: acp
    acp:
      command: claude-agent-acp
      modes: {plan: plan, ask: plan}
models:
  - model: claude/default
```

`acp.command` is the program, looked up on `PATH`, and `acp.args` its arguments. The model id after the provider name selects one of the agent's own models: `default` keeps the model the agent starts with, and anything else must be a model the agent offers (Settings lists them, see [Settings and the dry run](#settings-and-the-dry-run)). A model id may contain slashes, as `coddy acp` names its models (`coddy/neuraldeep/qwen3.8-27b`).

| Agent | `command` / `args` | Sign-in | `modes.plan` / `modes.ask` |
| --- | --- | --- | --- |
| Claude Code | `claude-agent-acp` | the Claude CLI login | `plan` |
| Codex | `codex-acp` | `auth_method: api-key` with `OPENAI_API_KEY` in `env`, or a ChatGPT login of the Codex CLI | `read-only` |
| OpenCode | `opencode`, `[acp]` | OpenCode's own providers | `plan` |
| ZCode | `zcode-acp-server` (with `process: per_session`) | ZCode's own provider file | `plan` |
| Coddy | `coddy`, `[acp]` | its own `config.yaml` (give it a `CODDY_HOME` of its own in `env`) | `plan` / `ask` |

Another Coddy as the agent reads a `config.yaml` of its own and offers the models listed there. The screenshots on this page run this row.

```yaml
providers:
  - name: coddy
    type: acp
    acp:
      command: coddy
      args: [acp]
      env:
        CODDY_HOME: ~/.coddy-agent
      modes: {plan: plan, ask: ask}
models:
  - model: coddy/default
  - model: coddy/neuraldeep/qwen3.8-27b
```

A leading `~` in the command, an argument, `cwd` or an `env` value is your home folder.

The other keys of the `acp` block:

- `env` - variables added to the environment the agent starts with. The agent inherits Coddy's environment, so an `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` Coddy has may take precedence over the agent's own login; set the variable here, to a value or to an empty string, to choose what the agent sees.
- `cwd` - a fixed folder for the agent and every session it opens. When it is empty, each session opens in the workspace of the Coddy session it serves. `${CWD}` in any value is that workspace, and the agent is then started once per workspace.
- `auth_method` - the id of the authentication method Coddy passes to the ACP `authenticate` call after the agent starts, for an agent that refuses sessions until a client authenticates it. `codex-acp` with an API key needs `api-key`.
- `permissions` - `ask` (default), `allow` or `deny`, see [Permissions](#permissions).
- `modes` - the agent's own mode for each Coddy mode, see [Plan and ask mode](#plan-and-ask-mode).
- `process` - `shared` (default) or `per_session`, see [The agent process](#the-agent-process).
- `idle_timeout_ms` - how long the agent stays up after its last call; 0 means sixty minutes.

On the provider row, `proxy` reaches the agent as its environment: `inherit` (the default) leaves Coddy's proxy variables as they are, `ALL_PROXY` included, `none` removes them, and a proxy URL sets `HTTPS_PROXY` and `HTTP_PROXY`, sets `NO_PROXY` to the loopback addresses and removes `ALL_PROXY`, so the agent's own local traffic stays direct. Whether the agent honours them is up to the agent. `timeout_ms`, when positive, bounds each prompt. `api_base`, `api_key` and `api_key_command` are not read.

## A session on an agent

A Coddy session is bound to one ACP session of the agent. The first turn opens it and sends the conversation so far; every later turn sends only the new message, and the agent keeps its own history, tool results included. Coddy's system prompt is not sent: the agent has its own, and Coddy's describes tools the agent cannot call. The runtime block Coddy adds to every turn (the time, the todo checklist, the memory report) is sent after the message.

The conversation starts over in a new ACP session, with the whole history in its first prompt, when the history no longer continues what the agent holds: after a rewind, an edited message, a compaction, a change of workspace, or when the agent process was restarted. Asking the same thing again after a completed answer (a regenerate) also opens a new session.

What the agent reports while it works:

- its text streams as the answer and its reasoning as reasoning;
- each of its tool calls is a numbered line of the reasoning, `[tool 3] Read README.md`, and `[tool 3 failed] ...` when it fails. These are the agent's tools, so they are never shown as Coddy tool calls;
- an answer the agent ends without any text reads `(the agent finished without a message)`, and one it stops short of an ordinary end says why, for example `(the agent stopped: it reached its output limit)`.

![The agent's answer with its reasoning open: its thought and its two tool calls as numbered lines](../assets/acp-provider/acp-provider-answer-dark-1280.png)

*An answer of an agent: its reasoning holds the agent's own tool calls as numbered lines.*

The model id selects the agent's model through the agent's model option, the session's reasoning level sets the agent's reasoning option when that option offers a value of the same name, and the session's mode selects the agent's mode through `acp.modes`. A value a turn does not name goes back to what the session started with, so a choice of an earlier turn never sticks.

Stop cancels the agent's prompt the way the protocol asks. The agent's work so far stays in the answer, the next message goes on in the same session, and the cancelled prompt is never sent again: a request that would repeat it fails instead, since the agent may already have acted on it. When the agent does not confirm the cancellation within ten seconds, Coddy closes its ACP session (the next message starts a new one from the transcript) and leaves the prompt twenty more seconds to end; after that it stops the agent process, which with `process: shared` also ends the prompts of the other sessions it serves.

## Plan and ask mode

In plan and ask mode a session promises not to write. The agent's tools are not Coddy's, so Coddy keeps that promise by putting the agent into one of its own read-only modes: `acp.modes.plan` and `acp.modes.ask` name it (the table above has the mode of each agent). If the key for the session's mode is not set, the turn is refused with a message naming the key, and `coddy --dry-run` warns about it.

On top of that, every permission request the agent sends in plan or ask mode is refused, whatever `acp.permissions` says. An agent that writes without asking (Claude's `bypassPermissions`, ZCode's `yolo`, or a tool you allowed always earlier) is held only by the mode.

## Permissions

When the agent asks before it runs one of its tools, `acp.permissions` decides the answer:

- `ask` - you answer, in the same prompt Coddy's own tools use, on whatever surface holds the session. It shows `Agent:` and the agent's title for the call, what the agent said about it, and three choices - allow, allow always when the agent offers it, and reject. The web UI labels them Allow, Allow always and Reject; the other surfaces show the names the agent gives them. Under the `bypass` permission mode the request is allowed without asking, and under `accept_edits` an edit is;
- `allow` - every request is allowed once;
- `deny` - every request is refused.

![The permission prompt of an agent's write request in the web UI, with its Allow, Allow always and Reject choices](../assets/acp-provider/acp-provider-permission-dark-1280.png)

*The agent asks to write a file: the web UI shows the same prompt as for Coddy's own tools.*

A request nobody answers is refused, not cancelled, so the agent goes on with its turn. Allow always is the agent's own "always": the agent keeps it on its own side, not Coddy.

## What an agent does not do

An agent answers inside a session only. The calls that are not a session's turn never reach it:

- compaction skips an `acp` model in its chain and needs `compaction.model` set to another model; automatic compaction does not run on an `acp` model at all, since the agent keeps and compacts its own context;
- titles and tags of a new chat use `agent.model` (or, when it is not set, the first configured model); when that is an `acp` model, they use the first model in alphabetical order that is not;
- the goal check needs `supervisor.model` set to another model, and the memory subagent `memory.model`: a turn on an agent runs without a memory report otherwise;
- Improve prompt in the web UI's composer uses the first model in alphabetical order that is not an agent;
- `/v1/chat/completions` and `/v1/responses` with an `acp` model as a direct model answer 400; the agent profiles (`agent`, `plan`, `ask`) work with it;
- a turn a messenger restricts (a sender who is not an admin of the bot) is refused: the restriction holds Coddy's tools, not the agent's.

The token counts the agent reports are kept for accounting; the context ring of a session on an agent does not measure the agent's context.

## The agent process

One agent process serves every session of a provider row (one per workspace when a value of the block names `${CWD}`), each session in an ACP session of its own. It starts on the first call, stays up for `idle_timeout_ms` after the last one, and stops with Coddy (`coddy serve`, `coddy acp`, the console). A process that dies is started again on the next call, and its sessions start over from the transcript.

`process: per_session` gives every Coddy session a process of its own. `zcode-acp-server` needs it for two sessions to answer at once: it runs one prompt at a time per process.

An agent can be another Coddy (`coddy acp`), which can have `acp` rows of its own. Each Coddy passes `CODDY_ACP_DEPTH` one higher to the agent it starts, and a Coddy whose own `CODDY_ACP_DEPTH` is 3 starts no agent, so a chain of agents that start each other ends after three.

The agent may call none of the protocol's file system and terminal methods (`fs/*`, `terminal/*`): Coddy advertises neither capability, and the agent reads, writes and runs commands with its own tools.

## Settings and the dry run

In Settings → LLM providers, a row of type `acp` shows the ACP agent block in place of the API fields.

![Settings, LLM providers: a row of type acp that runs coddy acp with a CODDY_HOME of its own](../assets/acp-provider/acp-provider-settings-dark-1280.png)

*An `acp` row that runs `coddy acp`: the command, its argument and the agent's own `CODDY_HOME`.*

The Models list at the end of the row asks the saved row's agent for its models: save the row first, since the form does not start a program it has not saved. `default` is the model the agent starts with, and the others are the models it offers. As on every provider row, the plus adds `<row>/<id>` to Logical models and the check takes it out again.

![Settings, LLM providers: the Models list of the coddy row with default, neuraldeep/qwen3.8-27b and neuraldeep/gemma-4-31b](../assets/acp-provider/acp-provider-models-dark-1280.png)

*The models `coddy acp` offers. `default` and `neuraldeep/qwen3.8-27b` are in Logical models, the plus adds `neuraldeep/gemma-4-31b`.*

A logical model of the row is a model of the composer's selector like any other, shown by the model's name with the full id in its tooltip.

![The composer's model selector with default and qwen3.8-27b, the two logical models of the coddy row](../assets/acp-provider/acp-provider-model-picker-dark-1280.png)

*The model selector with the two logical models of the `coddy` row.*

`coddy --dry-run` starts each agent, opens a session to read its models, checks that the configured model ids are among them, and warns about a missing `modes.plan` or `modes.ask`.

## Troubleshooting

- `authentication required; set acp.auth_method to one of: ...` - the agent wants a client to authenticate it. Sign the agent in on its own, or set the method it lists (with the key it reads in `env`).
- `acp.modes.ask of provider "..." is not set` (or `acp.modes.plan`) - the session is in that mode; set a read-only mode of the agent for it.
- `the agent's last prompt in this conversation ended without a clear outcome` - a request repeated the last message after a cancelled or failed prompt the agent may have acted on, and Coddy did not send it again. Send a new message.
- `exited` with the agent's stderr - the agent stopped. The next call starts it again; run the command by hand to see why.
- `Session is not active` from ZCode - two sessions asked one ZCode process at once; set `process: per_session`.
