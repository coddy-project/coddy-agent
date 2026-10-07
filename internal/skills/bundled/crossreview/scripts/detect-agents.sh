#!/bin/sh
# detect-agents.sh - probe which console code-agent CLIs are installed.
#
# Prints one line per detected agent, tab-separated:
#   agent <TAB> binary_path <TAB> models_cmd <TAB> run_template
#
# The agents and their command templates come from agents.tsv next to this
# script, the same table crossreview.py and detect-agents.ps1 read. The
# template keeps the {model}, {brief} and {out} placeholders: {model} is filled
# in when the roster is written, {brief} and {out} on every run. models_cmd is
# empty when the CLI cannot list its own models (or the listing failed), and
# the user types the model id instead.
#
# Nothing beyond a POSIX shell is needed: this is the detection path for an
# agent that has no Python (Coddy on Termux or a bare container).

set -u

# The script's own directory, without dirname(1): the table sits next to it.
case $0 in
    */*) here=${0%/*} ;;
    *) here=. ;;
esac
table="$here/agents.tsv"
if [ ! -r "$table" ]; then
    echo "detect-agents.sh: $table is missing" >&2
    exit 1
fi

timeout_bin=""
for _t in timeout gtimeout; do
    if command -v "$_t" >/dev/null 2>&1; then
        timeout_bin=$_t
        break
    fi
done

# probe <seconds> <cmd...> - run a probe bounded in time, so a CLI that waits
# for input or a login cannot stall detection. Stock macOS has no timeout(1);
# a watchdog subshell stands in for it there.
probe() {
    _secs=$1
    shift
    if [ -n "$timeout_bin" ]; then
        "$timeout_bin" "$_secs" "$@" </dev/null
        return
    fi
    "$@" </dev/null &
    _pid=$!
    (sleep "$_secs" && kill "$_pid") >/dev/null 2>&1 &
    _watch=$!
    wait "$_pid"
    _rc=$?
    kill "$_watch" >/dev/null 2>&1
    return $_rc
}

# subst <text> <from> <to> - replace every <from> with <to>, without sed, so a
# template may hold any character.
subst() {
    _out=''
    _rest=$1
    while :; do
        case $_rest in
            *"$2"*)
                _out=$_out${_rest%%"$2"*}$3
                _rest=${_rest#*"$2"}
                ;;
            *)
                printf '%s' "$_out$_rest"
                return
                ;;
        esac
    done
}

# verify <marker> <cmd...> - a name on PATH is not enough (`agent` can be an
# unrelated executable): --version or --help must exit 0 and print the marker.
verify() {
    _marker=$1
    shift
    for _flag in --version --help; do
        if _text=$(probe 10 "$@" "$_flag" 2>&1); then
            case $(printf '%s' "$_text" | tr '[:upper:]' '[:lower:]') in
                *"$_marker"*) return 0 ;;
            esac
        fi
    done
    return 1
}

tab=$(printf '\t')
while IFS="$tab" read -r agent bins marker models posix pwsh; do
    case $agent in
        '' | '#'*) continue ;;
    esac
    : "$pwsh"
    rest_bins=$bins
    while [ -n "$rest_bins" ]; do
        cand=${rest_bins%%,*}
        case $rest_bins in
            *,*) rest_bins=${rest_bins#*,} ;;
            *) rest_bins='' ;;
        esac
        exe=${cand%% *}
        fixed=''
        case $cand in
            *' '*) fixed=${cand#* } ;;
        esac
        path=$(command -v "$exe" 2>/dev/null) || continue
        [ -n "$path" ] || continue
        # $fixed is a fixed subcommand ("agent" for "cursor agent"), split on purpose.
        # shellcheck disable=SC2086
        verify "$marker" "$path" $fixed || continue
        models_cmd=''
        if [ "$models" != '-' ]; then
            # shellcheck disable=SC2086
            if probe 20 "$path" $fixed $models >/dev/null 2>&1; then
                models_cmd="$cand $models"
            fi
        fi
        template=$(subst "$posix" '{bin}' "$cand")
        printf '%s\t%s\t%s\t%s\n' "$agent" "$path" "$models_cmd" "$template"
        break
    done
done <"$table"
