# FreeBSD

Coddy runs on FreeBSD from a release built for it: **`coddy_X.Y.Z_freebsd_amd64.tar.gz`** for
x86-64 and **`coddy_X.Y.Z_freebsd_arm64.tar.gz`** for 64-bit ARM. It is the whole Coddy - the
console, **`coddy serve`** with the web UI, the Telegram and Pachca bots, the swarm relay, the
Prometheus exporter, **`coddy acp`** and the desktop window - built from the same tag set as the
Linux and macOS archives.

The binary is pure Go and static: it needs no port installed beside it, and it runs on FreeBSD 13
and later. The parts of Coddy that talk to the kernel use the BSD interfaces rather than Linux's,
which is the only thing this page has to explain.

## Install

The install script recognises FreeBSD and fetches the right archive. It is a bash script and uses
**`curl`**, neither of which is in the base system:

```sh
pkg install -y bash curl
curl -fsSL https://coddy.dev/install.sh | bash
```

It installs **`coddy`** into **`~/.local/bin`**, the man page and the completions into
**`~/.local/share`**, and writes the block that puts them on **`PATH`** and **`MANPATH`** into the
rc file of the shell **`$SHELL`** names: **`~/.profile`** for FreeBSD's default **`sh`**,
**`~/.bashrc`** for bash, **`~/.zshrc`** for zsh.

By hand, with **`X.Y.Z`** replaced by the release you want and **`amd64`** by **`arm64`** when
**`uname -m`** says **`arm64`**:

```sh
fetch https://github.com/coddy-project/coddy-agent/releases/download/X.Y.Z/coddy_X.Y.Z_freebsd_amd64.tar.gz
tar -xzf coddy_X.Y.Z_freebsd_amd64.tar.gz
mkdir -p ~/.local/bin ~/.local/share/man/man1
install -m 0755 coddy ~/.local/bin/coddy
install -m 0644 coddy.1 ~/.local/share/man/man1/coddy.1
```

The first run needs a provider key in **`~/.coddy/config.yaml`**, as on any other system; see
[Configuration](configuration.md). **`coddy update`** replaces the binary in place from the FreeBSD
archive of the next release, the man page and the completions beside it included; see
[Update](update.md).

There is no FreeBSD package or port yet, so **`coddy update`** does not ask **`pkg`** who owns the
file it is about to replace the way it asks **`dpkg`** and **`rpm`** on Linux. Install into your own
prefix, as above, rather than over a file a port put in **`/usr/local/bin`**.

## Running `coddy serve`

**`coddy serve`** works as it does elsewhere, and the web UI opens at
**`http://127.0.0.1:12345/`**. FreeBSD has no systemd, so **`coddy serve install`** is not
available and says so. Three routes are left:

- **`coddy serve --daemon`**, which keeps one process up and restarts it after a configuration
  change that needs a fresh one. **`coddy serve status | stop | restart`** drive it. This is the
  usual choice on a workstation;
- **`tmux`** (**`pkg install -y tmux`**), for a console session that outlives the terminal or a
  dropped SSH connection;
- an **`rc.d`** script of your own, for a machine where the agent should come back after a reboot.
  Coddy's state is per user under **`~/.coddy`**, so the service runs as that user and not as root.
  A short one, in **`/usr/local/etc/rc.d/coddy`**:

  ```sh
  #!/bin/sh
  #
  # PROVIDE: coddy
  # REQUIRE: LOGIN
  # KEYWORD: shutdown
  . /etc/rc.subr

  name="coddy"
  rcvar="coddy_enable"
  load_rc_config $name
  : ${coddy_enable:="NO"}
  : ${coddy_user:="coddy"}
  : ${coddy_home:="/home/coddy"}

  pidfile="/var/run/${name}.pid"
  command="/usr/sbin/daemon"
  command_args="-p ${pidfile} -u ${coddy_user} -r \
      /home/coddy/.local/bin/coddy serve"
  run_rc_command "$1"
  ```

  Then **`sysrc coddy_enable=YES`** and **`service coddy start`**. **`-r`** is what restarts the
  process if it exits, which is the part **`--daemon`** does for itself.

See [coddy serve](../operate/serve.md) for everything the server does once it is up.

## The desktop window

**`coddy desktop`** opens its window on FreeBSD the way it does on Linux: a Chromium-family browser
from the ports tree (**`www/chromium`**, **`www/ungoogled-chromium`** and the rest) in application
mode, in a profile of its own under **`~/.coddy/desktop/profiles/`**. Under X11 the system frame
comes off, the corners are cut and the tab strip moves the window; under Wayland the window keeps
the frame its compositor draws, as on Linux. The tray icon is a StatusNotifierItem on the session
bus, and **Open at login** writes the XDG autostart entry into **`~/.config/autostart`**.

See [Desktop window](../surfaces/desktop.md).

## What is different from Linux

Nothing needs configuring. These are the places where the FreeBSD build takes another route, so
that a report about one of them has somewhere to point:

- **Free disk space**, shown on the status page and by **`--dry-run`**, comes from **`statfs`** with
  **`f_bsize`**, where Linux uses **`f_frsize`**;
- **the local control socket** verifies that the process on the other end runs as the same user,
  through **`LOCAL_PEERCRED`** and a **`struct xucred`**, where Linux reads **`SO_PEERCRED`**. This
  is the check that keeps another account on the machine out of a running agent;
- **the Kimi credential store** takes the same locked, crash-durable path as on Linux and macOS:
  owner and permission checks on the store directory, **`flock`** around a refresh and a directory
  flush after it;
- **the desktop window** finds the browser process it started by walking up the process tree
  through the **`kern.proc.pid`** sysctl, because **`procfs`** is not mounted on a FreeBSD system by
  default and there is no **`/proc`** to read;
- **opening a URL or a folder** - a provider sign-in, or showing a file the agent produced in the
  desktop file manager - goes through **`xdg-open`** (**`devel/xdg-utils`**), as on Linux.

## Limitations

- Only the two 64-bit architectures are published, **`amd64`** and **`arm64`**;
- there is no FreeBSD package or port, and no **`pkg`** route to install or update Coddy;
- **`coddy serve install`** needs systemd and is therefore Linux only; the section above has what
  to use instead;
- the **`.deb`** and **`.rpm`** packages, the Homebrew cask and the container image are Linux and
  macOS artefacts and have no FreeBSD counterpart.

## Building it yourself

A FreeBSD machine builds Coddy the way any other does, with Go and, for the embedded web UI, Node:

```sh
pkg install -y go node npm gmake git
gmake build TAGS="http ui scheduler memory cli gateway swarm metrics browser desktop"
```

**`gmake`** is GNU make, which the **`Makefile`** needs; FreeBSD's own **`make`** will not read it.

From any host, **`make check-freebsd`** type-checks the FreeBSD build for both published
architectures with the test files included, and **`make lint-freebsd`** runs the linter over it. CI
runs both on every pull request and then runs the platform suites on a real FreeBSD; see
[Build from source](../contributing/build.md#freebsd).
