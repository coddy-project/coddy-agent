#!/usr/bin/env bash
# sync-site-schema.sh — publish internal/config/config.schema.json to the site repository.
#
# Coddy writes a modeline into every config.yaml it saves pointing editors at
# https://coddy.dev/config.schema.json. That URL is served from the site
# repository as a byte-for-byte copy of internal/config/config.schema.json (the
# schema embedded into the binary for -t / --test-config), so a schema
# change that stops here leaves every editor validating saved configs against a
# schema the binary no longer matches.
#
# The copy is deliberately not automatic on commit: the schema sets
# "additionalProperties": false, so publishing a renamed or removed key before
# the release that understands it marks configs already on disk as invalid.
# This script stages the change and tells you when it is safe to push.
#
#   SITE_REPO   path to the coddy-project.github.io checkout (default:
#               coddy-project.github.io beside the main checkout - resolved
#               through git's common dir, so it is right from a worktree too)
#   CHECK=1     report drift and exit non-zero without writing anything
#
# Exit codes: 0 = in sync (or updated), 1 = drift found under CHECK=1,
#             2 = the site repository could not be used.
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_schema="$repo_root/internal/config/config.schema.json"

# The sibling is the sibling of the MAIN checkout: inside a git worktree
# $repo_root points somewhere under .git/worktrees, whose parent holds no
# site repository. git's common dir leads back to the real checkout.
main_checkout="$repo_root"
if common_dir="$(git -C "$repo_root" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)"; then
    main_checkout="$(dirname "$common_dir")"
fi
site_repo="${SITE_REPO:-$main_checkout/../coddy-project.github.io}"
target_schema="$site_repo/config.schema.json"
check_only="${CHECK:-0}"

if [ ! -f "$source_schema" ]; then
    echo "sync-site-schema: missing $source_schema" >&2
    exit 2
fi

if [ ! -d "$site_repo" ]; then
    echo "sync-site-schema: no site checkout at $site_repo" >&2
    echo "  clone it, or point SITE_REPO at your checkout:" >&2
    echo "  SITE_REPO=/path/to/coddy-project.github.io make site-schema" >&2
    exit 2
fi

if [ ! -f "$target_schema" ]; then
    echo "sync-site-schema: $target_schema does not exist" >&2
    echo "  the site repository should already publish the schema; check the path" >&2
    exit 2
fi

if cmp -s "$source_schema" "$target_schema"; then
    echo "sync-site-schema: already in sync ($target_schema)"
    exit 0
fi

if [ "$check_only" = "1" ]; then
    echo "sync-site-schema: the published schema is stale" >&2
    diff -u "$target_schema" "$source_schema" | head -40 >&2
    echo >&2
    echo "  run: make site-schema" >&2
    exit 1
fi

cp "$source_schema" "$target_schema"
echo "sync-site-schema: copied internal/config/config.schema.json -> $target_schema"
echo
echo "Next, in $site_repo:"
echo "  git add config.schema.json && git commit"
echo
echo "Hold the push until the coddy-agent change is released when the schema"
echo "renamed or removed a key: editors fetch this file for configs already on"
echo "disk, and \"additionalProperties\": false turns an unknown key into an error."
