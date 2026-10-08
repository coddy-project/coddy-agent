#!/usr/bin/env bash
# Build the runtime image (Dockerfile) for every platform the release publishes
# and check that /bin/coddy inside each one is a binary of that platform.
#
# The release pushes a multi-arch manifest and nothing used to look inside it:
# for months the linux/arm64 variant carried an x86-64 binary (issue #482),
# because a default value on `ARG TARGETARCH` replaced the platform BuildKit
# passes. Docker reported the image as arm64, and a host with qemu-user-static
# even ran it, so only the ELF header of the binary tells the truth.
#
# Each platform is built on its own and its filesystem exported to a folder, so
# the check needs no registry and no multi-platform builder: the docker driver
# of a plain Docker install will do. The build stages run on the build
# platform, so no emulation is needed either. The binary of the host's own
# platform is also run and must print the version it was built with.
#
# Usage:
#   scripts/check-image.sh [options]
#
#   --platforms LIST  comma or space separated platforms
#                     (default: linux/amd64,linux/arm64, what the release pushes)
#   --version VER     version baked into the binary (default: `make -s print-version`)
#   --build-tags LIST BUILD_TAGS build argument (default: the Dockerfile's own)
#   --out DIR         where the image filesystems land, one folder per
#                     platform (default: dist/image)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

platforms="linux/amd64,linux/arm64"
version=""
build_tags=""
out="dist/image"

while [ $# -gt 0 ]; do
    case "$1" in
        --platforms) platforms="$2"; shift 2 ;;
        --version) version="$2"; shift 2 ;;
        --build-tags) build_tags="$2"; shift 2 ;;
        --out) out="$2"; shift 2 ;;
        -h|--help) sed -n '2,25p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$version" ]; then
    version=$(make -s print-version)
fi

build_args=(--build-arg "VERSION=$version")
if [ -n "$build_tags" ]; then
    build_args+=(--build-arg "BUILD_TAGS=$build_tags")
fi

# e_machine of a little-endian ELF header (bytes 18 and 19) per platform.
machine_of() {
    case "$1" in
        linux/amd64) echo "3e00" ;;
        linux/arm64) echo "b700" ;;
        *) return 1 ;;
    esac
}

machine_name() {
    case "$1" in
        3e00) echo "x86-64" ;;
        b700) echo "aarch64" ;;
        *) echo "e_machine $1" ;;
    esac
}

# The platform whose binaries this host runs natively, empty when none.
host_platform=""
if [ "$(uname -s)" = Linux ]; then
    case "$(uname -m)" in
        x86_64) host_platform="linux/amd64" ;;
        aarch64|arm64) host_platform="linux/arm64" ;;
    esac
fi

hex() {
    od -An -tx1 -j "$2" -N "$3" "$1" | tr -d ' \n'
}

failed=0
for platform in $(echo "$platforms" | tr ',' ' '); do
    want=$(machine_of "$platform") || {
        echo "check-image: no ELF machine known for $platform" >&2
        exit 2
    }
    dest="$out/$(echo "$platform" | tr '/' '_')"
    rm -rf "$dest"

    echo "check-image: building $platform"
    docker buildx build --platform "$platform" "${build_args[@]}" \
        --output "type=local,dest=$dest" .

    bin="$dest/bin/coddy"
    if [ ! -f "$bin" ]; then
        echo "check-image: $platform: the image has no /bin/coddy" >&2
        failed=1
        continue
    fi
    if [ "$(hex "$bin" 0 4)" != "7f454c46" ]; then
        echo "check-image: $platform: /bin/coddy is not an ELF binary" >&2
        failed=1
        continue
    fi
    got=$(hex "$bin" 18 2)
    if [ "$got" != "$want" ]; then
        echo "check-image: $platform: /bin/coddy is $(machine_name "$got"), want $(machine_name "$want")" >&2
        failed=1
        continue
    fi
    echo "check-image: $platform: /bin/coddy is $(machine_name "$got")"

    if [ "$platform" = "$host_platform" ]; then
        printed=$("$bin" --version)
        if [ "$printed" != "$version" ]; then
            echo "check-image: $platform: coddy --version printed '$printed', want '$version'" >&2
            failed=1
            continue
        fi
        echo "check-image: $platform: coddy --version prints $printed"
    fi
done

exit "$failed"
