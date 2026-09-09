#!/usr/bin/env bash
# Render the Homebrew cask for one release.
#
# A cask pins the checksum of every archive it installs, so this needs the
# actual macOS archives of the version being packaged. It uses the ones in the
# output directory when they are there - which is the case inside the release
# job, right after the cross-compile - and downloads them from the GitHub
# release otherwise.
#
# Usage:
#   scripts/build-homebrew-cask.sh [options]
#
#   --version VER   release to render (default: `make -s print-version`)
#   --out DIR       where coddy.rb lands, and where archives are looked for
#                   (default: dist)
#   --repo OWNER/N  GitHub repository to download archives from
#                   (default: coddy-project/coddy-agent)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

out="dist"
version=""
repo="coddy-project/coddy-agent"

while [ $# -gt 0 ]; do
    case "$1" in
        --version) version="$2"; shift 2 ;;
        --out) out="$2"; shift 2 ;;
        --repo) repo="$2"; shift 2 ;;
        -h|--help) sed -n '2,17p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$version" ]; then
    version=$(make -s print-version)
fi

mkdir -p "$out"
out=$(cd "$out" && pwd)

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# checksum_for prints the sha256 of one macOS archive, taking it from the output
# directory when it is already there.
checksum_for() {
    local arch="$1"
    local name="coddy_${version}_darwin_${arch}.tar.gz"
    local file="${out}/${name}"

    if [ ! -f "$file" ]; then
        file="${work}/${name}"
        echo "downloading ${name}" >&2
        if ! curl -fsSL -o "$file" "https://github.com/${repo}/releases/download/${version}/${name}"; then
            echo "no ${name} in ${out} and none published at ${version}" >&2
            echo "build the release archives first, or pass --version of a published release" >&2
            exit 1
        fi
    fi
    sha256sum "$file" | cut -d' ' -f1
}

sha_arm64=$(checksum_for arm64)
sha_amd64=$(checksum_for amd64)

# The template header explains how the file is rendered; the published cask
# starts at the `cask` line and carries none of it.
sed \
    -e "s|__VERSION__|${version}|g" \
    -e "s|__SHA256_ARM64__|${sha_arm64}|g" \
    -e "s|__SHA256_AMD64__|${sha_amd64}|g" \
    packaging/homebrew/coddy.rb.tmpl |
    sed -n '/^cask /,$p' > "${out}/coddy.rb"

echo "wrote ${out}/coddy.rb for ${version}"
echo "install it locally with: brew install --cask ${out}/coddy.rb"
