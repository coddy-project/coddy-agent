# bash completion for coddy
#
# Keep the command list in sync with printUsage() in cmd/coddy/main.go.

_coddy() {
    local cur prev commands
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    commands="cli acp serve sessions skills plugin mcp providers rules agents hooks docs update"
    # The one-shot flags of the console, after `coddy` and after `coddy cli`.
    local prompt_flags="-p --prompt -i --prompt-file --no-stdin"

    # -i reads the prompt from a file (or - for stdin).
    case "${prev}" in
        -i|--prompt-file)
            COMPREPLY=($(compgen -f -- "${cur}"))
            return
            ;;
    esac

    if [ "${COMP_CWORD}" -eq 1 ]; then
        COMPREPLY=($(compgen -W "${commands} -h --help -v --version -c --continue ${prompt_flags} --resume" -- "${cur}"))
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
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--browser --device --devin-cli --no-config --api-base --home" -- "${cur}"))
            ;;
        rules)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "list" -- "${cur}"))
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--cwd" -- "${cur}"))
            ;;
        agents|hooks)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "list trust untrust" -- "${cur}"))
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--cwd" -- "${cur}"))
            ;;
        docs)
            if [ "${COMP_CWORD}" -eq 2 ]; then
                COMPREPLY=($(compgen -W "list search show" -- "${cur}"))
            elif [ "${COMP_WORDS[2]}" = show ] && [ "${COMP_CWORD}" -eq 3 ]; then
                # The pages the binary carries, from the binary itself.
                COMPREPLY=($(compgen -W "$(coddy docs list --slugs 2>/dev/null)" -- "${cur}"))
            elif [ "${COMP_WORDS[2]}" = search ]; then
                COMPREPLY=($(compgen -W "--limit" -- "${cur}"))
            fi
            ;;
        update)
            COMPREPLY=($(compgen -W "--check -y --yes --version --repo --no-restart --no-notes" -- "${cur}"))
            ;;
        cli)
            COMPREPLY=($(compgen -W "-t --test-config --dry-run --config --home --cwd --log-level --log-output --log-file --log-format --remote --remote-token -c --continue ${prompt_flags} --model --mode --permission-mode" -- "${cur}"))
            ;;
        acp)
            COMPREPLY=($(compgen -W "-t --test-config --dry-run --config --home --cwd --log-level --log-output --log-file --log-format --remote --remote-token" -- "${cur}"))
            ;;
        -*)
            # `coddy -p ...`: the console's flags.
            COMPREPLY=($(compgen -W "-c --continue ${prompt_flags} --model --mode --permission-mode --session-id --cwd --remote --remote-token" -- "${cur}"))
            ;;
        serve)
            COMPREPLY=($(compgen -W "setup status stop restart set-password --user -d --daemon -t --test-config --dry-run --config --home --cwd --sessions-dir --session-id --log-level --log-output --log-file --log-format -H --host -P --port --auth-token --http --gateway --swarm --scheduler --swarm-host --swarm-port --swarm-auth-token --swarm-pairing-token --swarm-allow-insecure" -- "${cur}"))
            ;;
    esac
}

complete -F _coddy coddy
