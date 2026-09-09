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
        'http:OpenAI-compatible HTTP gateway and web UI'
        'gateway:messenger gateway'
        'sessions:list or export stored sessions'
        'skills:manage skills'
        'plugin:manage plugins and marketplaces'
        'mcp:list and trust MCP servers'
        'codex:manage codex provider credentials'
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
                codex)    _values 'subcommand' login status logout ;;
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
                cli|acp|http|gateway)
                    _arguments \
                        '--config[path to config.yaml]:file:_files' \
                        '--home[agent state directory]:directory:_files -/' \
                        '--cwd[default session working directory]:directory:_files -/' \
                        '--log-level[debug|info|warn|error]:level:(debug info warn error)' \
                        '--remote[drive a remote coddy http server]:remote:' \
                        '--remote-token[bearer token for --remote]:token:'
                    ;;
            esac
            ;;
    esac
}

_coddy "$@"
