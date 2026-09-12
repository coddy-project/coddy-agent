# Troubleshooting

What to check when a step of the [Quickstart](quickstart.md) does not go as written: the binary is not found, the configuration does not load, a provider rejects the key, a port is busy, a surface is missing from the build, a bot or a hook stays silent. Each section names the symptom, the usual cause and the fix, and the last one lists what to collect before asking for help. The commands are the same on every platform unless a section says otherwise.

## The binary is not found after the install

**Symptom.** `coddy: command not found` in the terminal that ran the installer, or an editor reports that it cannot start `coddy acp`.

**Cause.** `install.sh` places the binary in `~/.local/bin` and writes a guarded block into the rc file of your login shell that adds the directory to `PATH`; the terminal you installed from has not re-read that file. On Windows `install.ps1` adds `%LOCALAPPDATA%\Programs\coddy` to the user `PATH`, and again the installing terminal does not see it. Editors and other harnesses may spawn the agent through `cmd /c` or `sh -c` without the user `PATH` at all.

**Fix.** Open a new terminal, or refresh the one you have:

```bash
source ~/.zshrc            # or ~/.bashrc
export PATH="$HOME/.local/bin:$PATH"
coddy -v
```

```powershell
$env:Path = [Environment]::GetEnvironmentVariable("Path","User") + ";" + [Environment]::GetEnvironmentVariable("Path","Machine")
coddy -v
```

Give ACP clients the absolute path of the binary instead of relying on `PATH` (`/home/you/.local/bin/coddy`, `%LOCALAPPDATA%\Programs\coddy\coddy.exe`). When several copies exist, `which coddy` names the one a shell runs; update that one with `coddy update`. `make install` from a clone lands in the same `~/.local/bin` for a non-root user. Details: [Install](install.md).

## The configuration does not load, or `coddy -t` reports errors

**Symptom.** A command exits with `load config: ...`, `coddy -t` prints lines that start with the file path, or a setting you wrote has no effect.

**Cause.** The loader reads `$CODDY_HOME/config.yaml` (`~/.coddy` by default), falls back to `config.yaml` in the current directory when that file is missing, and runs on built-in defaults when neither exists, so a file edited in the wrong place changes nothing. It decodes leniently: an unknown or misspelled key (`enabled` for `enable`) is ignored rather than rejected, and a value of the wrong shape surfaces later as odd behaviour. A file that fails to parse is recovered from `config.yaml.bak` on a normal load; the check below does not do that, so it reports the file on disk.

**Fix.** Run the check the way the failing command would load the file; `--config PATH` and `--home DIR` select it as for a start:

```bash
coddy -t
coddy serve -t
```

Every problem is printed as `file:line:column: what is wrong`, with an indented `fix:` line and, where the schema has one, a `doc:` line. The exit status is 1 on errors; warnings (`yes` for a boolean, a missing `# yaml-language-server:` header) never fail the check, and a missing file is an error. When the file is clean, go one step further:

```bash
coddy --dry-run                 # the problems and one status line
coddy --dry-run --test-config   # the config report and every probe
```

`--dry-run` probes what the file names: the directories, each provider's model list, the executables of stdio MCP servers, the Telegram token, and for `coddy serve` the listen addresses. Report formats and rules: [Configuration](configuration.md#checking-the-file-from-the-command-line).

## The provider rejects the key or the model id

**Symptom.** A turn ends with an authentication error or an unknown-model error from the provider; `coddy --dry-run` reports `providers[<name>]: cannot reach ...` or `models[<id>]: not in the model list of provider <name>`; `coddy providers list` prints a line that starts with `!`.

**Cause.** Three things have to line up. The first segment of every `models[].model` must equal a `providers[].name` (letters, digits, hyphen and underscore, starting with a letter), and `agent.model` must be one of the `models` entries; the loader reports `models[...]: unknown provider` otherwise. The key is resolved in a fixed order: a literal `api_key` or a `${VAR}` reference expanded when the file loads, then the stdout of `api_key_command`, then the `NAME_API_KEY` environment variable, `NAME` being the provider name in upper case with hyphens turned into underscores (`my-proxy` reads `MY_PROXY_API_KEY`). A reference to a variable that is not set expands to nothing, and the request leaves without a credential. A `type: openai` row with no `api_base` talks to OpenAI itself; a local or third-party server needs `api_base` with `/v1`, and the model id must be one that server lists.

**Fix.**

