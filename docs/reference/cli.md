# CLI reference

Every command, verb and flag of the `coddy` binary, as the binary itself describes them. The synopsis is what `coddy --help` prints; the sections after it are the flag sets of the commands that have one, printed by `coddy <command> --help`. `make docs` regenerates this page from a binary built with the full tag set, and `make docs-check` fails when it no longer matches, so what is documented here is what the release binary answers. The man page and the shell completions under `packaging/` describe the same command set and are kept in step by hand (see [Writing documentation](../contributing/documentation.md)).

Commands that only print a one-line usage (`skills`, `mcp`, `rules`, `agents`, `hooks`) are covered by the synopsis; their guides are [Skills](../features/skills.md), [MCP servers](../features/mcp.md), [Rules](../features/rules.md), [Subagents](../features/subagents.md) and [Hooks](../features/hooks.md). The `providers` flags below belong to `providers login`; the guide is [Configuration](../getting-started/configuration.md), and `sessions` is described in [Sessions](../features/sessions.md). The console's own commands and keys are in [Console](../surfaces/console.md), [Slash commands](slash-commands.md) and [Keyboard](keyboard.md).

## Help screens

<!-- docsgen:cli:start -->
Help screens of a binary built with `-tags=http,ui,scheduler,memory,cli,gateway,swarm`, the set the release binaries carry.

### coddy

```text
Usage:
  coddy (no arguments on a terminal: interactive console, build tag cli)
  coddy -c | --continue (console: continue the latest session here)
  coddy -p | --prompt "..." (console: one-shot prompt, print the answer)
  coddy -h | --help
  coddy -v | --version
  coddy -t | --test-config [--config PATH] [--home DIR] (check config.yaml against
        the schema and the loader's rules, print each problem with its line and
        how to fix it, then exit; cli, acp and serve take the same flag)
  coddy --dry-run [--config PATH] [--home DIR] (check config.yaml and probe what it
        points at: paths, model servers and credentials, MCP commands, the
        Telegram token, and for serve the listen addresses; prints only the
        problems and a status line, add --test-config for the full report;
        exits without starting anything; cli, acp and serve take the same flag)
  coddy cli [flags] (interactive console TUI)
  coddy acp [flags] (Agent Client Protocol)
  coddy serve [flags] (run every subsystem enabled in config.yaml:
        the OpenAI-compatible HTTP API and web UI, the messenger gateway,
        the swarm relay, the cron scheduler)
  coddy serve -d | --daemon (the same, in the background under a dispatcher
        that starts it again if it dies)
  coddy serve status | stop | restart [--home DIR]
  coddy sessions list [flags]
  coddy sessions export <id> [--format md|html|json|jsonl] [--out PATH] [--no-tools] [--no-thinking]
  coddy skills list
  coddy skills enable <name>
  coddy skills disable <name>
  coddy skills add <owner/repo | git-url | marketplace-url>
  coddy skills sync
  coddy skills remove <name>
  coddy plugin marketplace list | add <src> | remove <src> | sync
  coddy plugin install <owner/repo | git-url | marketplace-url>
  coddy plugin remove <name>
  coddy plugin enable <name> | disable <name>
  coddy mcp list | trust <name> | untrust <name> [--cwd DIR]
  coddy providers list | login <name> [--browser] [--no-config] [--api-base URL] | logout <name> [--home DIR]
  coddy rules list [--cwd DIR]
  coddy agents list [--cwd DIR]
  coddy agents trust <name> [--cwd DIR]
  coddy agents untrust <name> [--cwd DIR]
  coddy hooks list [--cwd DIR]
  coddy hooks trust <file> [--cwd DIR]
  coddy hooks untrust <file> [--cwd DIR]
  coddy update [flags]
```

### coddy cli

