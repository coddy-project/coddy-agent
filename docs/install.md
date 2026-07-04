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
