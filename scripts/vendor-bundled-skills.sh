#!/usr/bin/env bash
# Refresh the vendored copies of the skills Coddy carries inside its binary.
#
# Every skill of the standard delivery lives in internal/skills/bundled/<name>/
# and is embedded from there. A skill whose home is another repository is
# vendored here rather than fetched at runtime, so a fresh install has it with
# no network round trip; this script is how that copy is brought up to date.
# scripts/bundled-skills.json says where each one comes from - an entry marked
# "origin": "local" is written in this repository and is left alone.
#
# Only the files a delivered skill needs are copied: SKILL.md, its references/
# tree, the README a reader opens next to it, and the LICENSE that has to travel
# with a redistributed copy. Plugin manifests for other agents, editor folders
# and dotfiles stay upstream.
#
# Usage:
#   scripts/vendor-bundled-skills.sh [options]
#
#   --skill NAME    refresh only this skill (repeatable)
#   --from DIR      take the sources from clones under DIR instead of cloning
#                   (DIR/<name> must be a checkout of that skill)
#   --check         report drift and write nothing; non-zero when stale
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

manifest="scripts/bundled-skills.json"
dest_root="internal/skills/bundled"
from=""
check=0
only=()

while [ $# -gt 0 ]; do
    case "$1" in
        --skill) only+=("$2"); shift 2 ;;
        --from) from="$2"; shift 2 ;;
        --check) check=1; shift ;;
        -h|--help) sed -n '2,24p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

command -v jq >/dev/null 2>&1 || { echo "vendor-bundled-skills: jq is required" >&2; exit 1; }
command -v git >/dev/null 2>&1 || { echo "vendor-bundled-skills: git is required" >&2; exit 1; }

wanted() {
    [ ${#only[@]} -eq 0 ] && return 0
    local name=$1 s
    for s in "${only[@]}"; do [ "$s" = "$name" ] && return 0; done
    return 1
}

# copy_skill SRC DST - the delivered subset of a skill checkout.
copy_skill() {
    local src=$1 dst=$2
    [ -f "$src/SKILL.md" ] || { echo "vendor-bundled-skills: $src has no SKILL.md" >&2; return 1; }
    rm -rf "$dst"
    mkdir -p "$dst"
    cp "$src/SKILL.md" "$dst/SKILL.md"
    local extra
    for extra in README.md LICENSE; do
        [ -f "$src/$extra" ] && cp "$src/$extra" "$dst/$extra"
    done
    if [ -d "$src/references" ]; then
        cp -R "$src/references" "$dst/references"
        find "$dst/references" -name '.git*' -prune -exec rm -rf {} + 2>/dev/null || true
    fi
    return 0
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

stale=0
count=0

while read -r name repo ref; do
    wanted "$name" || continue
    if [ "$repo" = "null" ] || [ -z "$repo" ]; then
        continue   # written in this repository, not vendored
    fi
    count=$((count + 1))

    if [ -n "$from" ]; then
        src="$from/$name"
        [ -d "$src" ] || { echo "vendor-bundled-skills: $src is not a directory" >&2; exit 1; }
    else
        src="$tmp/$name"
        git clone --quiet --depth 1 --branch "$ref" "$repo" "$src"
    fi

    staged="$tmp/staged-$name"
    copy_skill "$src" "$staged"
    version=$(sed -n 's/^version:[[:space:]]*//p' "$staged/SKILL.md" | head -1)
    [ -n "$version" ] || { echo "vendor-bundled-skills: $name has no version in its SKILL.md frontmatter" >&2; exit 1; }

    dst="$dest_root/$name"
    if [ "$check" = 1 ]; then
        if ! diff -rq "$staged" "$dst" >/dev/null 2>&1; then
            echo "stale: $name (upstream $version)"
            stale=1
        fi
        continue
    fi
    rm -rf "$dst"
    mkdir -p "$(dirname "$dst")"
    mv "$staged" "$dst"
    echo "vendored $name $version"
done < <(jq -r '.skills[] | "\(.name) \(.repo // "null") \(.ref // "main")"' "$manifest")

if [ "$check" = 1 ]; then
    if [ "$stale" = 1 ]; then
        echo "vendor-bundled-skills: run 'make skills-vendor' to refresh" >&2
        exit 1
    fi
    echo "vendor-bundled-skills: $count vendored skill(s) match their upstream"
fi
