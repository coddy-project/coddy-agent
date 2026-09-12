#!/usr/bin/env bash
# Render the Homebrew formula for one release.
#
# A formula pins the checksum of the source archive it builds, so this needs the
# tag to exist on GitHub: the archive is fetched and hashed rather than guessed.
# The rendered file is what a homebrew/core pull request carries as
# Formula/c/coddy.rb - see docs/getting-started/homebrew.md.
#
# Usage:
#   scripts/build-homebrew-formula.sh [options]
#
#   --version VER   release to render (default: `make -s print-version`)
#   --out DIR       where coddy.rb lands (default: dist/formula)
#   --repo OWNER/N  GitHub repository the archive comes from
#                   (default: coddy-project/coddy-agent)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

out="dist/formula"
version=""
repo="coddy-project/coddy-agent"

while [ $# -gt 0 ]; do
    case "$1" in
        --version) version="$2"; shift 2 ;;
        --out) out="$2"; shift 2 ;;
        --repo) repo="$2"; shift 2 ;;
        -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$version" ]; then
    version=$(make -s print-version)
fi

case "$version" in
    [0-9]*.[0-9]*.[0-9]*) ;;
    *)
        echo "version must be a released tag like 1.0.13, got ${version}" >&2
        echo "a formula pins the checksum of a published source archive" >&2
        exit 2
        ;;
esac

mkdir -p "$out"
out=$(cd "$out" && pwd)

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The source tarball of the tag, which is what the formula builds from. GitHub
# serves a byte-stable archive for a tag, so the checksum belongs to the tag and
# not to the moment it was fetched.
archive="${work}/coddy-${version}.tar.gz"
url="https://github.com/${repo}/archive/refs/tags/${version}.tar.gz"
echo "downloading ${url}" >&2
if ! curl -fsSL -o "$archive" "$url"; then
    echo "no source archive for ${version} at ${repo}" >&2
    echo "push the tag first, or pass --version of a published release" >&2
    exit 1
fi
sha_source=$(sha256sum "$archive" | cut -d' ' -f1)

# The template header explains how the file is rendered; the published formula
# starts at the `class` line and carries none of it.
sed \
    -e "s|__VERSION__|${version}|g" \
    -e "s|__SHA256_SOURCE__|${sha_source}|g" \
    packaging/homebrew/coddy-formula.rb.tmpl |
    sed -n '/^class /,$p' > "${out}/coddy.rb"

echo "wrote ${out}/coddy.rb for ${version}"
echo "check it on a macOS host with:"
echo "    brew install --build-from-source ${out}/coddy.rb"
echo "    brew test coddy && brew audit --strict --online coddy"
