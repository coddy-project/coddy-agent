# Operator commands

Some shell commands are refused to the agent: the [unsafe-command check](decisions.md) rejects them, a [toolset](../reference/tools.md) leaves `run_command` out, a hook blocks it. When such a command still has to run, the agent asks you to run it with the `operator_command` tool. You see the exact command, the shell, the machine and the folder, and press **Run** or **Decline**. The command runs in your operator shell, its output streams onto the card, and the output goes back to the agent, which reads it and goes on in the same turn.

The web UI and the console show the request; you cannot edit the command. What you read is byte for byte what the shell gets: before anything is shown, the tool refuses a command, a folder or a reason that holds control characters other than newline and tab, format characters (bidirectional overrides, zero-width characters), line or paragraph separators, variation selectors or fillers, and a multi-line command for `cmd`, which would run its first line only.

The card fixes the command line, not everything it runs. A script, a Makefile or a `package.json` script the command starts is a file of the workspace, which the agent can write, so read it before you press **Run**. The reason is the agent's own account of the command. zsh (`.zshenv`) and fish (`config.fish`) read startup files even for `-c`, and bash reads `$BASH_ENV` when it is set; PowerShell (`-NoProfile`), `cmd` (`/d`) and `sh` read none. Look-alike letters (a Cyrillic `і` in a host name) and spaces other than the plain one are not refused: the card shows them as they are, but they can read like other text.

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

- **Run** starts the command. Its last lines stream onto the card while it runs. Nothing starts when the turn was cancelled as you pressed Run, or when the folder changed while the card waited (a link swapped into its path): the agent is told why.
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

A request waits for you as long as it takes: there is no timeout. The composer's Stop ends the turn and stops the command the same way (Ctrl+C, then the group); the turn waits up to 12 seconds for it to end. A command that ends in that time is recorded as stopped, with its output and exit code, like one you stopped yourself. A command that outlives the turn keeps its request open, and its card keeps Stop, until it has ended.

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

