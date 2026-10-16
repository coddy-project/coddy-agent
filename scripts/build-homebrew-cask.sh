#!/usr/bin/env bash
# Render the Homebrew cask for one release.
#
# A cask pins the checksum of every archive it installs. Inside the release job
# the macOS archives have just been cross-compiled into the output directory,
# and this hashes them there. Anywhere else it reads the two checksums from the
# SHA256SUMS that release published - the list `coddy update` verifies against -
# rather than downloading both archives only to hash them; brew then checks the
# one it downloads against the cask.
#
# Usage:
#   scripts/build-homebrew-cask.sh [options]
#
#   --version VER      release to render (default: `make -s print-version`)
#   --out DIR          where coddy.rb lands, and where archives are looked for
#                      (default: dist)
#   --repo OWNER/N     GitHub repository of the release
#                      (default: coddy-project/coddy-agent)
#   --release-url URL  where the assets of releases are downloaded from
#                      (default: https://github.com/OWNER/N/releases/download)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

out="dist"
version=""
repo="coddy-project/coddy-agent"
release_url=""

while [ $# -gt 0 ]; do
    case "$1" in
        --version) version="$2"; shift 2 ;;
        --out) out="$2"; shift 2 ;;
        --repo) repo="$2"; shift 2 ;;
        --release-url) release_url="$2"; shift 2 ;;
        -h|--help) sed -n '2,21p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$version" ]; then
    version=$(make -s print-version)
fi
release_url="${release_url:-https://github.com/${repo}/releases/download}"

mkdir -p "$out"
out=$(cd "$out" && pwd)

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# sha256_of prints the SHA-256 of one file. macOS always ships shasum and not
# every release ships sha256sum, and this runs there as well as on the Linux
# release runner.
sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1
    fi
}

# checksum_for prints the sha256 of one macOS archive: hashed in the output
# directory when the archive is there, read from the release's SHA256SUMS
# otherwise.
checksum_for() {
    local arch="$1"
    local name="coddy_${version}_darwin_${arch}.tar.gz"
    local file="${out}/${name}"

    if [ -f "$file" ]; then
        sha256_of "$file"
        return
    fi
    if [ ! -f "${work}/SHA256SUMS" ]; then
        echo "reading SHA256SUMS of ${version}" >&2
        if ! curl -fsSL -o "${work}/SHA256SUMS" "${release_url}/${version}/SHA256SUMS"; then
            echo "no ${name} in ${out} and no SHA256SUMS published at ${version}" >&2
            echo "build the release archives first, or pass --version of a published release" >&2
            exit 1
        fi
    fi
    local sum
    sum=$(awk -v name="$name" '$2 == name || $2 == "*" name { print $1 }' "${work}/SHA256SUMS")
    if [[ ! "$sum" =~ ^[0-9a-f]{64}$ ]]; then
        echo "the SHA256SUMS of ${version} lists no ${name}" >&2
        exit 1
    fi
    echo "$sum"
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
# Current Homebrew refuses a cask given by its path, so a local try goes through
# a throwaway tap.
echo "install it with a real brew (macOS): scripts/check-homebrew-cask.sh --cask ${out}/coddy.rb"
