# Coddy documentation

Coddy is a general-purpose agent in one static Go binary: a ReAct loop with filesystem and shell tools, MCP servers, project rules, skills, subagents, hooks, background tasks, a cron scheduler and long-term memory, driven from a terminal console, an embedded web UI with an OpenAI-compatible HTTP API, editors over the Agent Client Protocol, or a Telegram bot. Every surface shares the same sessions under `~/.coddy`.

New here? Read [Quickstart](getting-started/quickstart.md), then the page of the surface you use. This map is generated from [nav.yaml](nav.yaml) by `make docs`; the same list feeds [llms.txt](llms.txt) and [llms-full.txt](llms-full.txt) for agents that read documentation. How the pages are organised and what a change to Coddy must carry into them is in [Writing documentation](contributing/documentation.md).

<!-- docsgen:nav:start -->
## Getting started

Install Coddy, give it a model, run it for the first time and keep it updated.

- [Quickstart](getting-started/quickstart.md) - From a fresh install to the first answer in five minutes, on the console, in the browser and from an editor.
- [Install](getting-started/install.md) - One-line installers, release archives, Linux .deb and .rpm packages, Homebrew, Windows paths, manual placement.
- [Configuration](getting-started/configuration.md) - Where config.yaml lives, how to check it with -t and --dry-run, providers and models, SSH remote execution, the .env file.
- [Update](getting-started/update.md) - coddy update, release assets, installations owned by a package manager, the report of what changed.
- [Docker](getting-started/docker.md) - The GHCR image, docker compose, volumes and environment, the bundled UI on port 12345.
- [Homebrew](getting-started/homebrew.md) - The cask against the formula, which Homebrew repository takes what, the homebrew/core submission.
- [Troubleshooting](getting-started/troubleshooting.md) - What to check when the binary is not on PATH, the config does not load, a provider rejects the key, a port is busy or a surface is missing from the build.
- [Changelog](getting-started/changelog.md) - Release notes of every published version, generated from GitHub Releases.

## Surfaces

The same agent and the same sessions from a terminal, a browser, an editor or a messenger.

- [Console (TUI)](surfaces/console.md) - Bare coddy in a terminal, the layout, keys, slash commands, the local shell, print mode and remote mode.
- [Web UI](surfaces/web-ui.md) - The embedded single-page app served by coddy serve, with sessions, the composer, modes and models, attachments, settings, themes and languages.
- [Editors (ACP)](surfaces/editors.md) - Zed, VS Code, Obsidian and scripts as ACP clients of coddy acp, and what they share with the other surfaces.
- [Telegram gateway](surfaces/gateway.md) - The Telegram bot adapter, setup, access levels, session isolation, rich messages, the same chat live in the browser, writing a new adapter.

## Operate

Running Coddy as a service, reaching it from elsewhere and bounding what it may do.

- [coddy serve and the daemon](operate/serve.md) - One process for every enabled subsystem, --daemon with status, stop and restart, how a configuration change reaches a running process.
- [Remote mode](operate/remote.md) - Driving a remote coddy serve from the console, ACP or the web UI with --remote, tokens, CORS and the environment chip.
- [Swarm](operate/swarm.md) - Relays and nodes, mounts, the aggregated session list, rings and routes, the reverse tunnel.
- [Scheduler](operate/scheduler.md) - Cron job files, UTC firing rules, run sessions, the scheduler tools and REST.
- [Security and trust](operate/security.md) - What the agent may execute and how to bound it, permission modes, project trust for MCP servers, hooks and subagents, tokens and CORS, what is not sandboxed.

## Features

What the agent can do and how each capability is configured.

- [Operating modes](features/modes.md) - agent, plan and ask, which tools each mode allows and how to switch on every surface.
- [Sessions](features/sessions.md) - Session bundles on disk, resuming, branches from an edited message, todo lists, the sessions CLI.
- [Rules and instructions](features/rules.md) - Rules from the .coddy, .agents, .cursor, .claude and .codex folders, AGENTS.md and DESIGN.md of a folder the agent enters, your own pair and rules in the agent home, instruction files, dialects by extension, activation.
- [Skills](features/skills.md) - SKILL.md packs as slash commands, skills.dirs, the registries and the plugin command.
- [Subagents](features/subagents.md) - spawn_agent, definition files, the built-ins, project trust receipts, capability narrowing, child sessions.
- [Hooks](features/hooks.md) - Lifecycle hooks in Claude Code's hooks.json shape, events, matchers, the stdin payload, exit codes and JSON answers, project trust.
- [MCP servers](features/mcp.md) - Connecting MCP servers over stdio, streamable HTTP and SSE, mcp.json files, workspace trust, enable switches, the management API.
- [Background tasks](features/background-tasks.md) - Detached commands, the task pool, timeouts, adoption of long foreground commands, program-wide permission grants.
- [Context compaction](features/compaction.md) - /compact and automatic summarisation at a threshold, the kept recent turns, result eviction with keep_result.
- [Long-term memory](features/memory.md) - The memory copilot, what it recalls and saves, the storage layout, configuration and cost.
- [Session export](features/session-export.md) - /export and coddy sessions export, formats, path rules, trimming options, the JSON document.

## Reference

