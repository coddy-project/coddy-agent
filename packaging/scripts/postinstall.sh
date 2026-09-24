#!/bin/sh
# Runs as root after the package is unpacked, on install and on upgrade.
# Coddy keeps every piece of state under the invoking user's $CODDY_HOME, so
# there is nothing to create system-wide here. The message points the user at
# the one file they do have to write themselves, and at the systemd user unit
# the package installed and deliberately did not enable: which accounts run a
# server is for each user to decide, with `coddy serve setup`.
set -e

# Debian passes "configure" and, on an upgrade, the version it replaces; rpm
# passes the number of installed copies, 1 on a first install.
case "${1:-}" in
    1) event=install ;;
    configure) if [ -n "${2:-}" ]; then event=upgrade; else event=install; fi ;;
    [0-9]*) event=upgrade ;;
    *) exit 0 ;;
esac

user_home=""
if [ -n "${SUDO_USER:-}" ]; then
    user_home="$(getent passwd "${SUDO_USER}" | cut -d: -f6)"
fi

if [ "$event" = upgrade ]; then
    # An enabled service keeps running the binary it started with, and an
    # upgrade from a release without the unit is where most users first meet
    # it. Which accounts enabled it is not something root can see from here
    # (each user manager has its own config home), so this goes to everyone.
    cat <<'EOF'

Coddy is upgraded. The systemd user unit /usr/lib/systemd/user/coddy.service
is installed and not enabled by the package. As the user the service is for,
without sudo:

    coddy serve setup   # enable and start it, or restart it on the new binary

EOF
    exit 0
fi

if [ -z "$user_home" ] || [ ! -f "$user_home/.coddy/config.yaml" ]; then
    cat <<'EOF'

Coddy is installed. Create your configuration:

    mkdir -p ~/.coddy
    cp /usr/share/doc/coddy/config.example.yaml ~/.coddy/config.yaml

Then set a provider key in it and start a surface:

    coddy               # interactive console
    coddy serve         # every subsystem config.yaml enables (web UI on by default)
EOF
else
    cat <<'EOF'

Coddy is installed, and ~/.coddy/config.yaml is already there.
EOF
fi

cat <<'EOF'

The systemd user unit /usr/lib/systemd/user/coddy.service is installed but
NOT enabled. To run coddy serve as a service for your account (it works in
~/Coddy and comes back after a crash), run as that user, without sudo:

    coddy serve setup       # enable and start coddy.service
    coddy serve uninstall   # stop and disable it again

Manual: man coddy   Service guide: https://coddy.dev/docs/operate/serve

EOF

exit 0
