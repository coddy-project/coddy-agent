# Plan: the console's first frame before its MCP servers (#319)

Status: shipped from `fix/319-console-first-frame-before-mcp`. Issue #319
reported the console hanging on startup after a large skill marketplace had
been synced; the measurements below found the skills innocent and the
configured MCP servers guilty. The current reference is
`docs/features/mcp.md` (*Pinning npx packages*) and `docs/surfaces/console.md`;
this file keeps the numbers and the decisions as they were taken.

## 1. Method

The console is started in a real pty (pexpect + pyte, the driver of
`examples/cli/cli_tui_driver.py`) and the clock runs from the spawn to three
points: the first read of the pty that returned data (one read can carry
more than one frame, so this is when output was first observed), the version
header on screen (the first frame) and the `escape interrupt` hint. Five runs
per case, medians reported, Linux amd64. `examples/cli/bench_tui_startup.py`
runs the skill cases on the demo config; `examples/cli/bench_tui_real.py` runs
a private copy of the operator's real `~/.coddy` (paths rewritten; every file
of the real home is compared before and after, and the report names anything
that changed). In every run the first read that carried the header also
carried the hint, so no frame without the hint was observed; that says what
was on screen, not that a key had been taken. The script has since gained an
input round trip, a probe typed after the hint and timed until the editor
echoes it (`echo` in the results), and the needles are searched in
everything the console wrote as well as on the emulated screen, since a long
`[Skills]` section scrolls the header off a short screen between two reads.
The table of section 2 is the committed run
`examples/cli/bench_results/tui-startup-2026-09-25.json`: release 1.2.17 and
the branch build side by side. The tables of section 3 were measured earlier,
with release 1.2.16 and `main` at 1.2.17, before the input round trip
existed; their result files name the operator's servers and stay out of the
repository.

Two numbers in section 3 were measured by hand, not by the scripts: the cost
of one `npx` spawn (`/usr/bin/time -f '%e s wall, %U s user' timeout 30 npx -y <package> </dev/null`,
package in the npm cache, network up) and the same under a refused proxy
(`HTTP_PROXY=http://127.0.0.1:9 HTTPS_PROXY=http://127.0.0.1:9 NO_PROXY=`,
150 s limit). The operator's `time coddy` is their own observation.

The proxy variants set `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` and npm's own
`npm_config_proxy` / `npm_config_https_proxy` for the console's process and
clear the exclusion lists; a provider row with its own `proxy` setting is
outside their reach (the rows measured were `inherit`). With
`mcp_servers: []` the same variants cost 26-27 ms, so nothing else the
console does before its first frame depends on the network.

## 2. Skills are not the cause

Demo config, an empty working directory, the first frame (header on screen)
for both binaries and the input round trip for the branch build:

| Skill set | Sources | first frame, release 1.2.17 | first frame, branch | probe echoed, branch |
|---|---|---|---|---|
| none | none | 33 ms | 31 ms | 112 ms |
| the operator's 11 | none | 36 ms | 46 ms | 140 ms |
| the operator's 11 | the operator's 3 (one git marketplace, one `marketplace.json` URL, one repository) | 43 ms | 41 ms | 123 ms |
| 300 synthetic `SKILL.md` | none | 54 ms | 54 ms | 152 ms |
| 300 synthetic | one that accepts TCP and never answers | 56 ms | 55 ms | 145 ms |
| 1000 synthetic | none | 114 ms | 109 ms | 236 ms |

`coddy -v` alone takes 8-9 ms. The loader (`skills.Loader.LoadAll`) reads
and parses every `SKILL.md`, about 0.1 ms each: the two-second budget the
issue proposed is some 20 000 skills away. The dead source was never contacted:
nothing at startup reads `skills.sources`, neither in `internal/session` nor
in `external/cli`. Hypothesis 1 of the issue (a blocking manifest refresh)
does not exist in the code; hypothesis 2 (O(N) scanning) is true with a
constant too small to matter.

## 3. The configured MCP servers are

A copy of the operator's real configuration: six `mcp_servers` entries, four
enabled and run through `npx -y <package>` (github, docker, sqlite,
playwright), one disabled, one whose executable does not exist. Same binary,
same pty:

| Variant | release 1.2.16 | main |
|---|---|---|
| real config, empty working directory | 6.48 s | 6.34 s |
| real config, cwd = this repository | 6.44 s | 7.63 s (two runs at 11 s) |
| real config with `mcp_servers: []` | 26 ms | 28 ms |
| real config, the proxy refuses connections | >60 s, not one byte | >60 s, not one byte |
| real config, the proxy accepts and never answers | >60 s, not one byte | >60 s, not one byte |
| `mcp_servers: []`, the proxy refuses | - | 26 ms |
| `mcp_servers: []`, the proxy accepts and never answers | - | 27 ms |

The operator's own `time coddy` (start, two ctrl+c) read 8.3 s real and 1.4 s
of user CPU: the startup above plus the keystrokes, the CPU belonging to the
`npx` processes.

The same measurement once the fix was in, on the same copy of the real
configuration, which by then listed more `npx` servers (the operator had
installed a marketplace of them in the meantime), release 1.2.17 against the
branch build `1.2.17-4-g64ec3ab1`, the operator's proxy in place:

| Variant | release 1.2.17 | branch build |
|---|---|---|
| real config, empty working directory | 34.7 s | 62 ms |
| real config, cwd = this repository | 33.4 s | 59 ms |
| real config with `mcp_servers: []` | 32 ms | 33 ms |
| real config, the proxy refuses connections (branch build only, 3 runs) | - | 55 ms |
| real config, the proxy accepts and never answers (branch build only, 3 runs) | - | 50 ms |

