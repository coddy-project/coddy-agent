#!/usr/bin/env bash
# sync-site-docs.sh — publish the documentation layer to the site repository.
#
# coddy.dev carries a stable address for every page of docs/nav.yaml:
# coddy.dev/docs/<slug> redirects a person to the page on GitHub (the fragment
# survives), coddy.dev/docs/<slug>.md is the Markdown itself for agents, and
# llms.txt plus llms-full.txt at the site root index them. The binary, the
# config schema and the bundled skill print those addresses, so they must
# exist for every page and match the repository. internal/docsgen renders all
# of them; this script points it at the site checkout.
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

main_checkout="$repo_root"
if common_dir="$(git -C "$repo_root" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)"; then
    main_checkout="$(dirname "$common_dir")"
fi
site_repo="${SITE_REPO:-$main_checkout/../coddy-project.github.io}"
check_only="${CHECK:-0}"

if [ ! -d "$site_repo" ]; then
    echo "sync-site-docs: no site checkout at $site_repo" >&2
    echo "  clone it, or point SITE_REPO at your checkout:" >&2
    echo "  SITE_REPO=/path/to/coddy-project.github.io make site-docs" >&2
    exit 2
fi

cd "$repo_root" || exit 2
if [ "$check_only" = "1" ]; then
    if go run ./cmd/docsgen -skip-cli -site "$site_repo" -site-only; then
        echo "sync-site-docs: the site layer is in sync ($site_repo)"
        exit 0
    fi
    echo "  run: make site-docs" >&2
    exit 1
fi

go run ./cmd/docsgen -skip-cli -site "$site_repo" -site-only -write || exit 2
echo
echo "Next, in $site_repo:"
echo "  git add docs llms.txt llms-full.txt && git commit"
echo
echo "Push it together with the coddy-agent change it belongs to: a redirect"
echo "page for a page that is not on main yet lands on a 404."
