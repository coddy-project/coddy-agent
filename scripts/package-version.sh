#!/usr/bin/env bash
# Turn the repository version string into one deb and rpm both accept.
#
# `make print-version` yields a plain tag on a release (1.0.8) but a git
# describe elsewhere (1.0.8-5-gb6b7d31-dirty). rpm forbids "-" in a version
# entirely, and dpkg reads the last "-" as the start of the Debian revision, so
# the describe form has to be folded into the upstream part. "+" is legal in
# both and sorts after the bare tag, which is what a build made after that tag
# should do.
#
#   1.0.8                  -> 1.0.8
#   v1.0.8                 -> 1.0.8
#   1.0.8-5-gb6b7d31-dirty -> 1.0.8+5.gb6b7d31.dirty
#   dev                    -> 0.0.0+dev
#
# Usage: scripts/package-version.sh <version>
set -euo pipefail

raw="${1:-}"
if [ -z "$raw" ]; then
    echo "usage: $0 <version>" >&2
    exit 2
fi

raw="${raw#v}"

# Split the leading X.Y.Z from whatever git appended to it.
if [[ "$raw" =~ ^([0-9]+\.[0-9]+\.[0-9]+)(.*)$ ]]; then
    core="${BASH_REMATCH[1]}"
    rest="${BASH_REMATCH[2]}"
else
    core="0.0.0"
    rest="-${raw}"
fi

if [ -z "$rest" ]; then
    echo "$core"
    exit 0
fi

# Everything after the release version becomes one build suffix: strip the
# separator git used, then reduce the rest to dot-joined alphanumerics.
rest="${rest#-}"
rest=$(echo "$rest" | tr -c '[:alnum:]' '.' | sed 's/\.\{1,\}/./g; s/^\.//; s/\.$//')

if [ -z "$rest" ]; then
    echo "$core"
else
    echo "${core}+${rest}"
fi
