#!/usr/bin/env bash
# Build the Linux packages published on a release tag (deb and rpm).
#
# The binary is built here unless --binary hands one over, which is what CI
# does: the release workflow has already cross-compiled every target, and
# rebuilding them would double the slowest step of the release for nothing.
#
# Usage:
#   scripts/build-packages.sh [options]
#
#   --arch LIST       comma or space separated GOARCH list (default: amd64)
#   --version VER     raw version to package (default: `make -s print-version`)
#   --formats LIST    package formats (default: deb,rpm)
#   --out DIR         where the packages land (default: dist)
#   --binary PATH     use this coddy binary instead of building one
#                     (only valid with a single --arch)
#
# Environment:
#   TAGS          go build tags for the binary built here
#                 (default: "http ui scheduler memory cli", the release set)
#   NFPM          nfpm command to use (default: nfpm on PATH, else `go run`)
#   NFPM_VERSION  pinned nfpm module version for the `go run` fallback
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

archs="amd64"
formats="deb,rpm"
out="dist"
version=""
binary=""

while [ $# -gt 0 ]; do
    case "$1" in
        --arch|--archs) archs="$2"; shift 2 ;;
        --version) version="$2"; shift 2 ;;
        --formats) formats="$2"; shift 2 ;;
        --out) out="$2"; shift 2 ;;
        --binary) binary="$2"; shift 2 ;;
        -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

archs=$(echo "$archs" | tr ',' ' ')
formats=$(echo "$formats" | tr ',' ' ')

TAGS="${TAGS:-http ui scheduler memory cli}"
NFPM_VERSION="${NFPM_VERSION:-v2.45.0}"

if [ -z "$version" ]; then
    version=$(make -s print-version)
fi
pkg_version=$("$root/scripts/package-version.sh" "$version")

if [ -n "$binary" ] && [ "$(echo "$archs" | wc -w)" -ne 1 ]; then
    echo "--binary takes a single --arch, got: $archs" >&2
    exit 2
fi

# nfpm is a single static binary and is not a module dependency of this repo:
# take it from PATH when it is there, otherwise run the pinned version.
if [ -n "${NFPM:-}" ]; then
    nfpm=$NFPM
elif command -v nfpm >/dev/null 2>&1; then
    nfpm=nfpm
else
    nfpm="go run github.com/goreleaser/nfpm/v2/cmd/nfpm@${NFPM_VERSION}"
fi

mkdir -p "$out"
out=$(cd "$out" && pwd)

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

# Everything except the binary is architecture-independent, so stage it once.
gzip -9 -n -c packaging/man/coddy.1 > "$stage/coddy.1.gz"
cp packaging/completions/coddy.bash "$stage/coddy.bash"
cp packaging/completions/coddy.zsh "$stage/coddy.zsh"
cp packaging/scripts/postinstall.sh "$stage/postinstall.sh"
cp config.example.yaml "$stage/config.example.yaml"
cp LICENSE "$stage/LICENSE"

for arch in $archs; do
    if [ -n "$binary" ]; then
        cp "$binary" "$stage/coddy"
    else
        echo "building coddy for linux/${arch} with tags \"${TAGS}\""
        GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build \
            -tags "$TAGS" \
            -trimpath \
            -ldflags "-s -w -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=${version}" \
            -o "$stage/coddy" \
            ./cmd/coddy/
    fi
    chmod 0755 "$stage/coddy"

    for format in $formats; do
        target="${out}/coddy_${version}_linux_${arch}.${format}"
        echo "packaging ${target}"
        # nfpm globs contents relative to its working directory, so it runs
        # where the staged files are and takes the recipe by absolute path.
        (
            cd "$stage"
            PKG_ARCH="$arch" PKG_VERSION="$pkg_version" \
                $nfpm package \
                    --config "${root}/packaging/nfpm.yaml" \
                    --packager "$format" \
                    --target "$target"
        )
    done
done

ls -la "$out"
