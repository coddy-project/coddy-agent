# bash completion for coddy
#
# Keep the command list in sync with printUsage() in cmd/coddy/main.go.

_coddy() {
    local cur prev commands
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    commands="cli acp http gateway sessions skills plugin mcp codex providers rules agents hooks update"

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
        codex)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "login status logout" -- "${cur}"))
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--provider --no-config --home" -- "${cur}"))
            ;;
        providers)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "list login logout" -- "${cur}"))
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--device --no-config --home" -- "${cur}"))
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
            COMPREPLY=($(compgen -W "--check -y --yes --version --repo --no-restart" -- "${cur}"))
            ;;
        cli|acp|http|gateway)
            COMPREPLY=($(compgen -W "--config --home --cwd --log-level --log-output --log-file --log-format --remote --remote-token" -- "${cur}"))
            ;;
    esac
}

complete -F _coddy coddy
