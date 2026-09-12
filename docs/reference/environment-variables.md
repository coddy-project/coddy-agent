# Environment variables

Every variable the `coddy` binary reads, grouped by area, with the file that reads it and the page that explains the feature around it. Precedence is the same everywhere: a command-line flag wins over the environment, and the environment wins over `config.yaml` and the built-in default. Two mechanisms feed the environment before anything below is consulted. `$CODDY_HOME/.env` is read at startup (`internal/config/dotenv.go`) and sets only the variables that are not already set, so the shell, Docker or systemd always win over the file. `${VAR}` references inside `config.yaml` are expanded from the process environment when the file loads (`internal/config/expand.go`); `$$` stands for a literal dollar and `${CWD}` is a session placeholder, not an environment reference. The variables that exist only for the test suite, the example harnesses and CI are listed at the end and are not part of the product surface.

## Paths and configuration

| Variable | Read by | Meaning | Documented in |
|---|---|---|---|
| `CODDY_HOME` | `internal/config/paths.go` | The state directory: `config.yaml`, `sessions/`, `skills/`, provider credentials under `providers/`, trust receipts, `.env`. Default `~/.coddy`; the `--home` flag wins over it. | [Configuration](../getting-started/configuration.md#config-file-location-and-paths) |
| `CODDY_CWD` | `internal/config/paths.go` | The working directory of a session whose client sends none, made absolute at startup. Default: the process working directory; the `--cwd` flag wins. | [Configuration](../getting-started/configuration.md#config-file-location-and-paths) |
| `CODDY_CONFIG` | `internal/config/paths.go` | The configuration file to load. Default `$CODDY_HOME/config.yaml`; the `--config` flag wins. | [Configuration](../getting-started/configuration.md#config-file-location-and-paths) |

`${CODDY_HOME}` inside `config.yaml` is substituted with the resolved home when the file is read (`internal/config/expand.go`), whatever the variable itself holds.

## Providers and keys

A provider's key is resolved in this order: the literal `api_key`, the stdout of `api_key_command`, then the variable `NAME_API_KEY`, where `NAME` is the provider name upper-cased with hyphens turned into underscores (`rpa` becomes `RPA_API_KEY`, `my-lab` becomes `MY_LAB_API_KEY`). The name is built by `config.ProviderAPIKeyEnvVarName` in `internal/config/providers.go`, and every surface that reports where a credential comes from (`coddy providers list`, the NeuralDeep sign-in over HTTP, the usage cache) calls the same function.

| Variable | Read by | Meaning | Documented in |
|---|---|---|---|
| `NAME_API_KEY` | `internal/config/providers.go` (`EffectiveAPIKey`) | Fallback key for the provider `name` when `api_key` and `api_key_command` yield nothing. Read when a request is built, not when the file loads. | [Configuration](../getting-started/configuration.md#full-configuration-schema) |
| `OPENAI_API_KEY` | `internal/config/config.go` | Besides the `NAME_API_KEY` rule for a provider named `openai`: when the file configures no `providers` and no `models` at all, the loader synthesises an `openai` provider with the model `openai/gpt-5.4` from this key. | [Configuration](../getting-started/configuration.md#full-configuration-schema), [Docker](../getting-started/docker.md#docker-compose) |
| `ANTHROPIC_API_KEY` | `internal/config/config.go` | The same bootstrap for an `anthropic` provider with `anthropic/claude-sonnet-4-6`, tried when `OPENAI_API_KEY` is empty. | [Configuration](../getting-started/configuration.md#full-configuration-schema) |
| `TELEGRAM_BOT_TOKEN` | `internal/config/gateway.go` (`EffectiveToken`) | The bot token when `gateways.telegram.token` is empty. | [Telegram gateway](../surfaces/gateway.md#quick-start-telegram) |
| `CODEX_HOME` | `internal/llm/codex_auth.go` | The Codex CLI state directory whose `auth.json` a `type: codex` provider falls back to when no Coddy-managed login exists. Default `~/.codex`. | [Configuration](../getting-started/configuration.md#config-file-location-and-paths) |
| `CODDY_CODEX_BASE_URL` | `internal/llm/codex_auth.go` | Overrides the Codex backend endpoint for the whole process; `api_base` is ignored for `type: codex` on purpose, so a configuration file can never redirect an OAuth token. | [Configuration](../getting-started/configuration.md#config-file-location-and-paths) |
| `CODDY_NEURALDEEP_HUB_URL` | `internal/llm/neuraldeep_auth.go` | The hub that issues keys during `coddy providers login neuraldeep` and answers the usage panel, overriding the deployment derived from `api_base`. | [Configuration](../getting-started/configuration.md) |
| `CODDY_NEURALDEEP_BASE_URL` | `internal/llm/neuraldeep_auth.go` | The API base every NeuralDeep request goes to, overriding `api_base`. Meant for stands and tests. | [Console](../surfaces/console.md#testing) |
| `SSH_AUTH_SOCK` | `internal/tools/ssh/agent.go` | The SSH agent socket `ssh_run_command` tries before key files; unset or unreachable means key files only. | [Configuration](../getting-started/configuration.md#ssh-remote-execution) |
| `BROWSER`, `NO_BROWSER` | `internal/platform/browser.go` | Whether `coddy providers login` may open a browser, and with what. `BROWSER` names the opener (`none` opts out; a `%` template or a `:` list is ignored and the openers on `PATH` are tried instead); a set `NO_BROWSER` opts out. On Linux `DISPLAY` or `WAYLAND_DISPLAY` must be set, and an SSH session (`SSH_CONNECTION`, `SSH_TTY` or `SSH_CLIENT` set) counts as having no browser on every platform. | [Security and trust](../operate/security.md#secrets) |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` | `internal/netx/dial.go` (Go's `http.ProxyFromEnvironment`) | The proxy for the HTTP transports `internal/netx` builds when the section they serve sets no `proxy` of its own; an explicit `proxy` replaces them. | [Swarm](../operate/swarm.md#encryption-and-proxies) |

## Remote, HTTP and swarm

| Variable | Read by | Meaning | Documented in |
|---|---|---|---|
| `CODDY_HTTP_TOKEN` | `cmd/coddy/serve.go` | The bearer token `coddy serve` requires on `/v1/*` and `/coddy/*` when `--auth-token` is absent; `httpserver.auth_token` comes after both. A token from the flag or the environment survives a hot reload without being written to the file. | [Remote mode](../operate/remote.md#the-token), [HTTP API](../reference/http-api.md#authentication-optional) |
| `CODDY_REMOTE_TOKEN` | `internal/remote/resolve.go` | The token the console and `coddy acp` send with `--remote` when `--remote-token` is absent. Never read from `config.yaml`. | [Remote mode](../operate/remote.md#the-token) |
| `CODDY_SWARM_TOKEN` | `external/swarm/serve.go`, read in `cmd/coddy/serve.go` | The bearer token clients present to the relay when `--swarm-auth-token` is absent; `swarm.auth_token` comes after both. | [Swarm](../operate/swarm.md#security), [config.yaml reference](../reference/config.md#related-environment-variables) |
| `CODDY_SWARM_PAIRING_TOKEN` | `external/swarm/serve.go`, read in `cmd/coddy/serve.go` | The credential a node presents to register when `--swarm-pairing-token` is absent; appended to `swarm.pairing_tokens` on every reload. | [Swarm](../operate/swarm.md#security), [config.yaml reference](../reference/config.md#related-environment-variables) |
| `CODDY_TELEGRAM_API_BASE` | `internal/dryrun/network.go` | The Bot API origin the `--dry-run` Telegram probe talks to, for stand-ins and self-hosted gateways. | [Configuration](../getting-started/configuration.md#dry-run-probing-what-the-file-points-at) |
| `CODDY_SERVE_ROLE` | `internal/serve/daemon.go` | Set by `coddy serve --daemon` on the processes it re-executes, to `dispatcher` or `worker`, so each half knows which one it is. Not meant to be set by hand. | [coddy serve and the daemon](../operate/serve.md#in-the-background) |

## Updates

| Variable | Read by | Meaning | Documented in |
|---|---|---|---|
| `CODDY_UPDATE_NOTES` | `internal/update/notes.go` | `0`, `false`, `no` or `off` keeps `coddy update` from printing the releases between the old and the new version, the same effect as `--no-notes`. | [Update](../getting-started/update.md#what-changed) |

## Variables passed to hooks

A hook process inherits Coddy's whole environment and gets these on top (`internal/hooks/runner.go`). They are set for the hook, never read by Coddy.

| Variable | Read by | Meaning | Documented in |
|---|---|---|---|
| `CODDY_PROJECT_DIR`, `CLAUDE_PROJECT_DIR` | the hook process | The session working directory, under both names so a Claude Code hook works unchanged. | [Hooks](../features/hooks.md#what-a-hook-receives) |
| `CODDY_SESSION_ID` | the hook process | The session the event belongs to. | [Hooks](../features/hooks.md#what-a-hook-receives) |
| `CODDY_HOOK_EVENT` | the hook process | The event name, such as `PreToolUse`. | [Hooks](../features/hooks.md#what-a-hook-receives) |
| `CODDY_HOME` | the hook process | The resolved state directory, when known. | [Hooks](../features/hooks.md#what-a-hook-receives) |

MCP servers started over stdio get Coddy's environment plus the `env` map of their definition (`internal/mcp/client.go`), whose values went through the same `${VAR}` expansion as the rest of the configuration.

## Console and terminal

| Variable | Read by | Meaning | Documented in |
|---|---|---|---|
| `COLORFGBG` | `external/cli/run.go` | With `--theme auto`, a background of `7` or `15` selects the light theme; anything else falls back to dark. | [Console](../surfaces/console.md#flags) |
| `COLORTERM` | `external/cli/theme.go` | `truecolor` or `24bit` switches the console palette to 24-bit colour. | [Console](../surfaces/console.md) |
| `SSH_CONNECTION`, `SSH_TTY` | `external/cli/tui/terminal.go` | A lone Escape is resolved after 100 ms over SSH and 10 ms locally. | [Keyboard](keyboard.md) |
| `COLUMNS` | `internal/skills/commands.go` | The width of the `coddy skills list` table; default 100, values under 40 are ignored. | [Skills](../features/skills.md) |

## Docker and compose

These are read by the compose files and the `Dockerfile`, not by the binary. Inside the container the image sets `CODDY_HOME=/home/user/.coddy`, `CODDY_CWD=/workspace` and `CODDY_CONFIG=/home/user/.coddy.yaml`, and `docker-compose.yml` overrides `CODDY_CONFIG` with the mounted `/home/user/.coddy/config.yaml`.

| Variable | Read by | Meaning | Documented in |
|---|---|---|---|
| `CODDY_IMAGE` | `docker-compose.yml` | The image to run; default `ghcr.io/coddy-project/coddy-agent:latest`. | [Docker](../getting-started/docker.md#docker-compose) |
| `CODDY_COMMAND` | `docker-compose.yml`, `docker-compose.dev.yml` | The command line of the container; default `serve -H 0.0.0.0 -P 12345`. | [Docker](../getting-started/docker.md#docker-compose) |
| `CODDY_HTTP_PORT` | both compose files | The host port published onto the container's 12345. | [Docker](../getting-started/docker.md#docker-compose) |
| `CODDY_CONFIG`, `CODDY_CWD`, `CODDY_HOME` | both compose files | On the host, the paths mounted as the config file, the workspace and the state directory (defaults `./config.yaml`, `./workspace`, `./coddy_home`). | [Docker](../getting-started/docker.md#docker-compose) |
| `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `DEEPSEEK_API_KEY`, `TELEGRAM_BOT_TOKEN` | both compose files | Passed through from the host into the container, empty when unset. | [Docker](../getting-started/docker.md#docker-compose) |
| `CODDY_VERSION`, `CODDY_BUILD_TAGS` | `docker-compose.dev.yml` | The tag and the comma-separated build tags of a locally built image. | [Docker](../getting-started/docker.md#docker-compose) |

## Development, tests and CI

None of these is read by a released binary. They exist for contributors and are listed so a grep of the tree does not turn up an undocumented name.

| Variable | Read by | Meaning | Documented in |
|---|---|---|---|
| `CODDY_HOOK_LINT`, `CODDY_HOOK_TESTS`, `CODDY_HOOK_SKIP` | `scripts/checks.sh`, run by `.githooks/pre-commit` | The commit gate: `CODDY_HOOK_LINT=0` skips the linter, `CODDY_HOOK_TESTS=fast|full|matrix` adds tests, `CODDY_HOOK_SKIP=1` bypasses the gate. | [Agent notes](../../AGENTS.md) |
| `CODDY_UI_BACKEND` | `external/ui/vite.config.ts` | The `coddy serve` origin the Vite dev server proxies `/v1`, `/coddy`, `/swarm`, `/docs` and `/openapi.*` to while working on the SPA. | [Assets index](../assets/INDEX.md) |
| `CODDY_LIVE_MODEL`, `NEURALDEEP_API_KEY` | `internal/agent/live_neuraldeep_test.go`, `internal/agent/live_tool_json_test.go` | Enable the live probes against a NeuralDeep endpoint; the tests are skipped without both. | the file headers |
| `CODDY_LIVE_REPEAT`, `CODDY_LIVE_SCENARIOS`, `CODDY_LIVE_PROMPTS` | `internal/agent/live_tool_json_test.go` | How many times each tool-call scenario runs, which scenarios run, and which prompt arms are compared (`tuned`, `shared`). | the file header, [Operating modes](../features/modes.md#model-tuned-system-prompts) |
| `CODDY_BIN`, `CODDY_CONFIG`, `CODDY_HOME`, `CODDY_CWD`, `SESSION_ROOT`, `SESSION_ID`, `BASE_URL`, `MODEL`, `WORK_DIR`, `CODDY_CHAT_PROFILE`, `NEURALDEEP_API_KEY`, `RPA_API_KEY` | the harness scripts under `examples/` | Which binary, configuration, server and model an end-to-end script drives; each script's docstring lists its own. | [Examples](../../examples/README.md) |
| `GITHUB_TOKEN` | `.github/workflows/*.yaml` | The GitHub Actions token used by the release, image and tag-on-merge jobs. The binary does not read it: `coddy update` talks to the releases API anonymously. | the workflow files |
| `GO_WANT_MCP_HELPER`, `GO_WANT_MCP_MARKER`, `GO_WANT_TRUST_MARKER`, `GO_WANT_CONFIG_RELOAD_MCP`, `CODDY_MCP_MARKER_FILE`, `MCP_HELPER_TOKEN`, `TRUST_MARKER_FILE` | MCP and trust tests in `internal/mcp`, `internal/session` and `external/httpserver` | Make a test binary re-execute itself as a stub MCP server and report where it ran. | the test files |
| `CODDY_TEST_DOTENV_A`, `CODDY_TEST_DOTENV_B`, `CODDY_TEST_DOTENV_C`, `CODDY_EXPAND_TEST`, `CODDY_CHECK_TEST_PORT` | `internal/config` tests | Fixtures for the `.env` loader, the `${VAR}` expansion and the `-t` check. | the test files |
| `CODDY_TEST_HOME`, `CODDY_TEST_WORKSPACE`, `CODDY_TEST_SESSIONS_ROOT`, `CODDY_TEST_SKILLS_DIR`, `CODDY_TEST_PREFERRED_SESSION_ID`, `CODDY_TEST_ACP_HELPER`, `CODDY_TEST_MCP_HELPER`, `CODDY_TEST_HTTP_SETTINGS_MCP_HELPER`, `CODDY_TEST_SLOW_MCP_STARTED`, `CODDY_PROJECT_MCP_STARTED` | the BDD harnesses and stub helpers under `cmd/coddy`, `internal/agent`, `internal/mcp` and `external/httpserver` | Point a test binary that re-executes itself at the temporary home, workspace and session root of the scenario, and let a stub MCP server report that it started. | the test files |
