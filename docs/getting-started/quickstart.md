# Quickstart

This page takes a fresh machine to a first answer: install the binary, give it one model, check the file, then talk to the agent from a terminal, from a browser and from an editor. Every other page assumes these steps were taken. Budget five minutes and one API key.

## 1. Install

One line per operating system; the [install guide](install.md) has the Linux packages, the Homebrew cask, the installer options and manual placement.

**Linux / macOS**

```bash
curl -fsSL https://coddy.dev/install.sh | bash
```

**Windows (PowerShell)**

```powershell
irm https://coddy.dev/install.ps1 | iex
```

The installer puts `coddy` on `PATH` (`~/.local/bin` on Unix, `%LOCALAPPDATA%\Programs\coddy` on Windows) and creates `~/.coddy/config.yaml` from the release `config.example.yaml` when the file is missing. The terminal that ran the installer has not re-read its profile, so open a new one and check:

```bash
coddy -v
```

Debian, Ubuntu, Fedora and their relatives can take the `.deb` or `.rpm` instead, and a container runs the same binary as `coddy serve` ([Docker](docker.md)). If the command is not found, see [Troubleshooting](troubleshooting.md#the-binary-is-not-found-after-the-install).

## 2. Give it a model

Everything Coddy needs to know lives in `~/.coddy/config.yaml` (`%USERPROFILE%\.coddy\config.yaml` on Windows). The file the installer created is a copy of `config.example.yaml`, with every section explained in comments; the minimum for an OpenAI-compatible provider is three blocks:

```yaml
providers:
  - name: openai
    type: openai
    api_key: "${OPENAI_API_KEY}"

models:
  - model: "openai/gpt-5.6-terra"
    max_tokens: 8192
    reasoning_default: medium

agent:
  model: "openai/gpt-5.6-terra"
  max_turns: 30
  max_tokens_per_turn: 200000
```

Three rules hold the blocks together. A provider's `name` becomes the prefix of every model id, so `models[].model` is `<provider name>/<model id as the API knows it>`. `agent.model` has to be one of the `models` entries. And `api_key` is either the secret itself, a `${VAR}` reference expanded when the file loads, or empty, in which case `OPENAI_API_KEY` (the provider name in upper case plus `_API_KEY`) is read at call time.

Put the key where the reference finds it, in the shell or in `~/.coddy/.env`, which is read before the file is parsed and never overrides a variable the shell already set:

```bash
export OPENAI_API_KEY="sk-..."
# or, once and for all:
echo 'OPENAI_API_KEY=sk-...' >> ~/.coddy/.env
```

Any OpenAI-compatible server works with `type: openai` and an `api_base` that includes `/v1`; a local Ollama or llama.cpp needs no real key:

```yaml
providers:
  - name: local
    type: openai
    api_base: "http://localhost:11434/v1"
    api_key: "~"
```

Anthropic, NeuralDeep (`coddy providers login neuraldeep` instead of a key) and Codex (ChatGPT OAuth) are covered in [Configuration](configuration.md).

## 3. Check the file

Two flags tell you whether the file is right before anything starts, and every command that loads the file takes them:

```bash
coddy -t          # the file against the schema and the loader's rules
coddy --dry-run   # the same, then the world it points at
```

`-t` prints `<path>: valid` or every problem as `file:line:column: what is wrong` with a `fix:` line under it, and exits 1 on errors. `--dry-run` goes on to ask each provider for its model list, which exercises the address and the credential in one request, and checks every `models[]` entry against that list; a clean setup answers with one line, `dry run: 0 errors, 0 warnings, N ok`. Details: [Checking the file](configuration.md#checking-the-file-from-the-command-line) and [Dry run](configuration.md#dry-run-probing-what-the-file-points-at).

## 4. The console

In a project directory, run the bare command:

```bash
cd ~/src/my-project
coddy
```

That opens the interactive console: type a request, press Enter, and watch the tool boxes, the thinking block and the answer stream in. Under the default permission mode the agent asks before it runs a command or writes a file. Escape interrupts a turn, `/quit` (or Ctrl-C twice) leaves, and `coddy -c` reopens the latest session of this folder.

![The console after a finished turn](../assets/screenshot-console-chat.png)

*A finished turn in the console: the `read` tool box, the thinking block, the answer and the footer counters.*

The same agent runs without a terminal, for a script or a cron job:

```bash
coddy -p "Explain in three sentences how tasks are saved in this repository"
coddy --mode ask -p "Which files read config.yaml?"
```

`-p` streams the answer to stdout, sends diagnostics to stderr, exits non-zero on an error and persists the turn as a normal session; `--mode ask` keeps it read-only. Everything the console can do is in [Console (TUI)](../surfaces/console.md).

## 5. The browser

```bash
coddy serve
```

```text
coddy serve 1.1.1
  httpserver  http://127.0.0.1:12345  (no auth)
```

Open **http://127.0.0.1:12345/**. The empty screen is a new chat: pick a mode and a model in the composer, type, send. Sessions live under the same `~/.coddy` whichever surface created them, so the console and the browser share one history. Settings live at `#/settings`, the OpenAI-compatible API at `/v1/*` and its Swagger UI at `/docs/`.

![A session in the web UI](../assets/screenshot-fullhd-chat.png)

*A session in the web UI, with an expanded `edit` tool call showing the diff it applied.*

The server binds loopback on purpose. To reach it from another machine, bind wider and require a token: `coddy serve -H 0.0.0.0 --auth-token <secret>` (or `httpserver.host` and `httpserver.auth_token` in the file). `coddy serve --daemon` keeps it running in the background and `coddy serve status | stop | restart` control it; see [coddy serve and the daemon](../operate/serve.md).

## 6. An editor over ACP

`coddy acp` speaks the Agent Client Protocol over stdio, which is what Zed, VS Code, Obsidian and scripts use. For Zed, add one entry to `settings.json` with the absolute path of the binary (the one `which coddy` prints; editors do not always inherit your `PATH`):

```json
"agent_servers": {
  "Coddy": {
    "type": "custom",
    "command": "/home/you/.local/bin/coddy",
    "args": ["acp"]
  }
}
```

Then pick **Coddy** under *External Agents* in the agent panel's new-thread menu. Coddy's modes (`agent`, `plan`, `ask`), its configured models and its permission policy appear in Zed's composer, and its skills show up as slash commands. The sessions are again the ones in `~/.coddy`. VS Code, other ACP clients and the reference script client are in [Editors](../surfaces/editors.md); the protocol itself is in [ACP protocol](../reference/acp-protocol.md), with runnable examples under `examples/acp/`.

## Where next

- [Configuration](configuration.md) - every provider type, the `.env` file, the environment variables and SSH remote execution;
- [Operating modes](../features/modes.md) - what agent, plan and ask allow and how to switch on each surface;
- [Skills](../features/skills.md), [Rules](../features/rules.md) and [MCP servers](../features/mcp.md) - how a project teaches the agent its conventions and tools;
- [Telegram gateway](../surfaces/gateway.md) - the same sessions from a messenger;
- [Update](update.md) - `coddy update`, and what happens when a package manager owns the binary;
- [Troubleshooting](troubleshooting.md) - when a step above did not go as written.
