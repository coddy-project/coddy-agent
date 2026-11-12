#!/usr/bin/env bash
# Build the two macOS release archives with the native desktop backend.
# Usage: scripts/build-darwin-archives.sh [--version VER] [--out DIR]
# Defaults: version from `make -s print-version`, output in dist.
set -euo pipefail
unset USER

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

version=""
out="dist"
while [ $# -gt 0 ]; do
    case "$1" in
        --version) version="$2"; shift 2 ;;
        --out) out="$2"; shift 2 ;;
        -h|--help) sed -n '2,4p' "$0"; exit 0 ;;
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

export CGO_ENABLED=1
export MACOSX_DEPLOYMENT_TARGET=13.0
export CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=13.0 -Werror=unguarded-availability-new"
export COPYFILE_DISABLE=1
DARWIN_TAGS="$(make -s print-darwin-tags)"
LDFLAGS="-s -w -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=${version}"

for arch in arm64 amd64; do
    stem="coddy_${version}_darwin_${arch}"
    bindir="$work/$stem"
    mkdir -p "$bindir"
    cc="clang -arch arm64"
    if [ "$arch" = "amd64" ]; then cc="clang -arch x86_64"; fi
    GOOS=darwin GOARCH="$arch" CC="$cc" go build \
        -tags="${DARWIN_TAGS}" -trimpath -ldflags "$LDFLAGS" \
        -o "$bindir/coddy" ./cmd/coddy/
    cp packaging/man/coddy.1 "$bindir/coddy.1"
    cp packaging/completions/coddy.bash "$bindir/coddy.bash"
    cp packaging/completions/coddy.zsh "$bindir/coddy.zsh"
    file "$bindir/coddy"
    xcrun vtool -show-build "$bindir/coddy"
    tar --no-xattrs --no-mac-metadata -C "$bindir" -czf "$out/${stem}.tar.gz" \
        coddy coddy.1 coddy.bash coddy.zsh
done
