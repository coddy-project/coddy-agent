#!/usr/bin/env bash
# Publish a rendered Homebrew cask to the Coddy tap.
#
# Current Homebrew installs a cask only out of a tap, so every release commits
# the cask it has just rendered (scripts/build-homebrew-cask.sh) to the tap
# repository as Casks/coddy.rb. `brew install coddy-project/coddy-agent/coddy` then
# finds it by name, and `brew upgrade` sees each new release.
#
# The tap only moves forward. A cask for a release older than the one the tap
# carries is skipped - that is a release job re-run for an old tag - and a cask
# identical to the tap's changes nothing, so re-running a release is safe.
#
# Usage:
#   scripts/publish-homebrew-tap.sh --version VER [options]
#
#   --version VER    release the cask was rendered for (required)
#   --cask FILE      the rendered cask (default: dist/coddy.rb)
#   --tap-repo URL   git URL of the tap
#                    (default: git@github.com:coddy-project/homebrew-coddy-agent.git)
#   --branch NAME    branch of the tap (default: main)
#
# Environment:
#   HOMEBREW_TAP_DEPLOY_KEY   private SSH key with write access to the tap - the
#                             release job's secret. Without it git pushes with
#                             the caller's own credentials.
#   GITHUB_TOKEN              optional; authenticates the read of GitHub's SSH
#                             host keys, which otherwise shares the hourly limit
#                             of unauthenticated API calls with the whole runner
set -euo pipefail

version=""
cask="dist/coddy.rb"
tap_repo="git@github.com:coddy-project/homebrew-coddy-agent.git"
branch="main"

while [ $# -gt 0 ]; do
    case "$1" in
        --version) version="$2"; shift 2 ;;
        --cask) cask="$2"; shift 2 ;;
        --tap-repo) tap_repo="$2"; shift 2 ;;
        --branch) branch="$2"; shift 2 ;;
        -h|--help) sed -n '2,28p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

die() {
    echo "publish-homebrew-tap: $*" >&2
    exit 1
}

semver_re='^[0-9]+\.[0-9]+\.[0-9]+$'
[ -n "$version" ] || die "--version is required"
[[ "$version" =~ $semver_re ]] || die "--version must be X.Y.Z, got ${version}"
[ -f "$cask" ] || die "no cask at ${cask}; render it first with scripts/build-homebrew-cask.sh"
cask=$(cd "$(dirname "$cask")" && pwd)/$(basename "$cask")

# cask_version prints the version stanza of a cask file.
cask_version() {
    sed -n 's/^[[:space:]]*version "\([^"]*\)"[[:space:]]*$/\1/p' "$1" | head -n 1
}

# version_lt succeeds when X.Y.Z $1 sorts before X.Y.Z $2, part by part as
# numbers: 1.3.9 is older than 1.3.10.
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

rendered=$(cask_version "$cask")
[ "$rendered" = "$version" ] || die "${cask} is the cask of ${rendered:-no version}, not of ${version}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if [ -n "${HOMEBREW_TAP_DEPLOY_KEY:-}" ]; then
    # The deploy key writes to the tap and nowhere else. GitHub's host keys come
    # from its API over HTTPS, so the SSH connection is not trusted on first use.
    key="${work}/deploy_key"
    (umask 077 && printf '%s\n' "$HOMEBREW_TAP_DEPLOY_KEY" > "$key")
    curl -fsSL ${GITHUB_TOKEN:+-H "Authorization: Bearer ${GITHUB_TOKEN}"} https://api.github.com/meta |
        grep -oE '"(ssh-ed25519|ecdsa-sha2-nistp256|ssh-rsa) [A-Za-z0-9+/=]+"' |
        tr -d '"' | sed 's/^/github.com /' > "${work}/known_hosts"
    [ -s "${work}/known_hosts" ] || die "could not read GitHub's SSH host keys from api.github.com/meta"
    export GIT_SSH_COMMAND="ssh -i ${key} -o IdentitiesOnly=yes -o UserKnownHostsFile=${work}/known_hosts -o StrictHostKeyChecking=yes"
fi

# publish makes one attempt and returns 3 when the push lost a race with
# another release job, so the caller starts again from the tap's new head. It
# runs as the left side of `||`, where bash ignores errexit, so every step that
# can fail says so itself.
publish() {
    local tap="${work}/tap"
    rm -rf "$tap"
    git clone --quiet --depth 1 --branch "$branch" "$tap_repo" "$tap" ||
        die "cannot clone branch ${branch} of ${tap_repo}"

    local current="${tap}/Casks/coddy.rb"
    if [ -f "$current" ]; then
        if cmp -s "$cask" "$current"; then
            echo "the tap already carries this cask of coddy ${version}"
            return 0
        fi
        local published
        published=$(cask_version "$current")
        if [[ "$published" =~ $semver_re ]] && version_lt "$version" "$published"; then
            echo "the tap carries coddy ${published}, newer than ${version}; leaving it"
            return 0
        fi
    fi

    mkdir -p "${tap}/Casks" && cp "$cask" "$current" || die "cannot write ${current}"
    git -C "$tap" add Casks/coddy.rb || die "cannot stage the cask"
    # A release runner has no git identity of its own; an operator publishing
    # by hand keeps the one git gives the clone (global, not the checkout's).
    local identity=()
    if [ -z "$(git -C "$tap" config user.email || true)" ]; then
        identity=(-c "user.name=github-actions[bot]" -c "user.email=41898282+github-actions[bot]@users.noreply.github.com")
    fi
    # ${identity[@]+...}: an empty array is unbound to the bash 3.2 macOS ships.
    git -C "$tap" ${identity[@]+"${identity[@]}"} commit --quiet -m "coddy ${version}" ||
        die "cannot commit the cask"
    if ! git -C "$tap" push --quiet origin "HEAD:${branch}"; then
        return 3
    fi
    echo "published coddy ${version} to ${tap_repo} (${branch})"
}

for attempt in 1 2 3; do
    status=0
    publish || status=$?
    if [ "$status" -ne 3 ]; then
        exit "$status"
    fi
    echo "the push to ${tap_repo} was rejected (attempt ${attempt}); reading the tap again" >&2
done
die "could not push to ${tap_repo} after 3 attempts"
