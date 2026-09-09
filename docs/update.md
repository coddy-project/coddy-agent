# Updating Coddy

Use **`coddy update`** to download official release binaries from [GitHub Releases](https://github.com/coddy-project/coddy-agent/releases) and replace the **`coddy`** executable you are running.

On Windows, Coddy starts a short-lived helper from the system temporary directory. The helper waits for `coddy update` to exit, keeps a backup of the installed executable, replaces it, and starts the updated Coddy again. The helper's status lines continue in the same `cmd.exe` or PowerShell console after the `update` command returns, so `coddy update` reports success once the download is staged, not once the swap has happened.

Every download is checked against the `SHA256SUMS` asset the release publishes before anything is unpacked or installed. A release without that asset is reported and installed anyway.

## What gets installed

CI publishes one archive per platform on each SemVer tag **`X.Y.Z`**, plus Linux distribution packages:

| Archive | Platform |
|---------|----------|
| **`coddy_X.Y.Z_linux_amd64.tar.gz`** | Linux x86_64 |
| **`coddy_X.Y.Z_linux_arm64.tar.gz`** | Linux arm64 |
| **`coddy_X.Y.Z_windows_amd64.zip`** | Windows x86_64 (**`coddy.exe`**) |
| **`coddy_X.Y.Z_darwin_amd64.tar.gz`** | macOS Intel |
| **`coddy_X.Y.Z_darwin_arm64.tar.gz`** | macOS Apple Silicon |
| **`coddy_X.Y.Z_linux_amd64.deb`** | Debian, Ubuntu and derivatives, x86_64 |
| **`coddy_X.Y.Z_linux_arm64.deb`** | Debian, Ubuntu and derivatives, arm64 |
| **`coddy_X.Y.Z_linux_amd64.rpm`** | Fedora, RHEL, openSUSE and derivatives, x86_64 |
| **`coddy_X.Y.Z_linux_arm64.rpm`** | Fedora, RHEL, openSUSE and derivatives, arm64 |

The Linux and macOS archives carry the man page and the bash and zsh completions beside the binary; the packages install the same files where the system expects them - see [Install](install.md#linux-packages-deb-rpm). Every release also publishes **`coddy.rb`**, the Homebrew cask for the two macOS archives of that tag.

Each binary is built with **`http`**, **`ui`**, **`scheduler`**, and **`memory`** (same as **`make build TAGS="http ui scheduler memory"`** and the default Docker image). See [Build from source](build.md#release-binaries-ci) for the release pipeline.

## Which file is replaced

**`coddy update`** resolves **`os.Executable()`** (symlinks followed) and overwrites that path. Examples:

- After **`make install`** as a regular user, that is usually **`~/.local/bin/coddy`**.
- When you run **`./build/coddy update`**, it updates **`build/coddy`** in the repo.

This differs from **`make install`**, which always copies to **`~/.local/bin`** or **`/usr/local/bin`**. To update the binary on **`PATH`**, invoke the same **`coddy`** that **`which coddy`** prints.

## Installations owned by a package manager

A **`coddy`** that **`apt`** or **`dnf`** put on disk is listed in the package database, file by file. Overwriting **`/usr/bin/coddy`** in place would leave that database describing a build that is gone, the next **`apt upgrade`** or **`dnf reinstall`** would quietly put the old version back, and **`dpkg --verify`** would report a checksum mismatch nobody asked for. So **`coddy update`** does not replace a packaged executable. It asks **`dpkg-query -S`** and **`rpm -qf`** who owns the file it is about to write and takes one of two other routes:

- **as an ordinary user** - it changes nothing, names the package that owns the installation, and prints the command that upgrades it (**`sudo apt-get install --only-upgrade coddy`**, **`sudo dnf upgrade coddy`**, ...). The exit code is **1**, so a script notices;
- **as root** - it downloads the **`.deb`** or **`.rpm`** for this architecture, verifies it against **`SHA256SUMS`** like any other asset, and hands it to the package manager it found (**`apt-get`**, **`apt`**, **`dnf`**, **`yum`**, **`zypper`**, else **`dpkg`** or **`rpm`**). The package database stays correct.

**Homebrew** is recognised too, on macOS and on Linux: an executable that resolves into a **`Caskroom`** or **`Cellar`** directory belongs to brew. There is no privileged route there - Homebrew refuses to run under **`sudo`** - so both root and an ordinary user are pointed at **`brew upgrade --cask coddy`**.

```bash
sudo coddy update -y
```

Ownership is asked of the package database, not guessed from the path, so a binary you copied out of **`/usr/bin`** keeps updating itself, a distribution's own rebuild of Coddy is recognised like ours, and a build under **`~/.local/bin`** is untouched by any of this.

A release published before this pipeline has archives but no packages. Root is told exactly that and pointed back at the package manager, rather than being handed an archive that would overwrite the packaged file.

## Commands

Check for a newer release (exit **0** if up to date, **1** if a newer **`X.Y.Z`** exists):

```bash
coddy -v
coddy update --check
```

Install the latest release (prompt **`[y/N]`** unless **`-y`**):

```bash
coddy update
coddy update -y
```

Install a specific tag:

```bash
coddy update --version 0.9.3
coddy update --version 0.9.3 -y
```

Override the GitHub repository (default **`coddy-project/coddy-agent`**):

```bash
coddy update --repo coddy-project/coddy-agent
```

Install on Windows without starting Coddy again afterwards - useful from a script or a CI step, where the restarted process has no console to run in:

```bash
coddy update -y --no-restart
```

All flags:

```bash
coddy update --help
```

## Version comparison

**`coddy -v`** may show a git describe string (for example **`0.9.2-5-gb6b7d31-dirty`**). **`coddy update`** compares the leading **`X.Y.Z`** prefix to the release tag. A local **`dev`** build is treated as older than any published SemVer release.

## Other upgrade paths

| Method | When to use |
|--------|-------------|
| **`coddy update`** | You already have a release binary on disk and want the next (or a specific) GitHub release. |
| **`make install`** | You built from a clone and want **`build/coddy`** on **`PATH`**. |
| **`make build TAGS="..."`** | You need custom tags or local changes not in releases. |
| **Docker** | **`docker compose pull`** / image tag **`X.Y.Z`** on [GHCR](https://github.com/coddy-project/coddy-agent/pkgs/container/coddy-agent). |
| **`go install ...@latest`** | Quick install without release assets; default module tags only (no **`http`** / UI unless you build from source). |
| **`apt` / `dnf` / `zypper`** | You installed the **`.deb`** or **`.rpm`**; install the newer package file, or run **`sudo coddy update`** to have it fetched for you. |
| **`brew upgrade --cask coddy`** | You installed the Homebrew cask. |

## Limitations

- Only platforms listed in the release table are supported; others get a clear error.
- On Windows, Coddy waits up to 30 seconds for another process to release the executable. A permission failure reports much sooner and names the directory: installing into `Program Files` needs an elevated console. If the update cannot be installed, the current executable is left in place; if the updated binary cannot be started, Coddy restores the backup.
- The Windows helper deletes itself through the Coddy it just installed. Installing a release older than that handoff - `coddy update --version` walking backwards - starts the older build directly instead, and its helper stays in `%TEMP%` until the next `coddy update` sweeps it.
- Asset downloads resume after a temporary connection failure (up to three attempts). GitHub supports the HTTP range requests Coddy uses to resume; a server that does not support ranges is downloaded again from the beginning, and one that resumes at the wrong offset fails the download rather than installing a spliced archive.
- **`coddy update`** needs outbound HTTPS to **`api.github.com`** and the asset CDN (GitHub release downloads).
- The package route is Linux-only (Homebrew installs are reported, never upgraded by Coddy), and the package manager runs non-interactively, so an upgrade that would remove another package fails instead of asking. Run the package manager yourself in that case.
- Config under **`$CODDY_HOME`** is not modified; only the binary, or the package that carries it, is replaced.
