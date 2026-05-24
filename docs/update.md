# Updating Coddy

Use **`coddy update`** to download official release binaries from [GitHub Releases](https://github.com/coddy-project/coddy-agent/releases) and replace the **`coddy`** executable you are running.

## What gets installed

CI publishes one archive per platform on each SemVer tag **`X.Y.Z`**:

| Archive | Platform |
|---------|----------|
| **`coddy_X.Y.Z_linux_amd64.tar.gz`** | Linux x86_64 |
| **`coddy_X.Y.Z_linux_arm64.tar.gz`** | Linux arm64 |
| **`coddy_X.Y.Z_windows_amd64.zip`** | Windows x86_64 (**`coddy.exe`**) |
| **`coddy_X.Y.Z_darwin_amd64.tar.gz`** | macOS Intel |
| **`coddy_X.Y.Z_darwin_arm64.tar.gz`** | macOS Apple Silicon |

Each binary is built with **`http`**, **`ui`**, **`scheduler`**, and **`memory`** (same as **`make build TAGS="http ui scheduler memory"`** and the default Docker image). See [Build from source](build.md#release-binaries-ci) for the release pipeline.

## Which file is replaced

**`coddy update`** resolves **`os.Executable()`** (symlinks followed) and overwrites that path. Examples:

- After **`make install`** as a regular user, that is usually **`~/.local/bin/coddy`**.
- When you run **`./build/coddy update`**, it updates **`build/coddy`** in the repo.

This differs from **`make install`**, which always copies to **`~/.local/bin`** or **`/usr/local/bin`**. To update the binary on **`PATH`**, invoke the same **`coddy`** that **`which coddy`** prints.

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

## Limitations

- Only platforms listed in the release table are supported; others get a clear error.
- On Windows the running process may lock the executable; close other **`coddy`** instances if replace fails.
- **`coddy update`** needs outbound HTTPS to **`api.github.com`** and the asset CDN (GitHub release downloads).
- Config under **`$CODDY_HOME`** is not modified; only the binary is replaced.
