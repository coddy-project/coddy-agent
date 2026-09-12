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

Each binary is built with **`http`**, **`ui`**, **`scheduler`**, and **`memory`** (same as **`make build TAGS="http ui scheduler memory"`** and the default Docker image). See [Build from source](../contributing/build.md#release-binaries-ci) for the release pipeline.

## Which file is replaced

**`coddy update`** resolves **`os.Executable()`** (symlinks followed) and overwrites that path. Examples:

- After **`make install`** as a regular user, that is usually **`~/.local/bin/coddy`**.
- When you run **`./build/coddy update`**, it updates **`build/coddy`** in the repo.

This differs from **`make install`**, which always copies to **`~/.local/bin`** or **`/usr/local/bin`**. To update the binary on **`PATH`**, invoke the same **`coddy`** that **`which coddy`** prints.

## The man page and the completions beside it

The Linux and macOS archives carry **`coddy.1`**, **`coddy.bash`** and **`coddy.zsh`** beside the binary, and the install script puts them into the **`share`** directory of the binary's prefix (**`~/.local/bin`** -> **`~/.local/share`**, **`/usr/local/bin`** -> **`/usr/local/share`**; see [Install](install.md)). **`coddy update`** refreshes every one of those files it finds there, right after the executable, so **`man coddy`** and Tab completion describe the release that is running. A completer left at the previous release keeps offering commands the binary no longer has - that is how a completer went on listing **`http`** and **`gateway`** without **`serve`** after the binary had moved on ([issue #188](https://github.com/coddy-project/coddy-agent/issues/188)).

Nothing is created: a file that was never installed (**`--no-shell-setup`**, a binary copied by hand) is left alone, and an executable outside a **`bin`** directory - a build tree, a bare download - has no **`share`** directory to pair with. A release from before the archives carried those files leaves the installed copies as they are and says so. A file it cannot write is reported after the binary is installed, and the command exits non-zero.

## Installations owned by a package manager

A **`coddy`** that **`apt`** or **`dnf`** put on disk is listed in the package database, file by file. Overwriting **`/usr/bin/coddy`** in place would leave that database describing a build that is gone, the next **`apt upgrade`** or **`dnf reinstall`** would quietly put the old version back, and **`dpkg --verify`** would report a checksum mismatch nobody asked for. So **`coddy update`** does not replace a packaged executable. It asks **`dpkg-query -S`** and **`rpm -qf`** who owns the file it is about to write and takes one of two other routes:

- **as an ordinary user** - it changes nothing, names the package that owns the installation, and prints the command that upgrades it (**`sudo apt-get install --only-upgrade coddy`**, **`sudo dnf upgrade coddy`**, ...). The exit code is **1**, so a script notices;
- **as root** - it downloads the **`.deb`** or **`.rpm`** for this architecture, verifies it against **`SHA256SUMS`** like any other asset, and hands it to the package manager it found (**`apt-get`**, **`apt`**, **`dnf`**, **`yum`**, **`zypper`**, else **`dpkg`** or **`rpm`**). The package database stays correct.

**Homebrew** is recognised too, on macOS and on Linux: an executable that resolves into a **`Caskroom`** or **`Cellar`** directory belongs to brew. There is no privileged route there - Homebrew refuses to run under **`sudo`** - so both root and an ordinary user are pointed at **`brew upgrade`**. Which of the two markers matched decides the flag: a **`Caskroom`** path is a cask and takes **`brew upgrade --cask coddy`**, a **`Cellar`** path is a formula and takes **`brew upgrade coddy`**. The distinction is not cosmetic - **`--cask`** on a formula install fails, there being no cask by that name to upgrade.

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

Install without the report of what changed (see [What changed](#what-changed)), for a script that only wants the install lines:

```bash
coddy update -y --no-notes
CODDY_UPDATE_NOTES=0 coddy update -y
```

All flags:

```bash
coddy update --help
```

## What changed

![The report after an update: every skipped release with its notes and the comparison link](../assets/update-report-dark-1920.png)

*The report after an update: every skipped release with its notes and the comparison link*

Once the update is in, **`coddy update`** says what it brought. It lists every release between the version that was running and the one it installed, oldest first, each with the day it was published and its release notes, and ends with the GitHub comparison for the whole range ([issue #195](https://github.com/coddy-project/coddy-agent/issues/195)):

```console
Installed 1.0.37 (/home/user/.local/bin/coddy)

Changes since 1.0.35:
1.0.36 (2026-09-11)
  - fix(update): refresh the man page and the shell completions beside the binary (#193)
1.0.37 (2026-09-11)
  - feat(config): --dry-run probes what config.yaml points at before anything starts (#194)
Full changelog: https://github.com/coddy-project/coddy-agent/compare/1.0.35...1.0.37
```

The notes are the release bodies GitHub holds, trimmed for a terminal: the generated *What's Changed* heading, the author trailer and the *Full Changelog* footer of each release go, the pull request number stays as **`(#N)`**, and a hand-written body keeps its sections as plain titles. The report is capped at 20 lines, so a long gap ends in **`... N more lines`** and the comparison link, which covers every release at once.

The report is printed on every route that installs something - the archive, the Windows helper handoff, and the **`.deb`** / **`.rpm`** route as root - and never on **`--check`** or when Coddy is already up to date. A build with no release to count from (**`coddy -v`** prints **`dev`**), and a **`--version`** that walks backwards, get the notes of the release just installed and the link to its page instead of a range.

Fetching the list is a second request to the GitHub API, bounded to 15 seconds, and it cannot fail the update: offline, rate-limited or answered with anything but a list, the report shrinks to the **`Full changelog:`** line alone. **`--no-notes`** skips it entirely, and so does **`CODDY_UPDATE_NOTES=0`** (also **`false`**, **`no`** or **`off`**) in the environment, for scripts and CI steps that only want the install lines.

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
| **`brew upgrade --cask coddy`** | You installed the Homebrew cask (the executable resolves into a **`Caskroom`**). |
| **`brew upgrade coddy`** | You installed a Homebrew formula (the executable resolves into a **`Cellar`**). |

## Limitations

- Only platforms listed in the release table are supported; others get a clear error.
- On Windows, Coddy waits up to 30 seconds for another process to release the executable. A permission failure reports much sooner and names the directory: installing into `Program Files` needs an elevated console. If the update cannot be installed, the current executable is left in place; if the updated binary cannot be started, Coddy restores the backup.
- The Windows helper deletes itself through the Coddy it just installed. Installing a release older than that handoff - `coddy update --version` walking backwards - starts the older build directly instead, and its helper stays in `%TEMP%` until the next `coddy update` sweeps it.
- Asset downloads resume after a temporary connection failure (up to three attempts). GitHub supports the HTTP range requests Coddy uses to resume; a server that does not support ranges is downloaded again from the beginning, and one that resumes at the wrong offset fails the download rather than installing a spliced archive.
- **`coddy update`** needs outbound HTTPS to **`api.github.com`** and the asset CDN (GitHub release downloads).
- The package route is Linux-only (Homebrew installs are reported, never upgraded by Coddy), and the package manager runs non-interactively, so an upgrade that would remove another package fails instead of asking. Run the package manager yourself in that case.
- Config under **`$CODDY_HOME`** is not modified; only the binary, or the package that carries it, is replaced.
