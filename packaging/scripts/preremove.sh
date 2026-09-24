#!/bin/sh
# Runs as root before the package's files are removed, on removal and on
# upgrade. The package enabled nothing, so it disables nothing either: an
# account that ran `coddy serve setup` has its own link to the unit under its
# own ~/.config, which a package script has no business editing. What it can
# do is say so while the account can still act on it.
set -e

# Debian passes "remove" (or "upgrade" on an upgrade); rpm passes the number of
# copies left, 0 on a removal.
case "${1:-}" in
    remove|0) ;;
    *) exit 0 ;;
esac

cat <<'EOF'

Removing Coddy. The systemd user unit coddy.service goes with the package,
but an account that enabled it with `coddy serve setup` keeps it enabled.
As each such user, stop and disable it and drop what setup left behind:

    systemctl --user disable --now coddy.service
    rm -f ~/.config/systemd/user/coddy.service.d/coddy-setup.conf

~/.coddy (configuration, sessions) and ~/Coddy (workspace) are left in place.

EOF

exit 0