Complete lists, generated from the code wherever the code is the source of truth.

- [CLI reference](reference/cli.md) - Every command, verb and flag of the coddy binary, generated from --help.
- [config.yaml reference](reference/config.md) - Every field of config.yaml with its type, default and description, generated from the schema, plus the self-configuration tools.
- [Environment variables](reference/environment-variables.md) - Every variable the binary reads, what it overrides and where it is documented.
- [Slash commands](reference/slash-commands.md) - The built-in commands on each surface and how skills become commands.
- [Keyboard](reference/keyboard.md) - The keys of the console TUI and of the web UI composer.
- [Tools](reference/tools.md) - The built-in tools the model can call, their arguments, permissions and the modes that expose them.
- [HTTP API](reference/http-api.md) - The OpenAI-compatible endpoints and the /coddy REST surface, authentication, sessions and headers, the OpenAPI document.
- [ACP protocol](reference/acp-protocol.md) - How coddy acp implements the Agent Client Protocol, methods, notifications, permission and question requests.

## Tutorials

Task-shaped guides, each a complete path from a goal to a working result, with the configs and commands to copy, the check that it worked and what tends to go wrong.

- [Coddy in CI](tutorials/coddy-in-ci.md) - A workflow file that runs coddy -p on every pull request, with the key in a secret and the answer in the job log.
- [A Telegram bot for a team](tutorials/telegram-bot-for-a-team.md) - One bot for a team with coddy serve: the token, who may talk to it, group isolation, and the same chat live in the browser.
- [A project set up for agents](tutorials/project-set-up-for-agents.md) - A repository that teaches any agent how it works: rules, AGENTS.md, skills, an MCP server and the trust receipts.
- [A server driven from a laptop](tutorials/server-driven-from-a-laptop.md) - A coddy serve on a machine with the models and the workspace, driven from a laptop over the console, an editor and the browser.
- [Coddy as a model in VS Code Copilot](tutorials/coddy-as-a-model-in-vs-code.md) - A running coddy serve registered in chatLanguageModels.json, so its models and its agent sit in Copilot's model picker, with sessions per request and the agent's permission gate.
- [A skill of your own](tutorials/a-skill-of-your-own.md) - A SKILL.md that becomes a slash command on every surface, from the first file to a registry install.
- [A relay and its nodes in Docker](tutorials/swarm-relay-and-nodes.md) - A Compose stand with a relay and nodes that dial out to it, the tokens, the checks, a mounted node from the console and the browser, scaling by adding nameless workers.
- [A chain of relays](tutorials/swarm-multi-hop.md) - A second relay behind the first with its own nodes, two-hop mounts, the recursive session list, rings and alternates.
- [Working with remote nodes](tutorials/swarm-remote-nodes.md) - Driving a node behind a relay from the console, an editor and the browser, what runs where, and which credential opens what.

## Contributing

How Coddy is built, tested, documented and released.

- [Contributing guide](../CONTRIBUTING.md) - The development environment, the branch and pull request flow, what every change must carry.
- [Writing documentation](contributing/documentation.md) - Page types, the navigation map, screenshots and videos, the assets index, generated references and the checks that guard them.
- [Architecture](contributing/architecture.md) - System design and component overview, package boundaries, session modes, the directory structure.
- [Build from source](contributing/build.md) - Prerequisites, make build, TAGS against go build -tags, the release binaries and the distribution packages.
- [Custom tools](contributing/custom-tools.md) - Adding a built-in tool to the registry, its schema and permission wiring, with a complete example.
- [ReAct agent](contributing/react-agent.md) - The loop design, the system prompt structure, the tool-calling contract and mode-specific behaviour.
- [Web UI design](../DESIGN.md) - Tokens, layout and component contracts of the embedded SPA.
- [Codex hooks](contributing/codex-hooks.md) - How the Cursor rules reach a Codex CLI session working on this repository.
- [OpenCode hooks](contributing/opencode-hooks.md) - Deterministic delivery of the Cursor rules to OpenCode sessions working on this repository.
- [ZCode hooks](contributing/zcode-hooks.md) - Deterministic delivery of the Cursor rules to ZCode sessions working on this repository.
- [Syntax highlighting audit](contributing/syntax-highlighting-audit.md) - The NeuralDeep audit of code highlighting across the seven themes, its fixtures and how to re-run it.
- [Agent notes](../AGENTS.md) - The repository map and contributor notes for coding agents.

## Design records

Plans and decisions as they were taken. They are not rewritten when the code moves on.

- [Console TUI plan](plans/cli-tui.md) - The design of the pi-style console.
- [File viewer plan](plans/file-viewer.md) - The design of the file viewer in the web UI.
- [Hooks plan](plans/hooks.md) - The design of lifecycle hooks and project trust.
- [NeuralDeep usage plan](plans/neuraldeep-usage.md) - The design of the provider usage panel and the limit wait.
- [Subagents plan](plans/subagents.md) - The design of subagent definitions, trust and child sessions.
- [Session bus plan](plans/session-bus.md) - The design of the per-session event bus, with every surface and every inference executor attached as a client, and the work to get there.
- [Remote control design](plans/remote-control.md) - The remote control and local/remote operation design, phases and resolved questions.
<!-- docsgen:nav:end -->