```text
Usage of cli (interactive console, also the default for bare coddy on a terminal):
  -c	shorthand for --continue
  -config string
    	path to config.yaml (CODDY_CONFIG, else <home>/config.yaml)
  -continue
    	continue the most recent session in this folder
  -cwd string
    	session working directory (CODDY_CWD, default process cwd)
  -dry-run
    	check config.yaml and probe what it points at - paths, model servers and their credentials, listen addresses, MCP commands, the Telegram token - then exit without starting anything; prints only problems and a status line (add --test-config for the full report); exit status 1 when a probe fails
  -home string
    	agent state directory (CODDY_HOME, default ~/.coddy)
  -log-file string
    	log file path (default <home>/logs/cli.log)
  -log-level string
    	log level: a bare level (debug|info|warn|error), or a comma-separated spec with per-component overrides such as info,gateway.telegram=debug (default from config)
  -mcp-project-trust string
    	trust policy for project-local .coddy/mcp.json: ask (approve each declaration), allow (start them automatically), deny (never load them); overrides mcp.project_trust (default "ask")
  -mode string
    	start in this mode: agent|plan|ask
  -model string
    	select a configured model id (provider/model)
  -p string
    	shorthand for --prompt
  -permission-mode string
    	permission mode: ask|accept_edits|bypass
  -plain
    	deterministic rendering for tests: no terminal queries or protocol negotiation
  -prompt string
    	run one prompt non-interactively, print the answer, and exit
  -remote string
    	connect to a remote coddy serve server (configured remote name, host:port, or http(s) URL)
  -remote-token string
    	bearer token for --remote (default from CODDY_REMOTE_TOKEN)
  -resume
    	open the session picker before starting
  -scheduler
    	run the cron scheduler in this process; overrides scheduler.enable (build with -tags scheduler)
  -session-id string
    	reopen or create the session under this id
  -sessions-dir string
    	sessions root (empty uses config sessions.dir or ~/.coddy/sessions)
  -skills-auto-discovery
    	model-driven skill auto-discovery (load_skill tool); pass =false to disable and override config (default true)
  -t	alias of --test-config
  -test-config
    	check config.yaml against the schema and the loader's rules, print every problem with its line and how to fix it, then exit without starting anything
  -theme string
    	color theme: dark|light|auto (default "auto")
```

### coddy acp

```text
Usage of acp:
  -config string
    	path to config.yaml (CODDY_CONFIG, else <home>/config.yaml or legacy search paths)
  -cwd string
    	default session cwd when the client sends an empty cwd (CODDY_CWD, default process cwd)
  -dry-run
    	check config.yaml and probe what it points at - paths, model servers and their credentials, listen addresses, MCP commands, the Telegram token - then exit without starting anything; prints only problems and a status line (add --test-config for the full report); exit status 1 when a probe fails
  -home string
    	agent state directory (CODDY_HOME, default ~/.coddy)
  -log-file string
    	log file path when output includes file (default from config)
  -log-format string
    	text|json (default from config)
  -log-level string
    	log level: a bare level (debug|info|warn|error), or a comma-separated spec with per-component overrides such as info,gateway.telegram=debug (default from config)
  -log-output string
    	stdout|stderr|file|both (default from config)
  -mcp-project-trust string
    	trust policy for project-local .coddy/mcp.json: ask (approve each declaration), allow (start them automatically), deny (never load them); overrides mcp.project_trust (default "ask")
  -remote string
    	serve ACP against a remote coddy serve server (configured remote name, host:port, or http(s) URL)
  -remote-token string
    	bearer token for --remote (default from CODDY_REMOTE_TOKEN)
  -scheduler
    	run the cron scheduler in this process; overrides scheduler.enable (build with -tags scheduler)
  -session-id string
    	if snapshots exist under this id, session/new restores them once (CLI UX); otherwise a new bundle uses this folder name
  -sessions-dir string
    	sessions root (empty uses config sessions.dir or ~/.coddy/sessions)
  -skills-auto-discovery
    	model-driven skill auto-discovery (load_skill tool); pass =false to disable and override config (default true)
  -t	alias of --test-config
  -test-config
    	check config.yaml against the schema and the loader's rules, print every problem with its line and how to fix it, then exit without starting anything
```

### coddy serve

