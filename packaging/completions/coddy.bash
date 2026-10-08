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
            [ "${COMP_CWORD}" -gt 2 ] && [ "${COMP_WORDS[2]}" = add ] && COMPREPLY=($(compgen -W "--project" -- "${cur}"))
            ;;
        plugin)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "marketplace install remove enable disable list" -- "${cur}"))
            [ "${COMP_CWORD}" -eq 3 ] && [ "${prev}" = marketplace ] &&
                COMPREPLY=($(compgen -W "add list update remove sync trust untrust" -- "${cur}"))
            ;;
        mcp)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "list trust untrust" -- "${cur}"))
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--cwd" -- "${cur}"))
            ;;
        providers)
            [ "${COMP_CWORD}" -eq 2 ] && COMPREPLY=($(compgen -W "list login logout" -- "${cur}"))
            [ "${COMP_CWORD}" -gt 2 ] && COMPREPLY=($(compgen -W "--type --browser --device --devin-cli --no-config --api-base --home" -- "${cur}"))
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
            # The verb and the words after it, the flags and their values
            # aside: --lang may stand anywhere, before the verb too. Bash
            # splits --lang=ru into --lang, = and ru, so a value after = is
            # skipped as well.
            local verb="" npos=0 i
            for ((i = 2; i < COMP_CWORD; i++)); do
                case "${COMP_WORDS[i]}" in
                    --lang|--limit)
                        ((i++))
                        [ "${COMP_WORDS[i]}" = "=" ] && ((i++))
                        ;;
                    --lang=*|--limit=*) ;;
                    *) if [ -z "${verb}" ]; then verb="${COMP_WORDS[i]}"; else ((npos++)); fi ;;
                esac
            done
            if [ "${prev}" = "--lang" ] || { [ "${prev}" = "=" ] && [ "${COMP_WORDS[COMP_CWORD-2]}" = "--lang" ]; }; then
                COMPREPLY=($(compgen -W "en ru" -- "${cur}"))
            elif [[ "${cur}" == --lang=* ]]; then
                COMPREPLY=($(compgen -P "--lang=" -W "en ru" -- "${cur#--lang=}"))
            elif [ -z "${verb}" ]; then
                COMPREPLY=($(compgen -W "list search show --lang" -- "${cur}"))
            elif [ "${verb}" = show ] && [ "${npos}" -eq 0 ]; then
                # The pages the binary carries, from the binary itself.
                COMPREPLY=($(compgen -W "$(coddy docs list --slugs 2>/dev/null)" -- "${cur}"))
            elif [ "${verb}" = search ]; then
                COMPREPLY=($(compgen -W "--limit --lang" -- "${cur}"))
            else
                COMPREPLY=($(compgen -W "--lang" -- "${cur}"))
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
            COMPREPLY=($(compgen -W "install uninstall status stop restart set-password --user -d --daemon -t --test-config --dry-run --config --home --cwd --sessions-dir --session-id --log-level --log-output --log-file --log-format -H --host -P --port --auth-token --http --gateway --swarm --scheduler --swarm-host --swarm-port --swarm-auth-token --swarm-pairing-token --swarm-allow-insecure" -- "${cur}"))
            ;;
    esac
}

complete -F _coddy coddy
