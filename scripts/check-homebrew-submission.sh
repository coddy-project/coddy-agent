#!/usr/bin/env bash
# Preflight for the homebrew/core submission.
#
# Homebrew's acceptance criteria are checkable from the outside, and most of a
# rejected pull request is spent on things a script can answer first: whether the
# token is free, whether the repository clears the notability threshold for the
# kind of submission being made, whether the rendered formula is valid Ruby and
# names a real release. This runs those checks and prints the submission steps
# for a macOS host, where `brew audit` can finish the job.
#
# Nothing here talks to Homebrew's repositories other than reading them, and
# nothing is pushed or opened; the last section is the manual procedure.
#
# Usage:
#   scripts/check-homebrew-submission.sh [options]
#
#   --version VER   release to check (default: `make -s print-version`)
#   --repo OWNER/N  upstream repository (default: coddy-project/coddy-agent)
#   --token NAME    Homebrew token to claim (default: coddy)
#   --self          notability as a self-submission by the repository owner
#                   (the default; --third-party for a submission by someone else)
#   --third-party   notability as a submission by an unaffiliated contributor
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

version=""
repo="coddy-project/coddy-agent"
token="coddy"
self=1

while [ $# -gt 0 ]; do
    case "$1" in
        --version) version="$2"; shift 2 ;;
        --repo) repo="$2"; shift 2 ;;
        --token) token="$2"; shift 2 ;;
        --self) self=1; shift ;;
        --third-party) self=0; shift ;;
        -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$version" ]; then
    version=$(make -s print-version)
fi

fail=0
note() { printf '  %s\n' "$1"; }
ok() { printf 'ok      %s\n' "$1"; }
bad() { printf 'BLOCKED %s\n' "$1"; fail=1; }
warn() { printf 'check   %s\n' "$1"; }

if ! command -v gh >/dev/null 2>&1; then
    echo "this needs the gh CLI to read GitHub's API" >&2
    exit 2
fi

echo "Homebrew submission preflight for ${repo} ${version}"
echo

# --- Package type -----------------------------------------------------------
# Homebrew routes open-source command-line software to homebrew/core as a
# formula built from source, and keeps homebrew/cask for native applications and
# binary-only software. Coddy is MIT-licensed and command-line only, so the
# submission is a formula; this is a statement of that decision, not a probe.
echo "Target repository"
ok "homebrew/core, as a formula built from source (open-source, command-line only)"
note "homebrew/cask takes native applications and binary-only software"
note "our own prebuilt cask stays on the release asset - see docs/homebrew.md"
echo

