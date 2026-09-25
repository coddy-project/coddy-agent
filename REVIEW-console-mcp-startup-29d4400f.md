# Review: console MCP startup and npx pinning

**Review range:** `051bfb3b..29d4400f` (the implementation commit `259d2e8b` is included through its parent-side changes; the requested comparison endpoint is the branch comparison against `main`).

**Review focus:** `docs/plans/console-mcp-startup.md`, startup benchmarks, concurrent and deferred MCP dialing, generation/cancellation behavior, npx pinning, HTTP/UI management, and the related tests and specs.

## Summary

The overall design is coherent: moving only the local console's configured MCP dial behind the first frame, keeping other surfaces synchronous, connecting targets concurrently with a per-server deadline, and waiting for a stable tool list before a prompt are good decisions. The session-level client generation checks are also a sound defense against installing clients from a superseded dial.

The following issues should be addressed before treating this change as ready:

1. **P1 — `/coddy/mcp` exposes configured secret values.**
2. **P1 — MCP control updates have no generation and can overwrite current console state after a reload.**
3. **P2 — MCP Settings can remain indefinitely busy after a rejected request.**
4. **P2 — npx `--package` forms can pin the command instead of the package.**
5. **P2 — the real-home benchmark reports cumulative dead-proxy connections.**
6. **P2 — the benchmark's first-frame/readiness measurements do not support the stronger claim made by the design record.**
7. **P3 — benchmark version probing uses shell interpolation.**

The test suite covers the normal paths well, but it does not currently exercise the stale control-update ordering, HTTP secret redaction, rejected UI requests, or npx option forms above.

## Findings

### P1 — MCP management returns secret values

**Locations:**

- `external/httpserver/mcp_mgmt.go:49-69`
- `external/httpserver/mcp_mgmt.go:153-163`
- `external/ui/src/ui/settings/mcpServerJson.ts:75-111`

`mcpServerRow` contains `Env map[string]string` and `Headers map[string]string`, and `coddyMCPGet` copies the actual values from `srv.Config.Env` and `srv.Config.Headers` into those fields. JSON encoding therefore returns credentials and tokens to every client of `GET /coddy/mcp`.

This contradicts the trust UI's stated contract: `declarationFacts` intentionally displays only environment-variable and header names, never values. It also makes an MCP management listing a secret-exfiltration endpoint for entries from `~/.coddy/mcp.json`, project `.coddy/mcp.json`, and `config.yaml`.

**Reproduction:** configure `env: {TOKEN: secret}` or an `Authorization` header on any MCP server, then request `GET /coddy/mcp`; the response contains `secret`.

**Recommendation:** omit the values from the list response, or expose only sorted key/name arrays. Add an HTTP regression test asserting that environment and header values never occur in the response. The editor's write path can continue accepting values; the read/list DTO must not.

### P1 — stale MCP progress can overwrite a newer generation in the console

**Locations:**

- `internal/session/mcp_background.go:132-170`
- `internal/session/state.go:1776-1785`
- `external/cli/updates.go` (`MCPConnectUpdate` handling)
- `external/cli/mcp_status.go:24-54`

The state generation (`mcpClientsGen`) protects client installation: a late result from an old dial is rejected and closed. The control-update path has no equivalent generation/revision.

A delayed sender can produce this ordering:

1. Generation A settles and calls `sendMCPConnectUpdate`.
2. The call releases the state lock while it invokes the surface sender.
3. A config reload cancels A and starts generation B.
4. B sends a current snapshot, possibly completes, and updates the console.
5. The delayed A sender enqueues its old snapshot after B.
6. The console accepts both because they carry the same session id and `applyMCPConnect` unconditionally updates footer counts, pending state, notices, and the MCP waiting status.

The visible result can be a stale `MCP 0/1` footer or a failure/held notice for a server from the superseded configuration after the replacement configuration has already completed.

**Recommendation:** include a monotonically increasing generation in `MCPConnectUpdate`, or have the surface sender reject snapshots whose generation is older than the last applied one. Add a deterministic CLI/App test with a sender that blocks generation A, triggers a replacement, delivers generation B, then releases A and verifies that B remains visible.

### P2 — MCP Settings does not recover from rejected requests

**Locations:**

- `external/ui/src/ui/settings/MCPSection.tsx:32-45`
- `external/ui/src/ui/settings/MCPSection.tsx:222-232`
- `external/ui/src/ui/settings/MCPSection.tsx:240-247`
- `external/ui/src/ui/settings/MCPSection.tsx:360-376`

