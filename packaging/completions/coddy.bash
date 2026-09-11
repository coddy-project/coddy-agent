# bash completion for coddy
#
# Keep the command list in sync with printUsage() in cmd/coddy/main.go.

_coddy() {
    local cur prev commands
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    commands="cli acp serve sessions skills plugin mcp providers rules agents hooks update"

    if [ "${COMP_CWORD}" -eq 1 ]; then
        COMPREPLY=($(compgen -W "${commands} -h --help -v --version -c --continue -p --prompt --resume" -- "${cur}"))
        return
    fi

    case "${COMP_WORDS[1]}" in
        sessions)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "list export" -- "${cur}"))
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--format --out --no-tools --no-thinking" -- "${cur}"))
            ;;
        skills)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "list enable disable add sync remove" -- "${cur}"))
            ;;
        plugin)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "marketplace install remove enable disable" -- "${cur}"))
            [ "${COMP_CWORD}" -eq 3 ] && [ "${prev}" = marketplace ] &&
                COMPREPLY=($(compgen -W "list add remove sync" -- "${cur}"))
            ;;
        mcp)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "list trust untrust" -- "${cur}"))
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--cwd" -- "${cur}"))
            ;;
        providers)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "list login logout" -- "${cur}"))
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--browser --device --no-config --api-base --home" -- "${cur}"))
            ;;
        rules)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "list" -- "${cur}"))
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--cwd" -- "${cur}"))
            ;;
        agents|hooks)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "list trust untrust" -- "${cur}"))
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--cwd" -- "${cur}"))
            ;;
        update)
            COMPREPLY=($(compgen -W "--check -y --yes --version --repo --no-restart --no-notes" -- "${cur}"))
            ;;
        cli|acp)
            COMPREPLY=($(compgen -W "-t --test-config --dry-run --config --home --cwd --log-level --log-output --log-file --log-format --remote --remote-token" -- "${cur}"))
            ;;
        serve)
            COMPREPLY=($(compgen -W "status stop restart -d --daemon -t --test-config --dry-run --config --home --cwd --sessions-dir --session-id --log-level --log-output --log-file --log-format -H --host -P --port --auth-token --http --gateway --swarm --scheduler --swarm-host --swarm-port --swarm-auth-token --swarm-pairing-token --swarm-allow-insecure" -- "${cur}"))
            ;;
    esac
}

complete -F _coddy coddy
