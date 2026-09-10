#!/bin/sh
# Runs as root after the package is unpacked, on install and on upgrade.
# Coddy keeps every piece of state under the invoking user's $CODDY_HOME, so
# there is nothing to create system-wide here: the message points the user at
# the one file they do have to write themselves.
set -e

# Debian passes "configure", rpm passes the number of installed copies. Only the
# first install needs the hint; an upgrade already has a config.
case "${1:-}" in
    1|configure)
        if [ -n "${SUDO_USER:-}" ] && [ -f "$(getent passwd "${SUDO_USER}" | cut -d: -f6)/.coddy/config.yaml" ]; then
            exit 0
        fi
        cat <<'EOF'

Coddy is installed. Create your configuration:

    mkdir -p ~/.coddy
    cp /usr/share/doc/coddy/config.example.yaml ~/.coddy/config.yaml

Then set a provider key in it and start a surface:

    coddy               # interactive console
    coddy serve         # every subsystem config.yaml enables (web UI on by default)

Manual: man coddy   Docs: https://coddy.dev

EOF
        ;;
esac

exit 0