```text
Usage of serve (runs every subsystem enabled in config.yaml):
  -H string
    	bind address for the HTTP API (default httpserver.host, else 127.0.0.1)
  -P string
    	listen port for the HTTP API (default httpserver.port, else 12345)
  -auth-token string
    	bearer token required on /v1/* and /coddy/* (else CODDY_HTTP_TOKEN, else httpserver.auth_token). Empty = no auth
  -config string
    	path to config.yaml (CODDY_CONFIG, else <home>/config.yaml or legacy search paths)
  -cwd string
    	default session cwd when a client omits it (CODDY_CWD, default process cwd)
  -d	alias of --daemon
  -daemon coddy serve status|stop|restart
    	run in the background under a dispatcher that restarts the process if it dies (see coddy serve status|stop|restart)
  -dry-run
    	check config.yaml and probe what it points at - paths, model servers and their credentials, listen addresses, MCP commands, the Telegram token - then exit without starting anything; prints only problems and a status line (add --test-config for the full report); exit status 1 when a probe fails
  -gateway
    	run the messenger gateway; overrides gateways.*.enable
  -home string
    	agent state directory (CODDY_HOME, default ~/.coddy)
  -host string
    	alias of -H
  -http
    	run the HTTP API in this process; overrides httpserver.enable (default true)
  -log-file string
    	log file path when output includes file (default from config)
  -log-format string
    	text|json (default from config)
  -log-level string
    	log level: a bare level (debug|info|warn|error), or a comma-separated spec with per-component overrides such as info,gateway.telegram=debug (default from config)
  -log-output string
    	stdout|stderr|file|both (default from config)
  -mcp-project-trust string
    	trust policy for project-local .coddy/mcp.json: ask (approve each declaration), allow (start them automatically), deny (never load them); overrides mcp.project_trust (default "ask")
  -port string
    	alias of -P
  -scheduler
    	run the cron scheduler; overrides scheduler.enable
  -session-id string
    	optional session id for new sessions (folder name)
  -sessions-dir string
    	sessions root (empty uses config sessions.dir or ~/.coddy/sessions)
  -skills-auto-discovery
    	model-driven skill auto-discovery (load_skill tool); pass =false to disable and override config (default true)
  -swarm
    	run the swarm relay; overrides swarm.enable
  -swarm-allow-insecure
    	permit binding the relay off loopback without a client token
  -swarm-auth-token string
    	bearer token clients must present to the relay (else CODDY_SWARM_TOKEN, else swarm.auth_token)
  -swarm-host string
    	bind address for the swarm relay (default swarm.host, else 0.0.0.0)
  -swarm-pairing-token string
    	credential nodes must present to register (else CODDY_SWARM_PAIRING_TOKEN, else swarm.pairing_tokens)
  -swarm-port string
    	listen port for the swarm relay (default swarm.port, else 12346)
  -t	alias of --test-config
  -test-config
    	check config.yaml against the schema and the loader's rules, print every problem with its line and how to fix it, then exit without starting anything
```

### coddy serve status | stop | restart

```text
Usage of serve status:
  -home string
    	agent state directory (CODDY_HOME, default ~/.coddy)
```

### coddy sessions list

```text
Usage of sessions list:
  -cwd string
    	only list sessions saved with this cwd (absolute)
  -sessions-dir string
    	sessions root (empty uses config sessions.dir or ~/.coddy/sessions)
```

### coddy sessions export

```text
usage: coddy sessions export <session-id> [--format md|html|json|jsonl] [--out <path>] [--no-tools] [--no-thinking] [--sessions-dir <path>]
  -format string
    	export format: md, html, json, jsonl (default: from the --out extension, else md)
  -no-thinking
    	leave out the model's reasoning
  -no-tools
    	leave out tool calls and their results
  -out string
    	output file or directory (default: the current directory)
  -sessions-dir string
    	sessions root (empty uses config sessions.dir or ~/.coddy/sessions)
```

### coddy providers

```text
Usage of providers:
  -api-base string
    	neuraldeep: API endpoint to sign in against, one of https://api.neuraldeep.ru/v1, https://api.neuraldeep.tech/v1 (default: the provider's api_base, else the first)
  -browser
    	neuraldeep: sign in through a loopback browser callback instead of the device flow
  -device
    	neuraldeep: the device flow, which is the default (accepted for compatibility)
  -home string
    	override CODDY_HOME
  -no-config
    	login: do not add the provider and its models to config.yaml after login
flag: help requested
```

### coddy plugin

```text
plugin commands:
  plugin marketplace list                 list configured marketplaces and their status
  plugin marketplace add <owner/repo|url> add a marketplace and fetch its skills
  plugin marketplace remove <source>      remove a marketplace
  plugin marketplace sync                 refresh all marketplaces
  plugin install <owner/repo|url>         install (and update) a marketplace's skills
  plugin remove <name>                    remove an installed skill
  plugin enable <name>                    enable a skill
  plugin disable <name>                   disable a skill
  plugin list                             list installed skills with versions
```

### coddy update

```text
Usage of update:
  -check
    	report whether a newer release exists and exit
  -no-notes
    	do not print what changed after the update (also CODDY_UPDATE_NOTES=0)
  -no-restart
    	Windows only: install the update but do not start Coddy again
  -repo string
    	GitHub repository owner/name for releases (default "coddy-project/coddy-agent")
  -version string
    	install a specific release tag (X.Y.Z) instead of latest
  -y	install without confirmation
  -yes
    	install without confirmation (same as -y)

Downloads release assets from https://github.com/coddy-project/coddy-agent/releases
```
<!-- docsgen:cli:end -->
