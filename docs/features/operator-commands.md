# Operator commands

Some shell commands are refused to the agent: the [unsafe-command check](decisions.md) rejects them, a [toolset](../reference/tools.md) leaves `run_command` out, a hook blocks it. When such a command still has to run, the agent asks you to run it with the `operator_command` tool. You see the exact command, the shell, the machine and the folder, and press **Run** or **Decline**. The command runs in your operator shell, its output streams onto the card, and the output goes back to the agent, which reads it and goes on in the same turn.

The web UI and the console show the request; you cannot edit the command. What you read is byte for byte what the shell gets: the tool refuses a command with control characters, bidirectional overrides or zero-width characters before it is shown.

## The request

The agent calls the tool with the command and the reason it has to ask:

| Argument | Meaning |
|---|---|
| `command` | The shell command, exactly as it will run. |
| `reason` | Why you have to run it: what refused it to the agent, and what the command does. Shown next to the command. |
| `cwd` | Folder to run it in, relative to the session workspace or absolute. Left out, the workspace. |

The card shows the command, the agent's reason, the shell and the executable it resolved to, the host and the folder, with links resolved. Nothing on it takes the focus when it appears, so a key you were pressing for the composer never runs anything.

![The operator command card in the web UI: the folder, the agent's reason, the command and the Run and Decline buttons](../assets/operator-commands/operator-command-waiting-dark-1280.png)

*A command the agent was refused, waiting for you: the shell and the host on the right, nothing editable.*

- **Run** starts the command. Its last lines stream onto the card while it runs.
- **Decline** refuses it. The agent is told, and the same command is refused without asking you again for the rest of the turn.
- **Stop** interrupts a running command the way Ctrl+C does: `SIGINT` to its process group, so a program that cleans up on Ctrl+C gets to. After 3 seconds, or at once on a second press (**Kill**), the whole group is terminated. The turn goes on with what the command printed so far. On Windows there is no interrupt to send, so Stop terminates at once.

![A running operator command: its output streams onto the card, and Stop interrupts it](../assets/operator-commands/operator-command-running-dark-1280.png)

*While the command runs, its last lines stream onto the card; Stop interrupts it.*

The result the agent reads is where the command ran, its exit code and its output, through the output limit of `run_command` (`tools.output_limits.run_command`):

```text
The operator ran the command in bash on build-box, in /srv/app (exit code 0):
...
```

![A finished operator command: the exit code and the output on the card, and the agent's answer that read them](../assets/operator-commands/operator-command-done-dark-1280.png)

*Once the command ended, the card keeps its record and the agent goes on with the output.*

![The same card in the light theme](../assets/operator-commands/operator-command-waiting-light-1280.png)

*The card in the light theme.*

A request waits for you as long as it takes: there is no timeout. The composer's Stop ends the turn and stops the command the same way (Ctrl+C, then the group); the turn waits up to 12 seconds for it to end. A command that outlives the turn keeps its request open, and its card keeps Stop, until it has ended.

## Where it is offered

The agent is offered the tool only where a person can answer it:

| Surface | Operator commands |
|---|---|
| Web UI | yes |
| Console (`coddy`) and `coddy --remote` | yes |
| A streaming client of `POST /v1/responses` | no, unless it declares `metadata.coddy_operator_commands` (see [HTTP API](../reference/http-api.md)) |
| A client of `POST /v1/chat/completions` | no |
| ACP editors, the Telegram and Pachca bots, `coddy -p`, the scheduler, subagents, supervised goal runs | no |

It is offered in Agent and Plan mode, not in Ask mode, which has no shell at all. A `PreToolUse` [hook](hooks.md) can block it like any other tool; a hook matching `Bash` or `run_command` does not match it.

## Where the command runs

The command runs on the machine that owns the session, in the session's folder: for the console that is your machine, for the web UI and `coddy --remote` it is the `coddy serve` host. It runs as the same user as Coddy, with Coddy's environment: it bypasses Coddy's rules, not the operating system's.

Without a multiplexer the command has no terminal and no input; on Windows it has no console either. A program that asks for one fails at once: write `sudo -n`, not `sudo`. The tool's description tells the model so.

## The shell

```yaml
tools:
  operator_command:
    enable: true      # false: the tool is never offered
    shell: ""         # empty: PowerShell on Windows, bash on Linux, macOS and Android
    multiplexer: ""   # macOS, Linux: tmux or screen
```

`shell` is a name on `PATH` (`bash`, `zsh`, `fish`, `sh`, `pwsh`, `powershell`, `cmd`) or a path to the executable (`C:\Program Files\Git\bin\bash.exe`, `/data/data/com.termux/files/usr/bin/zsh`). Empty means PowerShell on Windows (`pwsh`, then Windows PowerShell) and bash elsewhere (`bash`, then `sh`), Termux's on Android. The kind of shell is read off the executable's name: PowerShell gets `-NoProfile -Command`, `cmd` gets its command line verbatim (`/d /s /c "..."`), every other shell `-c`. A shell that is not found is an error, never a fallback to another one: a command written for zsh must not run in sh. `coddy serve --dry-run` reports the shell this host resolves to.

The console's `!!` uses the same shell, so the commands you type by hand and the ones the agent asks you to run go to one place.

## Long runs: tmux or screen

On macOS and Linux, `multiplexer: tmux` or `multiplexer: screen` runs each operator command inside a detached session. The command gets a real terminal, a prompt for a password waits for you, and a long run does not depend on the browser tab or the console that pressed Run. Coddy manages the session: you see and approve only the command, never a `tmux` line, and the card shows the command's own output.

- One multiplexer session per Coddy session, `coddy-<id>`; each command gets a window of its own. tmux runs on a server of Coddy's own (`tmux -L coddy`, no `tmux.conf`), so your own tmux sessions are never touched.
- The card shows how to reach the terminal, for example `tmux -L coddy attach -t '=coddy-1a2b3c4d5e6f:=oc-0a1b2c3d4e5f'` (screen: `screen -r coddy-1a2b3c4d5e6f -p oc-0a1b2c3d4e5f`), the command's own window. Attach to type a password or answer a prompt; the output keeps streaming to the card.
- Stop sends Ctrl+C to the window, then terminates its process group.
- The window gets the environment the multiplexer server was started with: Coddy's, at the first run.
- Under the systemd user unit (`coddy serve install`) the server starts in a scope of its own, so restarting the service does not end a long run; without `systemd-run` it does.
- Windows has neither: the setting is ignored there, and `--dry-run` warns. macOS ships no tmux; install it (`brew install tmux`) or use `screen`. tmux 3.0 or newer is needed.

## In the console

The console shows the request in a dialog above the composer. The selection starts on **Decline**: an Enter you typed before the dialog appeared never runs anything, and no letter key decides, so a word you were typing does not either. Move to **Run** and press Enter; escape declines.

![The console's operator command dialog: the shell, the host, the folder, the agent's reason and the command, with Decline selected](../assets/operator-commands/operator-command-console-modal-dark.png)

*The console asks before anything runs; the selection starts on Decline.*

While the command runs, its tool box shows the output. Escape stops the command (the turn goes on with its output), a second escape kills it, and only then does escape interrupt the turn.

![The console while the operator command runs: its output streams into the tool's box](../assets/operator-commands/operator-command-console-running-dark.png)

*After Run, the command's output streams into the tool's box.*

The console captures are made with `examples/cli/capture_operator.py`.

## Related

- [Tools](../reference/tools.md) - the `operator_command` row.
- [Console](../surfaces/console.md) - `!!`, the operator shell typed by hand.
- [HTTP API](../reference/http-api.md) - the `operator_command` event and `POST /coddy/sessions/{id}/operator-command`.
- [Configuration reference](../reference/config.md) - `tools.operator_command`.
