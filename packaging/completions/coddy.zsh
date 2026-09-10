#compdef coddy
#
# zsh completion for coddy
#
# Keep the command list in sync with printUsage() in cmd/coddy/main.go.

_coddy() {
    local -a commands
    commands=(
        'cli:interactive console TUI'
        'acp:Agent Client Protocol server on stdio'
        'serve:run every subsystem enabled in config.yaml'
        'sessions:list or export stored sessions'
        'skills:manage skills'
        'plugin:manage plugins and marketplaces'
        'mcp:list and trust MCP servers'
        'providers:manage provider credentials'
        'rules:list project rules'
        'agents:list and trust subagents'
        'hooks:list and trust lifecycle hooks'
        'update:install the latest release'
    )

    _arguments -C \
        '(-h --help)'{-h,--help}'[print the command list]' \
        '(-v --version)'{-v,--version}'[print the version]' \
        '(-c --continue)'{-c,--continue}'[continue the latest session here]' \
        '(-p --prompt)'{-p,--prompt}'[run one prompt and exit]:prompt:' \
        '--resume[pick a session to resume]' \
        '1: :->command' \
        '*:: :->argument'

    case $state in
        command)
            _describe -t commands 'coddy command' commands
            ;;
        argument)
            case $words[1] in
                sessions) _values 'subcommand' list export ;;
                skills)   _values 'subcommand' list enable disable add sync remove ;;
                plugin)   _values 'subcommand' marketplace install remove enable disable ;;
                mcp|agents|hooks) _values 'subcommand' list trust untrust ;;
                providers) _values 'subcommand' list login logout ;;
                rules)    _values 'subcommand' list ;;
                update)
                    _arguments \
                        '--check[report whether a newer release exists]' \
                        '(-y --yes)'{-y,--yes}'[install without confirmation]' \
                        '--version[install a specific release tag]:tag:' \
                        '--repo[GitHub repository to take releases from]:repo:' \
                        '--no-restart[Windows only: do not start Coddy again]'
                    ;;
                cli|acp)
                    _arguments \
                        '--config[path to config.yaml]:file:_files' \
                        '--home[agent state directory]:directory:_files -/' \
                        '--cwd[default session working directory]:directory:_files -/' \
                        '--log-level[debug|info|warn|error]:level:(debug info warn error)' \
                        '--remote[drive a remote coddy serve server]:remote:' \
                        '--remote-token[bearer token for --remote]:token:'
                    ;;
                serve)
                    _arguments \
                        '--config[path to config.yaml]:file:_files' \
                        '--home[agent state directory]:directory:_files -/' \
                        '--cwd[default session working directory]:directory:_files -/' \
                        '--sessions-dir[sessions root]:directory:_files -/' \
                        '--log-level[debug|info|warn|error]:level:(debug info warn error)' \
                        '-H[bind address for the HTTP API]:host:' \
                        '-P[listen port for the HTTP API]:port:' \
                        '--auth-token[bearer token for the HTTP API]:token:' \
                        '--http[run the HTTP API]' \
                        '--gateway[run the messenger gateway]' \
                        '--swarm[run the swarm relay]' \
                        '--scheduler[run the cron scheduler]' \
                        '--swarm-host[bind address for the relay]:host:' \
                        '--swarm-port[listen port for the relay]:port:' \
                        '--swarm-auth-token[bearer token clients present to the relay]:token:' \
                        '--swarm-pairing-token[credential nodes present to register]:token:' \
                        '--swarm-allow-insecure[bind the relay off loopback without a token]'
                    ;;
            esac
            ;;
    esac
}

_coddy "$@"