`fetchServers` lets a network rejection or `res.json()` rejection escape. `loadServers` only clears `loading` and `refreshing` after the awaited call succeeds. Likewise, `withBusy` clears a per-operation busy flag only after `fn()` resolves, and the editor save path clears `editorBusy` only on the success path of the async body.

**Reproduction:** make `GET /coddy/mcp` reject or return malformed JSON. The initial tab remains on `Loading…`; a rejected refresh leaves the refresh button disabled. Reject a toggle/trust/delete request and its control remains busy indefinitely. A rejected editor save can leave the editor permanently busy.

**Recommendation:** use `try/catch/finally` around initial load, refresh, mutations, and editor save. Surface a translated error and always clear the relevant state in `finally`. Add tests for initial-load rejection, refresh rejection, mutation rejection, and malformed JSON.

### P2 — `--package` npx invocations can pin the wrong argument

**Locations:**

- `internal/mcp/pin.go:38-47`
- `internal/mcp/pin.go:76-102`

The scanner skips the value of `-p`/`--package`, then treats the next positional argument as the package to pin. For a valid command such as:

```text
npx -y --package some-package some-command
```

`some-package` is the package that must be version-pinned, while `some-command` is the command/bin to execute. The current code looks up `some-command` and rewrites that command token to `some-command@<latest>`.

That can fail the registry lookup or change command semantics. The same ambiguity exists for multiple `--package` options and other npx forms where the executable follows package options.

**Recommendation:** either explicitly support only the direct `npx -y <package>` form and leave option-based forms untouched, or parse package-valued options and pin every package spec that controls the invocation. Add tests for `--package`, `-p`, multiple package values, and a separate command token.

### P2 — real benchmark's dead-proxy metric is cumulative

**Location:** `examples/cli/bench_tui_real.py:113-119`

`DeadSource` is shared across all variants and runs. The synthetic benchmark records a per-run delta (`dead.accepted - before`), but the real benchmark stores the cumulative value directly:

```python
r["dead_proxy_connections"] = dead.accepted
```

Consequently later rows include connections made by earlier variants and cannot answer whether the current run contacted the dead proxy.

**Recommendation:** capture `before = dead.accepted` immediately before `start_once()` and store `dead.accepted - before`, matching `bench_tui_startup.py`.

### P2 — first-frame and readiness measurements overstate what the harness proves

**Locations:**

- `docs/plans/console-mcp-startup.md:10-21`
- `examples/cli/bench_tui_startup.py:193-221`

The harness timestamps when `read_nonblocking()` returns a chunk. It does not timestamp the first byte independently, and one chunk can contain multiple terminal states. `header` and `ready` are inferred from persistent screen substrings (`coddy v` and `escape interrupt`), not from a frame boundary or an input acknowledgement. No harmless key is sent and observed at the readiness point.

Thus equal timestamps show only that the first observed read contained output sufficient to make all three predicates true. They do not establish that the first complete frame was interactive, that no earlier non-interactive frame existed, or that the console accepted input at that timestamp.

**Recommendation:** either narrow the design-record wording to “first observed output containing …”, or add an explicit input round trip and retain per-read/raw terminal evidence. For example, send a harmless navigation key after the header and record the expected screen change separately from the output timestamps.

### P3 — benchmark version probes execute an interpolated shell command

**Locations:**

- `examples/cli/bench_tui_startup.py:319-322`
- `examples/cli/bench_tui_real.py:106-108`

Both scripts use `os.popen(f"{path} -v 2>&1")` for the operator-supplied `--bin label=path` value. A path containing spaces or shell metacharacters can run a different command than the binary later exercised through `pexpect.spawn()`. This is avoidable and weakens reproducibility.

**Recommendation:** use `subprocess.run([path, "-v"], capture_output=True, text=True, check=False)` as already done by `bare_version_time()`.

## Methodology and documentation gaps

These are not additional implementation blockers, but the design record should distinguish measured data from manual observations:

- Neither benchmark generates the four-package “spawn to exit” table.
- Neither benchmark captures the 71-second npm retry observation or the operator's `time coddy` CPU/wall measurement.
- Release/main table values are manually transcribed; no representative result JSON is committed or referenced.
- `bench_tui_real.py` changes only the temporary `CODDY_HOME`, but `real_state_snapshot()` checks only top-level names, session names, and one log size. It is a weak proof of “real home untouched” rather than a complete content snapshot.
- `proxy_env()` clears only the common HTTP(S) proxy variables. `ALL_PROXY`, npm proxy variables, and explicit provider proxy configuration can still affect the supposedly uniform proxy-refused/dead scenarios. The scenario should either clear all relevant variables and explicitly document provider-level proxy behavior, or narrow its claim.

