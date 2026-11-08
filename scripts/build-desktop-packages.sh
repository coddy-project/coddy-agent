#!/usr/bin/env bash
# Build the Linux packages of the desktop window (deb and rpm).
#
# A package of its own, coddy-desktop, beside the agent's: the window carries
# the embedded web UI and is built with its own tags, and the two share no
# file. Beyond the binary it installs what makes this an application on a Linux
# desktop - the entry in /usr/share/applications and the icon it names - so
# Coddy can be started from the menu.
#
# Usage:
#   scripts/build-desktop-packages.sh [options]
#
#   --arch LIST       comma or space separated GOARCH list (default: amd64)
#   --version VER     raw version to package (default: `make -s print-version`)
#   --formats LIST    package formats (default: deb,rpm)
#   --out DIR         where the packages land (default: dist)
#   --binary PATH     use this coddy-desktop binary instead of building one
#                     (only valid with a single --arch)
#
# Environment:
#   TAGS          go build tags for the binary built here
#                 (default: the desktop set, `make print-desktop-tags`)
#   NFPM          nfpm command to use (default: nfpm on PATH, else `go run`)
#   NFPM_VERSION  pinned nfpm module version for the `go run` fallback
#
# The embedded SPA has to exist before a binary claims to carry it: `make
# deb-desktop` runs ui-build first. Calling this script by hand on a tree that
# never built the UI packages a window with no web interface in it.
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
        -h|--help) sed -n '2,27p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

archs=$(echo "$archs" | tr ',' ' ')
formats=$(echo "$formats" | tr ',' ' ')

TAGS="${TAGS:-$(make -s -C "$root" print-desktop-tags | tr , ' ')}"
NFPM_VERSION="${NFPM_VERSION:-v2.45.0}"

if [ -z "$version" ]; then
    version=$(make -s print-version)
fi
pkg_version=$("$root/scripts/package-version.sh" "$version")

if [ -n "$binary" ] && [ "$(echo "$archs" | wc -w)" -ne 1 ]; then
    echo "--binary takes a single --arch, got: $archs" >&2
    exit 2
fi

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
# The icon is the web interface's own, taken from where it already lives rather
# than copied into the tree a second time: one logo, one place to change it.
cp packaging/desktop/coddy-desktop.desktop "$stage/coddy-desktop.desktop"
cp external/ui/public/coddy-favicon.svg "$stage/coddy-desktop.svg"
cp external/ui/public/icon-512.png "$stage/coddy-desktop-512.png"
cp LICENSE "$stage/LICENSE"

for arch in $archs; do
    if [ -n "$binary" ]; then
        cp "$binary" "$stage/coddy-desktop"
    else
        echo "building coddy-desktop for linux/${arch} with tags \"${TAGS}\""
        revision=$(git rev-parse --verify -q HEAD 2>/dev/null || true)
        GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build \
            -tags "$TAGS" \
            -trimpath \
            -ldflags "-s -w -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=${version} -X github.com/EvilFreelancer/coddy-agent/internal/version.Revision=${revision}" \
            -o "$stage/coddy-desktop" \
            ./cmd/coddy-desktop/
    fi
    chmod 0755 "$stage/coddy-desktop"

    for format in $formats; do
        target="${out}/coddy-desktop_${version}_linux_${arch}.${format}"
        echo "packaging ${target}"
        (
            cd "$stage"
            PKG_ARCH="$arch" PKG_VERSION="$pkg_version" \
                $nfpm package \
                    --config "${root}/packaging/nfpm-desktop.yaml" \
                    --packager "$format" \
                    --target "$target"
        )
    done
done

ls -la "$out"
