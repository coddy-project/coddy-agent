#!/usr/bin/env bash
# Install the Coddy cask with a real Homebrew and check what it put in place.
#
# Two modes:
#   --cask FILE   a rendered cask (scripts/build-homebrew-cask.sh), installed out
#                 of a throwaway local tap - how a pull request proves a template
#                 change before a release carries it. Current Homebrew refuses a
#                 cask given by its path, so a tap is the only way in;
#   --tap NAME    the published cask, installed by name from that tap exactly as
#                 the documentation says (default: coddy-project/coddy-agent).
#
# Either way it checks that `coddy -v` prints the version, that the linked
# binary lives in the Caskroom with no quarantine attribute left on it, that the
# man page and both completions are linked, that `coddy update` names
# `brew upgrade --cask coddy` and that this command runs. Then it uninstalls the
# cask and removes the tap it added, unless --keep. Nothing it runs turns on
# Homebrew's developer mode, which would stay on for every later brew command.
#
# macOS with Homebrew only; it refuses to run over an installed coddy cask.
#
# Usage:
#   scripts/check-homebrew-cask.sh [--cask FILE | --tap NAME] [--version VER] [--keep]
#
#   --version VER   oldest version the installed cask may be: after a release,
#                   the one it has just published (a re-run for an older tag
#                   leaves a newer cask in the tap, which passes)
#   --keep          leave the cask installed and the tap in place
set -euo pipefail

cask_file=""
tap="coddy-project/coddy-agent"
version=""
keep=0

while [ $# -gt 0 ]; do
    case "$1" in
        --cask) cask_file="$2"; shift 2 ;;
        --tap) tap="$2"; shift 2 ;;
        --version) version="$2"; shift 2 ;;
        --keep) keep=1; shift ;;
        -h|--help) sed -n '2,28p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

step() { printf '\n==> %s\n' "$*"; }