```bash
coddy providers list     # every provider with the credential source requests actually use
coddy --dry-run          # asks each provider for its model list, checks every models[] entry
```

`providers list` warns about an `openai` row aimed at the official endpoint with nothing to present; `--dry-run` names the address it tried and, for a model the server does not list, the ids it does list (a warning, since some servers serve more than they list). Put the key in `~/.coddy/.env` (`OPENAI_API_KEY=sk-...`), which is read before the file and never overrides the shell. A NeuralDeep row whose key was rejected is repaired with `coddy providers login neuraldeep`, a Codex row with `coddy providers login codex`. Field reference: [config.yaml reference](../reference/config.md#providers).

## Port 12345 is busy, or the API is not served

**Symptom.** `coddy serve` exits with a bind error, `coddy serve --dry-run` reports the `httpserver` address together with the line that set it, or `http://127.0.0.1:12345/` refuses the connection while the process runs.

**Cause.** The address comes from `-H` / `-P`, then `httpserver.host` / `httpserver.port`, then `127.0.0.1:12345`. Another process holds the port - often a `coddy serve --daemon` started earlier - or the HTTP API is switched off for this process with `httpserver.enable: false` or `--http=false`. The default bind is loopback, so a browser on another machine is refused by design.

**Fix.**

```bash
coddy serve status            # a daemon already up? then: coddy serve stop
coddy serve --dry-run         # binds each listen address once and releases it
coddy serve -P 12346          # or httpserver.port in the file
```

For access from other machines bind wider and require a token: `coddy serve -H 0.0.0.0 --auth-token <secret>` (also `CODDY_HTTP_TOKEN` or `httpserver.auth_token`); without a token the process warns that it is reachable without authentication. A listen address is the one setting a running process cannot adopt: under `--daemon` the worker restarts on the new address by itself (exit status 75 asks the dispatcher for a replacement), in the foreground restart it yourself. Under `--daemon` a port that is held is reported in the terminal that typed the command and retried until it frees. See [coddy serve and the daemon](../operate/serve.md) and [HTTP API](../reference/http-api.md#cli-flags).

## A surface is missing from the build

**Symptom.** `coddy serve` refuses to start with `<key> is true but this binary has no <surface> support (rebuild with -tags <tag>)`; bare `coddy` prints the usage instead of opening the console and `coddy cli` answers `interactive console is not built in`; `coddy serve` with nothing enabled says `no subsystem is enabled`.

**Cause.** The console, the HTTP API, the web UI, the scheduler, the memory copilot, the messenger gateway and the swarm relay are Go build tags. A plain `make build` (or `go build`, or `go install ...@latest`) produces the lean ACP binary without them. `coddy serve` treats an enabled subsystem the binary cannot run as an error rather than a warning, so a bot that would otherwise be silently offline is refused by name.

**Fix.** Use a release build - the GitHub Release archives, the `.deb` and `.rpm` packages and the Homebrew cask are built with every tag, and the Docker image ships the API, the web UI, the scheduler, the memory copilot, the console and the gateway - or build it yourself:

```bash
make build TAGS="http ui scheduler memory cli gateway swarm"
```

`ui` needs Node.js and npm (the Makefile runs `ui-build` first). `coddy serve --dry-run` reports a surface this binary was not built with without starting anything. Tag reference: [Build from source](../contributing/build.md#build-tags-reference).

## The web UI answers with the API but no page

**Symptom.** `GET /` returns a short plaintext 404 while `/docs/`, `/v1/models` and the rest of the API work.

**Cause.** Either the binary was linked with `http` but without `ui` - the single-page app is embedded at build time from assets that `make ui-build` generates with npm - or `ui.enable: false` runs the server in API-only mode on purpose.

**Fix.** Rebuild with both tags, `make build TAGS="http ui"` at least, with Node.js and npm on `PATH`; check `ui.enable` in the file. After a rebuild a normal browser reload picks up the new assets. Release binaries carry the UI. See [Build from source](../contributing/build.md) and [HTTP API](../reference/http-api.md).

## An MCP server does not start

**Symptom.** The session starts, but a warning names a server as skipped and its tools are missing from the model's list; under **Settings -> MCP servers** the server shows a shield button.

**Cause.** A server declared in `<workspace>/.coddy/mcp.json` arrived with the checkout, so under the default policy `mcp.project_trust: ask` it stays cold until you approve that exact declaration for that workspace; the warning names the server, the workspace, the digest and the command that approves it, and rewriting an approved entry withdraws the approval. Servers from `config.yaml` and `~/.coddy/mcp.json` are not gated, but their command still has to be on `PATH`, and a server marked `disabled: true` (`"disabled": true` in an mcp.json) is not connected. A server that fails to start never blocks the session: it proceeds with a warning.

**Fix.**

```bash
coddy mcp list               # scope, trust state and the command of every merged server
coddy mcp trust <name>       # prints the declaration, then records the receipt
coddy --dry-run              # resolves stdio commands in PATH, contacts remote servers
```

`coddy mcp list` and `trust` take `--cwd DIR` for another workspace. Over HTTP the same decisions are `POST /coddy/mcp/{name}/trust` and the shield in the web UI. `coddy serve --mcp-project-trust allow` (or `coddy acp`) overrides the policy for one process, which is what a CI job uses. Receipts live in `~/.coddy/mcp-trust.json`. Guide: [MCP servers](../features/mcp.md#workspace-trust-for-project-local-servers).

## The Telegram bot is silent

**Symptom.** The bot never answers, or a `/model` or `/mode` tap changes nothing.

**Cause.** In order of frequency: the binary has no gateway tag (a startup error naming the tag); `gateways.telegram.enable` is true but no token could be resolved (the gateway refuses to start and says to set `gateways.telegram.token` or `TELEGRAM_BOT_TOKEN`); the sender is not allowed - `default_access: admins` or `group:<name>` drops everyone else silently, and `admins` must hold your numeric Telegram user id; the message is in a group and the bot was not addressed (in groups it reacts only to an @mention, a reply to its own message, or `/clear`); another process is polling the same bot, and Telegram hands each update to one long poll only.

**Fix.** `coddy --dry-run` checks the token against the Bot API and names the bot. A connected bot logs `telegram bot connected` at `info`. For a dropped update raise that one component:

```bash
coddy serve --log-level "info,gateway.telegram=debug"
```

or the same in the file under `logger.levels`. At `debug` every update is recorded with the reason it was dropped (access denied, an admin-only chat, a group message not addressed to the bot, a full queue). Silence at `warn` and nothing at `debug` means the update never arrived: check the token, the access lists and other pollers. Guide: [Telegram gateway](../surfaces/gateway.md#debugging-a-chat).

## Hooks or subagent definitions are ignored

**Symptom.** A hook in `<workspace>/.coddy/hooks.json` (or the Claude Code `.claude/settings.json`) never runs and the transcript shows a notice that a hooks file is held; `spawn_agent` answers that a definition comes from a project file that is not approved for this workspace; `coddy hooks list` or `coddy agents list` shows `needs_approval`.

**Cause.** Files at or under the session cwd are project scope and follow `hooks.project_trust` and `subagents.project_trust`. Under the default `ask` they are parsed and listed, but nothing runs or spawns until you approve that exact file for that workspace on the machine running Coddy; the receipt is bound to a digest of the file, so editing it asks again. Your own files (`~/.coddy/hooks.json`, `${CODDY_HOME}/agents`) never need approval. `hooks.enable: false` or `subagents.enable: false` switches the feature off entirely, only the `hooks` key of a Claude Code settings file is read, and `spawn_agent` is never offered in ask mode.

**Fix.**

```bash
coddy hooks list [--cwd DIR]
coddy hooks trust <file>        # e.g. .coddy/hooks.json
coddy agents list [--cwd DIR]
coddy agents trust <name>
```

Both `trust` commands print what they are about to approve and record the receipt (`~/.coddy/hooks-trust.json`, `~/.coddy/subagents-trust.json`). From a remote console or an ACP client the approval belongs on the server: `POST /coddy/hooks/trust` and `POST /coddy/subagents/{name}/trust` with the session workspace as `cwd`. A checkout you already trust can run under `project_trust: allow`. Guides: [Hooks](../features/hooks.md#project-files-and-trust), [Subagents](../features/subagents.md#scopes-and-project-trust).

## A turn stops with a usage limit

**Symptom.** The turn ends with the provider's limit error; the console footer reads `limit reached (resets 20:59)` and the transcript says `Usage limit reached`.

**Cause.** A `429` that names a reset further away than the retry budget covers ends the turn by default. `agent.wait_for_limit_reset` is off because a waiting turn keeps the turn lock and the client stream open the whole time.

**Fix.** `/usage` in the console prints the account breakdown: every window with its percent and reset time, the cooldown, the wallet. Wait for the reset or switch models with `/model`. To have the turn wait by itself, set:

```yaml
agent:
  wait_for_limit_reset: true
  # wait_for_limit_reset_max_ms: 14400000   # at most 4 h per turn, the default
```

A top-level turn then waits for the reset, reports `Usage limit reached · resuming at 20:59` every 20 seconds, and re-issues the same call; Escape stops the wait like any turn. A key the hub rejects reads `key rejected` in the footer and is repaired with `coddy providers login neuraldeep`. See [Console (TUI)](../surfaces/console.md) and the [`agent` reference](../reference/config.md#agent).

## `coddy update` refuses to overwrite a packaged binary

**Symptom.** `coddy update` changes nothing, names the package that owns the installation, prints the package manager command and exits 1; or it points at `brew upgrade`.

**Cause.** A `coddy` that `apt`, `dnf` or Homebrew put on disk is tracked by that tool's database. Overwriting `/usr/bin/coddy` in place would leave the database describing a build that is gone, and the next `apt upgrade` would quietly put the old version back. `coddy update` asks `dpkg-query -S` and `rpm -qf` who owns the file, and recognises a `Caskroom` or `Cellar` path as Homebrew's.

**Fix.**

```bash
sudo coddy update -y                                    # fetches the .deb or .rpm, verifies it, hands it to the package manager
sudo apt-get install ./coddy_<newer>_linux_amd64.deb    # or: sudo dnf install ./coddy_<newer>_linux_amd64.rpm
brew upgrade --cask coddy                               # the cask; a formula install takes: brew upgrade coddy
```

A copy under `~/.local/bin` or a build of your own is untouched by any of this and keeps updating itself. See [Update](update.md#installations-owned-by-a-package-manager).

## Windows notes

- **Paths.** The binary is `%LOCALAPPDATA%\Programs\coddy\coddy.exe`; config and sessions live under `%USERPROFILE%\.coddy\`. Use `$env:USERPROFILE`, not `$HOME`, which differs between Windows PowerShell and Git Bash.
- **Shell.** `run_command`, `api_key_command` and hooks run through `pwsh`, then Windows PowerShell, then `cmd.exe`, in that order of preference; Unix builds pick `bash`, then `sh`. Write commands for PowerShell.
- **`make`.** The Makefile needs a Unix-like shell: run it from Git Bash (or WSL, MSYS2), not from `cmd` or PowerShell. The `ui` tag also needs Node.js and npm on `PATH`.
- **`npm --prefix`.** When `make ui-build` fails with `npm error enoent ... open '...\package.json'`, the npm on the machine mishandles `--prefix`. Build the UI inside its directory, then let `make` pick up the prebuilt assets:

  ```bash
  (cd external/ui && npm install && npm run build:go)
  make build TAGS="http ui scheduler memory cli gateway swarm"
  ```

- **Updates from scripts.** `coddy update -y --no-restart` installs without starting Coddy again, for a CI step with no console to run in.
- **Editors.** Configure ACP clients with the absolute `coddy.exe` path (see the first section).

## Collect diagnostics

Before asking for help, gather what a reader needs to reproduce the setup without the secrets:

```bash
coddy -v                          # the version (dev when the build embedded none)
coddy -t                          # the config report; api_key and token values are never echoed
coddy --dry-run --test-config     # every probe, the ones that passed included
coddy providers list              # providers and the credential source each one uses
coddy mcp list                    # MCP servers, scope and trust state
coddy hooks list                  # hook files and their trust state
coddy agents list                 # subagent definitions and their trust state
coddy serve status                # the daemon: pid, config, log and worker
```

Logs follow the same knobs on every long-running command: `--log-level` takes a bare level (`debug`, `info`, `warn`, `error`) or a spec that raises one component, such as `info,gateway.telegram=debug`; `coddy acp` and `coddy serve` also take `--log-output stdout|stderr|file|both`, `--log-file` and `--log-format text|json`. The console never logs to the terminal: its records go to `<home>/logs/cli.log` (`--log-file` moves them). A daemon appends to `~/.coddy/logs/serve.log`. For a reproduction over the protocol:

```bash
coddy acp --log-level debug
```

The same settings persist under `logger` in `config.yaml` (`level`, `levels`, `outputs`, `file`, `format`). Reference: [Configuration](configuration.md), [`logger`](../reference/config.md#logger).