# --- Token availability -----------------------------------------------------
echo "Token \"${token}\""
for tap in homebrew-core:Formula homebrew-cask:Casks; do
    name=${tap%%:*}
    dir=${tap##*:}
    path="${dir}/${token:0:1}/${token}.rb"
    if gh api "repos/Homebrew/${name}/contents/${path}" --silent >/dev/null 2>&1; then
        bad "Homebrew/${name} already carries ${path}"
    else
        ok "free in Homebrew/${name} (${path})"
    fi
done
open_prs=$(gh api "search/issues?q=${token}+repo:Homebrew/homebrew-core+repo:Homebrew/homebrew-cask" --jq '.total_count' 2>/dev/null || echo "?")
note "issues and pull requests mentioning \"${token}\" in either repository: ${open_prs}"
echo

# --- Notability -------------------------------------------------------------
# Package Acceptance Policy: 30 forks, 30 watchers or 75 stars, and 90 / 90 / 225
# when the repository owner submits it themselves.
echo "Notability"
# One API read, parsed once: with set -e an empty answer would otherwise abort
# the run inside a parser rather than reporting which check could not be made.
metrics=$(gh repo view "$repo" --json stargazerCount,forkCount,watchers,createdAt,licenseInfo 2>/dev/null || true)
parsed=$(printf '%s' "$metrics" | python3 -c '
import json, sys
d = json.load(sys.stdin)
licence = d["licenseInfo"]["name"] if d["licenseInfo"] else "none"
print(d["stargazerCount"], d["forkCount"], d["watchers"]["totalCount"], d["createdAt"][:10], licence)
' 2>/dev/null || true)

if [ -z "$parsed" ]; then
    bad "could not read ${repo} from the GitHub API"
    note "check gh auth status, then run this again"
else
    # licence is last so it keeps its spaces
    read -r stars forks watchers created licence <<<"$parsed"

    if [ "$self" = 1 ]; then
        kind="self-submission by the repository owner"
        need_stars=225; need_forks=90; need_watchers=90
    else
        kind="submission by an unaffiliated contributor"
        need_stars=75; need_forks=30; need_watchers=30
    fi
    note "${kind}"
    note "${stars} stars, ${forks} forks, ${watchers} watchers; repository created ${created}; licence ${licence}"
    if [ "$stars" -ge "$need_stars" ] || [ "$forks" -ge "$need_forks" ] || [ "$watchers" -ge "$need_watchers" ]; then
        ok "clears one of ${need_stars} stars / ${need_forks} forks / ${need_watchers} watchers"
    else
        bad "below ${need_stars} stars, ${need_forks} forks and ${need_watchers} watchers"
        note "short by $((need_stars - stars)) stars, $((need_forks - forks)) forks or $((need_watchers - watchers)) watchers"
        note "a submission by someone other than the owner is held to 75 / 30 / 30"
    fi

    # A repository younger than 30 days is normally not eligible whatever its
    # metrics say, so it is a separate answer rather than part of the one above.
    age_days=$(( ( $(date -u +%s) - $(date -u -d "$created" +%s) ) / 86400 ))
    if [ "$age_days" -ge 30 ]; then
        ok "repository is ${age_days} days old (30 or more)"
    else
        bad "repository is ${age_days} days old; under 30 is normally not eligible"
    fi
fi
echo

# --- The release the formula would carry ------------------------------------
echo "Release ${version}"
if gh api "repos/${repo}/releases/tags/${version}" --silent >/dev/null 2>&1; then
    ok "published, so the source archive has a stable checksum"
else
    bad "no release tagged ${version} at ${repo}"
fi
formula="dist/formula/coddy.rb"
if [ ! -f "$formula" ]; then
    warn "no ${formula} yet; render it with: make brew-formula VERSION=${version}"
else
    if ruby -c "$formula" >/dev/null 2>&1; then
        ok "${formula} is valid Ruby"
    else
        bad "${formula} does not parse"
    fi
    if grep -q "refs/tags/${version}.tar.gz" "$formula"; then
        ok "${formula} points at ${version}"
    else
        bad "${formula} was rendered for another version"
    fi
    if grep -qE '^\s*sha256 "[0-9a-f]{64}"' "$formula"; then
        ok "${formula} pins a source checksum"
    else
        bad "${formula} has no source checksum"
    fi
    if grep -q "bottle do" "$formula"; then
        bad "${formula} carries a bottle block; BrewTestBot adds those, submitters do not"
    else
        ok "${formula} carries no bottle block"
    fi
fi
echo

# --- What only a macOS host can finish -------------------------------------
echo "On a macOS host with Homebrew"
note "brew tap --force homebrew/core"
note "cd \"\$(brew --repository homebrew/core)\" && git checkout -b coddy origin/HEAD"
note "cp ${root}/${formula} Formula/c/coddy.rb"
note "brew install --build-from-source --verbose --debug coddy"
note "brew test coddy"
note "brew audit --strict --online --new coddy"
note "brew style coddy"
note "then push the branch to your fork of Homebrew/homebrew-core and open the pull request"
echo
note "Homebrew requires an AI/LLM disclosure in the pull request body, forbids an"
note "AI/LLM authorship trailer, and expects the submitter to answer review"
note "comments themselves - docs/homebrew.md quotes the whole list."
echo

if [ "$fail" != 0 ]; then
    echo "preflight: not ready to submit"
    exit 1
fi
echo "preflight: nothing blocking from this side"