# version_lt succeeds when X.Y.Z $1 sorts before X.Y.Z $2, part by part as
# numbers.
version_lt() {
    local IFS=.
    local -a a b
    read -r -a a <<< "$1"
    read -r -a b <<< "$2"
    local i
    for i in 0 1 2; do
        if (( 10#${a[i]} < 10#${b[i]} )); then return 0; fi
        if (( 10#${a[i]} > 10#${b[i]} )); then return 1; fi
    done
    return 1
}
fail() {
    echo "check-homebrew-cask: $*" >&2
    exit 1
}

[ "$(uname -s)" = "Darwin" ] || fail "the cask installs macOS archives; run this on macOS"
# Developer mode lifts the rules a user's Homebrew applies (casks from paths and
# URLs among them), so the check runs without it.
unset HOMEBREW_DEVELOPER
command -v brew >/dev/null 2>&1 || fail "brew is not on PATH"
if brew list --cask coddy >/dev/null 2>&1; then
    fail "a coddy cask is already installed; uninstall it first (brew uninstall --cask coddy)"
fi

prefix=$(brew --prefix)
added_tap=""

cleanup() {
    [ "$keep" -eq 1 ] && return
    brew uninstall --cask coddy >/dev/null 2>&1 || true
    if [ -n "$added_tap" ]; then
        brew untap "$added_tap" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

step "$(brew --version | head -n 1) on macOS $(sw_vers -productVersion) $(uname -m)"

if [ -n "$cask_file" ]; then
    [ -f "$cask_file" ] || fail "no cask at ${cask_file}"
    tap="coddy-check/local"
    brew untap "$tap" >/dev/null 2>&1 || true
    step "a throwaway tap ${tap} with ${cask_file}"
    # A tap is a directory under Library/Taps; `brew tap-new` would also turn
    # Homebrew's developer mode on for good, on whatever Mac runs this.
    tap_dir="$(brew --repository)/Library/Taps/coddy-check/homebrew-local"
    mkdir -p "${tap_dir}/Casks"
    added_tap="$tap"
    cp "$cask_file" "${tap_dir}/Casks/coddy.rb"
elif ! brew tap | grep -qx "$tap"; then
    # `brew install <tap>/coddy` adds the tap by itself; remember to remove it.
    added_tap="$tap"
fi

full="${tap}/coddy"

# The command the documentation gives, word for word: no --cask, the full name.
step "brew install ${full}"
brew install "$full"

# After the install, so the tap a by-name install adds is there to read. style
# is a developer command: without HOMEBREW_DEVELOPER in its environment brew
# turns developer mode on for good (homebrew.devcmdrun), and from then on
# `brew update` follows Homebrew's main branch instead of its releases.
step "brew style ${full}"
HOMEBREW_DEVELOPER=1 brew style --cask "$full"

installed=$(brew list --cask --versions coddy | awk '{print $2}')
[ -n "$installed" ] || fail "brew does not list coddy after the install"
if [ -n "$version" ] && version_lt "$installed" "$version"; then
    fail "brew installed coddy ${installed}, older than ${version}"
fi
version="$installed"

step "the linked binary"
bin="${prefix}/bin/coddy"
[ -L "$bin" ] || fail "${bin} is not the symlink the cask links"
target=$(readlink "$bin")
case "$target" in
    */Caskroom/coddy/"${version}"/coddy) ;;
    *) fail "${bin} points at ${target}, not into the Caskroom of coddy ${version}" ;;
esac
printed=$("$bin" -v)
echo "$printed"
grep -qF "$version" <<< "$printed" || fail "coddy -v does not print ${version}"

step "quarantine"
# Homebrew quarantines the download; the cask's postflight step is what keeps
# the attribute off the binary. Print both, so the log shows the step at work.
download=$(brew --cache --cask "$full" 2>/dev/null || true)
if [ -n "$download" ] && xattr -p com.apple.quarantine "$download" >/dev/null 2>&1; then
    echo "the download ${download} carries com.apple.quarantine"
else
    echo "the download ${download:-(not in the cache)} carries no com.apple.quarantine"
fi
xattr -l "$target" || true
if xattr -p com.apple.quarantine "$target" >/dev/null 2>&1; then
    fail "${target} still carries com.apple.quarantine; Gatekeeper would stop its first run"
fi
# Informational: what Gatekeeper makes of the binary and whether it is on.
spctl --status || true
spctl --assess --type execute -vv "$target" 2>&1 || true

step "the man page and the completions"
for linked in share/man/man1/coddy.1 etc/bash_completion.d/coddy share/zsh/site-functions/_coddy; do
    [ -e "${prefix}/${linked}" ] || fail "the cask did not link ${prefix}/${linked}"
    echo "${prefix}/${linked}"
done

step "coddy update sends a cask install back to brew"
status=0
update_out=$("$bin" update --yes --version "$version" 2>&1) || status=$?
printf '%s\n' "$update_out"
# coddy update asks the GitHub API for the release before it looks at the file,
# without a token. A shared CI address runs out of anonymous calls now and then;
# that says nothing about the cask, and features/update_packages.feature holds
# the hint itself, so the step is reported as skipped rather than failed.
if grep -q 'rate limit exceeded' <<< "$update_out"; then
    msg="skipped: the GitHub API refused this address its anonymous rate limit"
    if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::warning::coddy update ${msg}"; else echo "$msg"; fi
else
    [ "$status" -ne 0 ] || fail "coddy update replaced a file Homebrew owns instead of pointing at brew"
    grep -qF "brew upgrade --cask coddy" <<< "$update_out" ||
        fail "coddy update does not name 'brew upgrade --cask coddy'"
fi

step "brew upgrade --cask coddy"
brew upgrade --cask coddy

if [ "$keep" -eq 0 ]; then
    step "brew uninstall --cask coddy"
    brew uninstall --cask coddy
    [ ! -e "$bin" ] || fail "${bin} is still there after the uninstall"
fi

step "the coddy cask of ${full} installs, runs and upgrades"
