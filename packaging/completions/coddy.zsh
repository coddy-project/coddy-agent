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
        'docs:read and search the built-in documentation'
        'update:install the latest release'
    )

    _arguments -C \
        '(-h --help)'{-h,--help}'[print the command list]' \
        '(-v --version)'{-v,--version}'[print the version]' \
        '(-c --continue)'{-c,--continue}'[continue the latest session here]' \
        '(-p --prompt)'{-p,--prompt}'[run one prompt and exit (- reads it from stdin)]:prompt:' \
        '(-i --prompt-file)'{-i,--prompt-file}'[run one prompt read from a file (- for stdin)]:prompt file:_files' \
        '--no-stdin[one-shot run: do not attach piped stdin]' \
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
                providers)
                    if (( CURRENT > 3 )); then
                        _arguments \
                            '--browser[neuraldeep: loopback browser callback instead of the device flow]' \
                            '--device[neuraldeep: the device flow, which is the default]' \
                            '--devin-cli[devin: use the login devin auth login already holds]' \
                            '--no-config[login: do not add the provider and its models to config.yaml]' \
                            '--api-base[neuraldeep: endpoint to sign in against]:url:' \
                            '--home[override CODDY_HOME]:dir:_files -/'
                    else
                        _values 'subcommand' list login logout
                    fi
                    ;;
                rules)    _values 'subcommand' list ;;
                docs)
                    if (( CURRENT == 3 )) && [[ $words[2] == show ]]; then
                        # The pages the binary carries, from the binary itself.
                        _values 'page' ${(f)"$(coddy docs list --slugs 2>/dev/null)"}
                    elif (( CURRENT > 2 )) && [[ $words[2] == search ]]; then
                        _arguments '--limit[sections to print]:count:'
                    else
                        _values 'subcommand' list search show
                    fi
                    ;;
                update)
                    _arguments \
                        '--check[report whether a newer release exists]' \
                        '(-y --yes)'{-y,--yes}'[install without confirmation]' \
                        '--version[install a specific release tag]:tag:' \
                        '--repo[GitHub repository to take releases from]:repo:' \
                        '--no-restart[Windows only: do not start Coddy again]' \
                        '--no-notes[do not report what changed after the install]'
                    ;;
                cli|acp)
                    _arguments \
                        '(-t --test-config)'{-t,--test-config}'[check config.yaml against the schema and exit]' \
                        '--dry-run[check config.yaml, then probe the paths, servers and credentials it names, and exit]' \
                        '--config[path to config.yaml]:file:_files' \
                        '--home[agent state directory]:directory:_files -/' \
                        '--cwd[default session working directory]:directory:_files -/' \
                        '--log-level[level, or a spec such as info,gateway.telegram=debug]:level:(debug info warn error)' \
                        '--remote[drive a remote coddy serve server]:remote:' \
                        '--remote-token[bearer token for --remote]:token:'
                    ;;
                serve)
                    _arguments \
                        '1: :((setup\:"run coddy serve as a systemd user service" uninstall\:"stop, disable and remove the systemd user service" status\:"report the background dispatcher" stop\:"stop the background dispatcher" restart\:"restart the background dispatcher" set-password\:"write the web UI sign-in account into config.yaml"))' \
                        '--user[account name for the web UI sign-in form (set-password)]:user:' \
                        '(-d --daemon)'{-d,--daemon}'[run in the background under a dispatcher]' \
                        '(-t --test-config)'{-t,--test-config}'[check config.yaml against the schema and exit]' \
                        '--dry-run[check config.yaml, then probe the paths, servers and credentials it names, and exit]' \
                        '--config[path to config.yaml]:file:_files' \
                        '--home[agent state directory]:directory:_files -/' \
                        '--cwd[default session working directory]:directory:_files -/' \
                        '--sessions-dir[sessions root]:directory:_files -/' \
                        '--log-level[level, or a spec such as info,gateway.telegram=debug]:level:(debug info warn error)' \
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