With the servers off the first-frame path, the console with the real
configuration starts as fast as one without MCP servers, and the network
has no say in when it draws.

How long the servers themselves take, measured by the same run through the
console's own account of them (`bench_tui_real.py --mcp-timeout`: the
footer's `MCP n/m` segment from the first frame until it leaves, the rows
for the servers that failed), 26 configured servers:

| Variant | first frame | every server settled | outcome |
|---|---|---|---|
| network up, 5 runs | 49 ms | 3.5 s (3.1-3.8 s) | 7 connected, 19 failed, the same 19 every run |
| the proxy accepts and never answers, 3 runs | 58 ms | 20.05 s | 0 connected: every server hit the 20 s bound |

The 19 that fail with the network up fail in the release as well, where each
one cost its spawn before the first frame and said nothing; here they are
rows of the transcript with the reason and, for an `npx` package without a
version, the hint to pin it. The 3.5 s is the slowest of 26 concurrent
spawns and handshakes; a prompt sent inside that window waits on
`Connecting MCP servers`, one sent after it starts at once.

What one `npx -y <package>` costs on that machine, package already in the npm
cache, network up:

| Package | Spawn to exit | Outcome |
|---|---|---|
| `@modelcontextprotocol/server-github` | 1.18 s | runs |
| `mcp-server-docker` | 0.94 s | exit 127 |
| `mcp-server-sqlite` | 1.14 s | exit 1 |
| `@playwright/mcp` | 1.40 s | runs |

Two of the four never worked, and the console paid for them on every start.
With the proxy refusing connections, the cached `@modelcontextprotocol/server-github`
took **71 s** to start (exit 0): an unpinned spec sends npx to the registry
for `latest` on every run, and npm's defaults are `fetch-retries=2`,
`fetch-retry-mintimeout=10000`, `fetch-retry-maxtimeout=60000`,
`fetch-timeout=300000`.

## 4. Where the time went in the code

`HandleSessionNew` → `buildFreshState` → `connectConfiguredMCPServers` ran
before the console's `App.Start` returned, so before anything was drawn.
`dialConfiguredMCPServers` walked the servers one after another; neither
`mcp.Connect` nor the client set a timeout, and the console handed
`session/new` a context cancelled only by a signal (`external/cli/run.go`).
`mcpReloadTimeout` (30 s) covered only a settings reload. A server that
spawned and stayed silent held the start forever; `npx` without a network was
such a server for as long as npm retried.

## 5. Decisions

- **Every surface dials concurrently, each server under
  `defaultMCPConnectTimeout` = 20 s** (`internal/session/mcp_dial.go`), below
  the 30 s a settings reload shares across sessions. A constant, not a config
  key: once the dial is off the first-frame path the value only bounds one
  waiting turn, and a key would pull in the schema, the docs tables, the
  bundled skill, `config.example.yaml` and the site. `mcp.connect_timeout`
  can be added to `config.MCP` later if a legitimate server needs more.
- **Only the console defers the dial past the first frame** (option A):
  `SetBackgroundMCPConnect` on the manager, `MCPConnectUpdate` as a control
  update the console renders (`MCP 2/5` in the footer, one row per failed or
  held server), `State.WaitMCPConnect` at turn admission. ACP promises
  connected servers when `session/new` returns (`docs/reference/acp-protocol.md`),
  HTTP and Telegram create their session on the first message and would only
  move the wait inside the turn, and `features/mcp_project_trust.feature`
  checks the marker right after `session/new`; deferring everywhere (option B)
  would rewrite all of that for no visible gain.
- **A turn started while the dial is pending waits, bounded**, rather than
  running with the servers connected so far: the model's tool list is fixed
  when the turn starts (`currentToolDefinitions`), a late server would change
  the tools prefix and invalidate the provider's prompt cache, and a turn
  that sees the GitHub tool depending on how fast the operator typed is not a
  behaviour anybody can rely on. Typical dials end before a person submits a
  prompt, so the wait bites only when a server hangs.
- **Registration pins the version** (`internal/mcp/pin.go`): an entry
  registered through Settings → MCP servers, `PUT /coddy/mcp/{name}` or
  `config_set` whose command is `npx` with `-y` and a package that names no
  exact version is rewritten to `<package>@<version>` before it is written,
  and the operator is told what was pinned and why. The version comes from an
  HTTP `GET <registry>/<package>/latest` (`npm_config_registry`, then the
  public registry, 10 s, through the proxy environment), not from `npm view`:
  that would put npm's own retry loop, the 71 s above, inside a save request,
  needs npm on the server's PATH, and is hard to stub in tests. A `.npmrc`'s
  auth and scoped registries are not read; such a package is saved unpinned
  with the warning. Hand-written entries are not rewritten: `--dry-run`
  warns at the server's line and the manager logs once per process.

## 6. Follow-ups

- `PUT /coddy/config` (the Settings form's `mcp_servers` list) does not pin;
  the MCP tab and `config_set` cover the paths people register servers by.
- `coddy mcp add` does not exist; a verb would carry the man page, the
  completions and `usage_test` with it.
- A killed `npx` leaves its `node` child until stdin EOF reaches it
  (`exec.CommandContext` has no process group); the timeout makes this more
  frequent than before. The process-group helpers of `internal/platform`
  are the fix.
- The remote console (`--remote`) is not deferred: the server creates the
  session synchronously; it gets the concurrent, bounded dial like every
  surface.