The command runs on the machine that owns the session, in the session's folder: for the console that is your machine, for the web UI and `coddy --remote` it is the `coddy serve` host. It runs as the same user as Coddy, with Coddy's environment (inside tmux or screen, the multiplexer server's, with the pagers set to `cat`; see [long runs](#long-runs-tmux-or-screen)): it bypasses Coddy's rules, not the operating system's.

## Commands that ask nothing

Interactive input is not a feature of the tool. The agent must always write a command that needs no answer from you: no password, no confirmation, no editor, no pager. The tool's description tells the model so whatever the settings, with the forms to use: `sudo -n`, not `sudo`; `apt-get -y`; `ssh -o BatchMode=yes`; git with `GIT_TERMINAL_PROMPT=0`. A command that cannot run without a password or an answer is not for this tool: the description tells the agent to give it to you in its reply, and you run it yourself. This is an instruction to the model, not a check: nothing tells a command that asks from one that does not before it runs.

What a prompt does to a command depends on where it runs:

- Without a multiplexer the command has no terminal and its input is empty. A program that asks for something usually fails at once with a non-zero exit code, and the agent reads the error. One that retries on an empty answer, or opens a window of its own (an askpass, a pinentry, a credential manager), waits until you press **Stop**. On Windows the command has a console with no window: a program that reads that console waits there, unseen, until Stop.
- In a [tmux or screen](#long-runs-tmux-or-screen) window the command has a terminal, so a prompt does not fail: it waits. There is no timeout, and the turn waits with it until you press **Stop**.

`sudo` has two limits of its own:

- `sudo -n` never asks for a password, so it runs only what needs none: a command sudoers lets your account run without one (`NOPASSWD`), or one a sudo ticket still covers. Anything else fails with "a password is required".
- With sudo's default per-terminal tickets, a `sudo` you ran in your own terminal does not help: the command does not run in yours.

On Windows there is no way to elevate the command and get its output back; a command that needs an administrator is one you run yourself in an administrator terminal.

## The shell

```yaml
tools:
  operator_command:
    enable: true      # false: the tool is never offered
    shell: ""         # empty: PowerShell on Windows, bash on Linux, macOS and Android
    multiplexer: ""   # macOS, Linux: tmux or screen
```

`shell` is a name on `PATH` (`bash`, `zsh`, `fish`, `sh`, `pwsh`, `powershell`, `cmd`) or a path to the executable (`C:\Program Files\Git\bin\bash.exe`, `/data/data/com.termux/files/usr/bin/zsh`). Empty means PowerShell on Windows (`pwsh`, then Windows PowerShell) and bash elsewhere (`bash`, then `sh`), Termux's on Android. The kind of shell is read off the executable's name: PowerShell gets `-NoProfile -Command`, `cmd` gets its command line verbatim (`/d /s /c "..."`), every other shell `-c`. A shell that is not found is an error, never a fallback to another one: a command written for zsh must not run in sh. `coddy serve --dry-run` reports the shell this host resolves to.

The console's `!!` uses the same shell, so the commands you type by hand and the ones the agent asks you to run go to one place. The agent cannot change this section: `config_set` refuses every path under `tools.operator_command`, since a shell the agent chose would run on every Run you press.

In the web UI the section is **Settings → Tools and permissions → Operator commands**: the switch, the shell (empty shows the platform's default) and where the command runs.

![Settings, Tools and permissions, Operator commands: Enabled, Shell with the default bash (PowerShell on Windows), Run inside set to Directly](../assets/operator-commands/operator-command-settings-dark-1280.png)

*The operator commands section of the settings.*

## Long runs: tmux or screen

On macOS and Linux, `multiplexer: tmux` or `multiplexer: screen` runs each operator command inside a detached session. The command gets a real terminal, so a program that refuses to run without one works, and the command lives on in its window if `coddy serve` restarts (see the systemd note below). Quitting the console that ran a command stops it, inside a multiplexer too. The terminal is not a way to answer the command: it still has to [ask nothing](#commands-that-ask-nothing). Coddy manages the session: you see and approve only the command, never a `tmux` line, and the card shows the command's own output.

- One multiplexer session per Coddy session, `coddy-<id>`; each command gets a window of its own. tmux runs on a server of Coddy's own (`tmux -L coddy`, no `tmux.conf`), so your own tmux sessions are never touched. The session stays after its commands end; Coddy does not remove it. End one with `tmux -L coddy kill-session -t coddy-<id>` (or every one with `tmux -L coddy kill-server`), or `screen -S coddy-<id> -X quit`.
- The card shows how to reach the terminal, for example `tmux -L coddy attach -t '=coddy-1a2b3c4d5e6f:=oc-0a1b2c3d4e5f'` (screen: `screen -x coddy-1a2b3c4d5e6f -p oc-0a1b2c3d4e5f`), the command's own window, with the tmux or screen Coddy runs named by its path. Attach, as the user Coddy runs as, to watch the command on its terminal; the output keeps streaming to the card.
- Stop sends Ctrl+C to the window, then terminates its process group.
- The window gets the environment the multiplexer server was started with. tmux runs one `-L coddy` server for every Coddy process of your account, the console and `coddy serve` alike, so that is the environment of the process that started it first; screen starts one session per Coddy session. Under the systemd unit that environment usually has no `DISPLAY` and no `SSH_AUTH_SOCK` of your terminal, and a `PATH` changed later does not reach the window while the server runs. Attaching to tmux copies your terminal's `DISPLAY`, `SSH_AUTH_SOCK` and the like into the session (tmux's `update-environment`), so the windows of later commands get them too.
- On a terminal programs print colours, progress bars and full-screen redraws. The card and the result clean the output up, but what `htop`, `vim` or `less` leave there is of little use to the agent.
- Under the systemd user unit (`coddy serve install`) the server starts in a scope of its own, so restarting the service does not end a long run; without `systemd-run` it does. A restart ends the turn all the same: the command goes on in its window (attach to see it), but its output no longer reaches the agent or the card, and its window and its files in the session's bundle (`operator-commands/` next to the session's history) stay until you close the window.
- Windows has neither: the setting is ignored there, and `--dry-run` warns. macOS ships no tmux; install it (`brew install tmux`) or use `screen`. tmux 3.0 or newer is needed.

If a command stops on a prompt anyway and you attach to answer it, that is outside what the tool supports, with these consequences:

- You answer on the machine of the session, not in the browser: for the web UI and `coddy --remote` that means a terminal on the `coddy serve` host, over SSH for instance.
- Each command has a window, and so a terminal, of its own: with sudo's default per-terminal tickets, sudo asks for the password again for every command.
- Whatever the window shows reaches the agent: the card, the session's history and the request to the model's provider. sudo does not echo a password, but a prompt that echoes what you type puts it there.
- Nobody attaching is not an answer either: the command waits until you press **Stop**, which sends it Ctrl+C.

## In the console

The console shows the request in a dialog above the composer. The selection starts on **Decline**: an Enter you typed before the dialog appeared never runs anything - it declines, and the decline holds for the turn - and no letter key decides, so a word you were typing does not either. Move to **Run** and press Enter; escape declines.

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
