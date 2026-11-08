#!/usr/bin/env bash
# Build the Linux packages of the desktop application (deb and rpm).
#
# The window is a verb of the ordinary binary (`coddy desktop`), so this
# package carries no binary at all: it installs what makes Coddy an
# application on a Linux desktop - the entry in /usr/share/applications and
# the icon it names - and depends on `coddy` for the program itself. That is
# why it is a package of its own rather than files added to the agent's: a
# server that only runs `coddy serve` has no use for a menu entry.
#
# Usage:
#   scripts/build-desktop-packages.sh [options]
#
#   --arch LIST       comma or space separated GOARCH list (default: amd64)
#   --version VER     raw version to package (default: `make -s print-version`)
#   --formats LIST    package formats (default: deb,rpm)
#   --out DIR         where the packages land (default: dist)
#
# Environment:
#   NFPM          nfpm command to use (default: nfpm on PATH, else `go run`)
#   NFPM_VERSION  pinned nfpm module version for the `go run` fallback
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

archs="amd64"
formats="deb,rpm"
out="dist"
version=""

while [ $# -gt 0 ]; do
    case "$1" in
        --arch|--archs) archs="$2"; shift 2 ;;
        --version) version="$2"; shift 2 ;;
        --formats) formats="$2"; shift 2 ;;
        --out) out="$2"; shift 2 ;;
        -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

archs=$(echo "$archs" | tr ',' ' ')
formats=$(echo "$formats" | tr ',' ' ')

NFPM_VERSION="${NFPM_VERSION:-v2.45.0}"

if [ -z "$version" ]; then
    version=$(make -s print-version)
fi
pkg_version=$("$root/scripts/package-version.sh" "$version")

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
# The icon is the application's own (packaging/desktop/coddy-desktop.svg and
# the sizes rendered from it): the same mark as the web interface's favicon,
# drawn to survive the 32 and 48 pixels an application menu gives it.
cp packaging/desktop/coddy-desktop.desktop "$stage/coddy-desktop.desktop"
cp packaging/desktop/coddy-desktop.svg "$stage/coddy-desktop.svg"
for size in 512 256 128 64 48; do
    cp "packaging/desktop/icons/coddy-desktop-${size}.png" "$stage/coddy-desktop-${size}.png"
done
cp LICENSE "$stage/LICENSE"

# The package has no binary in it, so an architecture only names the package.
# It is still built per architecture, because the dependency on `coddy` is
# satisfied by a package of that architecture.
for arch in $archs; do
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