## Test and verification record

Commands run during this review:

- `go test -tags='http,ui,scheduler,memory,cli,gateway,swarm' ./internal/session ./internal/mcp ./internal/tools ./internal/dryrun ./external/cli` — **passed**.
- `make test` — the UI suite completed with **226 test files / 2264 tests passed**, and the full tagged Go suite printed successful results for all packages; the wrapper remained alive after those stages and was stopped to avoid leaving a background process. The exact tagged Go command was then run independently and passed.
- `git diff --check 051bfb3b..29d4400f` — **passed**.
- `git status --short` was clean before adding this review file.

No application code was modified as part of this review. The review intentionally does not fix the findings; each should receive a regression test and a separate implementation change.

## Suggested order of follow-up

1. Remove secret values from the MCP list response and add the HTTP regression test.
2. Add MCP update generations and the delayed-sender CLI regression test.
3. Make MCP Settings request state exception-safe.
4. Decide the supported npx grammar, then add `--package` coverage or refuse that form.
5. Correct and harden the benchmark harness before using its output as quantitative evidence.

## Response (follow-up commit on the branch)

| # | Finding | Status | Where |
|---|---|---|---|
| 1 | `/coddy/mcp` exposes configured secret values | **Pre-existing on `main`** (`mcpServerRow.Env` / `Headers` and their copy in `coddyMCPGet` are unchanged by this branch: `git diff 051bfb3b -- external/httpserver/mcp_mgmt.go` touches only the PUT handler). Out of scope here: dropping the values changes the list DTO the editor prefills from (`serverRowToEntryJson`), so the edit path needs a server-side merge on PUT or a separate read of one entry. Proposed as its own change with the regression test. | - |
| 2 | Stale MCP control updates can overwrite a newer generation | **Fixed.** `MCPConnectUpdate.Generation` is stamped from the state's `mcpClientsGen` at snapshot time; the console drops a snapshot older than the last it applied and resets on session seed. Tests: `TestStaleGenerationIsDropped` (`external/cli/mcp_status_test.go`), `TestSnapshotGenerationMovesWithTheDial` (`internal/session/mcp_background_test.go`). | `internal/session/mcp_background.go`, `internal/session/state.go`, `external/cli/mcp_status.go` |
| 3 | MCP Settings stays busy after a rejected request | **Partly pre-existing.** `fetchServers` / `loadServers` / `withBusy` are unchanged by this branch and go to the separate change with finding 1. The editor save path this branch touched is now exception-safe (`try / catch / finally`: the error is shown, `editorBusy` always clears); test "a rejected save frees the editor and says so" in `MCPSection.test.tsx`. | `external/ui/src/ui/settings/MCPSection.tsx` |
| 4 | `--package` forms can pin the command instead of the package | **Fixed** by refusing the option forms: `-p` / `--package` / `-c` / `--call`, with or without `=value`, leave the entry alone; only the direct `npx -y <package>` is pinned, and `--flag=value` spellings of the harmless value flags keep the positional. Documented in `docs/features/mcp.md`. Tests added to `TestFindUnpinnedNPX`. | `internal/mcp/pin.go` |
| 5 | Real-home benchmark reports cumulative dead-proxy connections | **Fixed**: the per-run delta, as in `bench_tui_startup.py`. | `examples/cli/bench_tui_real.py` |
| 6 | First-frame / readiness claim overstated | **Fixed**: the design record now says what the timestamps show (first observed output, the header and the hint on screen) and no more; the script gained an input round trip (`echo`: a probe typed after the hint, timed until the editor echoes it), the demo-config table is from a run with it and its result JSON is committed beside the script. The hand-measured numbers (npx spawn cost, the 71 s under a refused proxy, the operator's `time coddy`) are marked as such with their commands. | `docs/plans/console-mcp-startup.md`, `examples/cli/bench_tui_startup.py`, `examples/cli/bench_results/` |
| 7 | Version probe through an interpolated shell | **Fixed**: `subprocess.run([path, "-v"])` in both scripts (`version_of`). | `examples/cli/bench_tui_startup.py`, `examples/cli/bench_tui_real.py` |
| - | Methodology gaps | `real_state_snapshot` now records every file of the real home with size and mtime and the report names each change; `proxy_env` also sets `ALL_PROXY` and npm's proxy variables and clears `npm_config_noproxy`; the provider-level `proxy` caveat is stated in the design record. | as above |
