# Install Coddy

Install scripts and the landing page: **https://coddy.dev/**

## One-line install

**Linux / macOS**

```bash
curl -fsSL https://coddy.dev/install.sh | bash
```

**Windows (PowerShell)**

```powershell
irm https://coddy.dev/install.ps1 | iex
```

Creates **`~/.coddy/config.yaml`** from the release **`config.example.yaml`** when missing.

## Linux packages (deb, rpm)

Every release publishes a **`.deb`** and an **`.rpm`** for **x86_64** and **arm64** beside the
archives, so Coddy can be installed the way the rest of the system is - and removed the same way.
Prefer this over the install script on a machine you administer: the files are tracked by the
package database, the man page and shell completions are wired up for you, and `coddy update`
knows not to fight your package manager.

**Debian, Ubuntu and derivatives**

```bash
curl -fsSLO https://github.com/coddy-project/coddy-agent/releases/latest/download/coddy_1.0.10_linux_amd64.deb
sudo apt-get install ./coddy_1.0.10_linux_amd64.deb
```

**Fedora, RHEL, openSUSE and derivatives**

```bash
curl -fsSLO https://github.com/coddy-project/coddy-agent/releases/latest/download/coddy_1.0.10_linux_amd64.rpm
sudo dnf install ./coddy_1.0.10_linux_amd64.rpm
```

Replace **`1.0.10`** with the release you want and **`amd64`** with **`arm64`** on 64-bit ARM. The
[releases page](https://github.com/coddy-project/coddy-agent/releases) lists what each tag
published, and **`SHA256SUMS`** beside them covers the packages too:

```bash
sha256sum -c --ignore-missing SHA256SUMS
```

### What the package installs

| Path | What |
|------|------|
| **`/usr/bin/coddy`** | The full binary (**`http`**, **`ui`**, **`scheduler`**, **`memory`**, **`cli`**) |
| **`/usr/share/man/man1/coddy.1.gz`** | **`man coddy`** |
| **`/usr/share/bash-completion/completions/coddy`** | bash completion |
| **`/usr/share/zsh/site-functions/_coddy`** | zsh completion |
| **`/usr/share/doc/coddy/config.example.yaml`** | starting point for **`~/.coddy/config.yaml`** |
| **`/usr/share/doc/coddy/LICENSE`**, **`copyright`** | licence |

That is the whole package: a binary and its documentation. No service, no system account, nothing
under **`/etc`**. Configuration, sessions, skills and credentials stay in the invoking user's
**`~/.coddy`**, so one installed package serves every user on the machine, each with their own
state, and what to run - the console, the HTTP gateway, an editor over ACP - stays your decision.

### First run

```bash
mkdir -p ~/.coddy
cp /usr/share/doc/coddy/config.example.yaml ~/.coddy/config.yaml
# set a provider key in ~/.coddy/config.yaml
coddy
```

### Upgrading and removing

Upgrade through the package manager, or let Coddy fetch the release package for you as root - both
end in the same place, and **`coddy update`** as an ordinary user will say so rather than silently
replacing a packaged file (see [update.md](update.md#installations-owned-by-a-package-manager)):

```bash
sudo apt-get install ./coddy_<newer>_linux_amd64.deb   # or dnf install ./...rpm
sudo coddy update -y                                   # downloads and installs the package
```

```bash
sudo apt-get remove coddy    # or: sudo dnf remove coddy
rm -rf ~/.coddy              # only if you also want the sessions and config gone
```

There is no apt or dnf repository to subscribe to: the packages are release assets, so a new version
arrives when you install the newer file or run **`sudo coddy update`**, not from a background
**`apt upgrade`**.

### Building the packages yourself

```bash
make deb
make rpm
```

See [build.md](build.md#distribution-packages).

## macOS (Homebrew)

```bash
brew install --cask coddy
```

The cask installs the same **`coddy`** binary the macOS archive carries, plus **`man coddy`** and the
bash and zsh completions. Upgrade and removal go through Homebrew:

```bash
brew upgrade --cask coddy
brew uninstall --cask coddy      # brew zap --cask coddy also removes ~/.coddy
```

**`coddy update`** recognises a Homebrew install and points back at **`brew upgrade`** rather than
replacing a file Homebrew tracks. Homebrew refuses to run under **`sudo`**, so there is no
privileged shortcut there.

> Until the cask is accepted into **homebrew/cask**, install it from the release asset:
> **`brew install --cask https://github.com/coddy-project/coddy-agent/releases/latest/download/coddy.rb`**.
> Every release publishes that file with the checksums of the macOS archives of the same tag.

If macOS blocks the first run because the binary is not notarised, clear the quarantine flag:
**`xattr -d com.apple.quarantine "$(which coddy)"`**.

## After install

```bash
export PATH="$HOME/.local/bin:$PATH"
coddy -v
# edit ~/.coddy/config.yaml
coddy http
```

## Windows

### Install locations

| What | Path |
|------|------|
| Binary | `%LOCALAPPDATA%\Programs\coddy\coddy.exe` |
| Config | `%USERPROFILE%\.coddy\config.yaml` |
| Sessions / memory | `%USERPROFILE%\.coddy\sessions\` |

The user directory is **`$env:USERPROFILE`** (`%USERPROFILE%`), **not** `$HOME` — `$HOME` is unreliable across Windows PowerShell and Git Bash setups (Git Bash `$HOME` may differ from `%USERPROFILE%`).

### PATH in the current session

`install.ps1` adds the binary directory to the **user** `PATH`. New terminals pick it up automatically; the terminal you installed from does **not**. Either open a new terminal, or refresh in place:

```powershell
$env:Path = [Environment]::GetEnvironmentVariable("Path","User") + ";" + [Environment]::GetEnvironmentVariable("Path","Machine")
```

(`refreshenv` also works if you have Chocolatey.)

### Editor / agent integrations: use the absolute path

Some harnesses spawn **`coddy acp`** via `cmd /c` or `sh -c` and do not inherit the user `PATH`. To avoid "command not found" wiring bugs, configure clients with the absolute path:

```text
%LOCALAPPDATA%\Programs\coddy\coddy.exe
```

### Package managers

Scoop / winget manifests are not published yet — tracked in [issue #42](https://github.com/coddy-project/coddy-agent/issues/42). Until then use `install.ps1` above and upgrade with `coddy update -y`.

## Docker

```bash
docker compose pull && docker compose up -d
```

See [docker.md](docker.md) and the [README Docker section](../README.md#docker).

## Upgrade

```bash
coddy update -y
```

See [update.md](update.md).

## Build from source

See [build.md](build.md) and the README section **Other installation methods**.
