---
description: Go tests, tags, and commands for this repo
paths:
  - "**/*_test.go"
  - "**/*.go"
---

# Testing (Go)

## Commands

- Express run (what agents run before finishing): **`make test`**
  - Runs **`make test-agent-rules`**, **`make ui-build`**, **`make ui-test`** (the SPA's vitest suite), then one **`go test -tags=http,ui,scheduler,memory,cli,gateway,swarm ./...`** - the whole tree with every optional module compiled in, which is what the shipped binary contains. Minutes, not tens of minutes.
- Tag matrix (CI's job, not a local step): **`make test-matrix`** walks every combination in **`TEST_TAG_SETS`** (**`Makefile`**) - the untagged build with its stubs, single tags, pairs, the shipped set. **`.github/workflows/tests-on-pr.yaml`** reads that list through **`make print-test-tag-sets`** and runs one job per combination with **`fail-fast: false`**, so a broken stub is named by the job that failed. Locally, a build-tag boundary change (a **`_stub.go`**, an **`Available`** const, a **`//go:build`** line) is checked with that one combination: **`go test -tags=<set> ./...`**. The untagged build is also compiled by **`make lint`**, so a signature a stub no longer satisfies is caught before the push.
- Race detector: **`make test-race`** runs the whole tree under **`go test -race`** with every tag but `ui`; CI runs it on every pull request (job **Race detector**). Run it before the push when a change touches goroutines, locks or channels, or a test harness that drives a live loop (the console's UI loop, a running `App`, a godog step reading state a worker writes). **`GOFLAGS=-count=3 make test-race`** repeats every test, which a test that leans on process-wide state (a counter, **`bgtask.Default()`**) has to survive. Every package is clean under it: a race it reports is fixed where it lives (a stub's field read under the stub's own lock, a channel handed to a handler goroutine as a local), never skipped.
- Prompt-cache group: **`make test-cache`** runs the **`features/prompt_cache_*.feature`** specs and every test named **`TestPromptCache*`** - what each request sends the provider: one system message for the whole session, a history that only grows at its end, every rule at most once and none that no path brought in. It is part of **`make test`**; run it on its own when a change touches the system prompt, the rules, the history or the send boundary, and give a new test of that kind the **`TestPromptCache`** prefix so it joins the group.
- Performance group: **`make test-perf`** runs the benchmarks (**`Benchmark*`** in the **`perf_test.go`** files), which **`make test`** never runs: timings of the work a turn repeats and the context a session over this repository's own rules costs (**`system-tokens`**, **`request-tokens`**, **`duplicate-rules`** metrics). **`BENCH=<regexp>`** narrows them, **`BENCHTIME=5x`** sets the runs. A benchmark goes into the package's **`perf_test.go`**, not into its functional tests.
- Targeted run while iterating: **`go test ./path/to/pkg -run TestName -count=1`**
- **Touching a file behind `//go:build windows`, or any signature it shares with `_other.go` / the rest of the tree: also run `make check-windows` and `make lint-windows`.** `make test` and `make lint` run on the host, so the Windows half of **`internal/platform`** (and anything else with a per-OS file) is never compiled by them. `check-windows` cross-builds and `go vet`s every non-`ui` tag combination, test files included. CI runs both (**`.github/workflows/tests-on-pr.yaml`**), plus **`go test`** on a real `windows-latest` runner for **`internal/platform`**, **`internal/bgtask`**, **`internal/tools/shell`**, **`internal/update`**, **`internal/hooks`**, **`internal/rules`** (drive-letter glob anchoring) and the console TUI (**`external/cli`**, `-tags=cli`) — the packages whose behavior differs by OS — and for **`cmd/coddy`**, **`internal/config`**, **`internal/dryrun`** and **`internal/swarm`**, whose tests write files and compare paths. The platform packages and the console suite run on `macos-latest` as well (job **`test-macos`**), which also builds the console and drives the binary through a real pty with **`examples/cli/cli_e2e_startup.py`**; the `cli` job of the Linux test matrix runs that script too, and its `gateway` job runs **`examples/gateway/tg_e2e_offline.sh`**: the built binary against the fake Bot API of **`cmd/tgfake`**, no Telegram and no model. The Go suite never opens a pty, so this is the only check of the console's terminal path.
- **Touching a file behind `//go:build android`, or `internal/platform/android.go` that it calls: also run `make check-android`.** The Android build (`GOOS=android` with cgo, linked by the NDK against Bionic for arm64 and x86_64, published for Termux) compiles `android_init.go` and the cgo guard `android_nocgo.go`, which no host gate sees; `check-android` runs `go vet` over it with the NDK compiler (`scripts/android-cc.sh`), test files included, and CI runs it in the **Android cross-build** job, which also builds both release binaries, checks that each is a position-independent executable naming `/system/bin/linker64` with no library Android does not ship, and uploads them as artifacts. The decisions themselves are plain functions tested on every Unix host (`features/android_termux.feature`, `internal/platform/android_test.go`). A new place that builds an `exec.Cmd` calls `platform.AdaptCommand` before `Start`; `TestEverySpawnSiteAdaptsTheCommand` fails otherwise.
- HTTP server core is **`//go:build http`**. Embedded UI tests compile with **`go test -tags=http,ui ./external/httpserver`**. SPA-free **`http`** build uses **`//go:build http && !ui`** handlers under **`external/httpserver`**. Session memory REST uses **`//go:build http && memory`** (**`memory_http.go`**); without **`memory`**, **`memory_http_stub.go`** registers no routes.

## Conventions

- Prefer table-driven tests where it clarifies cases.
- Keep tests deterministic; avoid real network unless the test is explicitly integration-style and documented.
- A test that touches files or paths holds on Windows too (a contributor's machine, and the `windows-latest` job for the packages above): build expected paths with **`filepath.Join`** / **`filepath.FromSlash`**, check POSIX mode bits only when **`runtime.GOOS != "windows"`**, put a path into a YAML fixture with **`%q`** (a double-quoted YAML string reads the `\U` of `C:\Users` as an escape), turn CRLF into LF before matching lines of a checked-out file (Git on Windows converts them), and make a remove or a rename fail by holding the file open (**`os.Open`** does not share delete access) rather than by `chmod` on its directory, which Windows ignores.
- New HTTP behavior belongs in **`external/httpserver/server_test.go`** (and related files) with **`http` build tag parity.

## BDD feature specs (`features/`)

- **Happy-path behavior** of a feature (and the reproduction of a bug) is an executable Gherkin
  spec in the **repo-root `features/`** directory, run by a godog harness (e.g.
  **`external/httpserver/bdd_remote_test.go`**, **`bdd_workspace_test.go`**) whose
  **`Options.Paths`** points at **`../../features/<name>.feature`**.
- **Edge / boundary / error cases** are ordinary **unit tests** next to the code, **not** scenarios
  in `features/`. Keep `.feature` files to the correct-behavior story.
- Feature suites run under the tag that owns the behavior (e.g. **`-tags http`**) and are part of
  **`make test`**. Step definitions may use a stub runner to stay deterministic and LLM-free.

## References

@Makefile
@code-style.mdc
@architecture.mdc

## Asynchronous and tool-driven test harnesses

- `Pool.Get` final status does not mean metadata or subscriber persistence is complete. Persistence tests wait with `Pool.Wait` or `waitUntilFinished`; notification tests wait for the subscriber signal. Do not repair races with sleeps or ordering changes.
- Godog steps that invoke Vitest use `runVitestScenario`, provide a full describe-and-test pattern, and require a nonzero `Tests ... passed` summary. Do not bypass this with a local wrapper.
- Do not run npm-installing Make targets concurrently with Vitest or external-ui Go tests.
- `make docs` and `make docs-check` rebuild `build/coddy` and run npm. A live harness uses a copied stable binary or waits for the rebuild.
- A flake that shows only under load is reproduced by starving the scheduler, not by adding load: `taskset -c 0,1` plus 16-24 parallel copies of the test binary. `yes` in the background or `GOMAXPROCS=1` is not enough. To find a race between closes, wrap the `net.Conn` and the body and record each `Close` with its stack in memory, printing only when the run fails: printing as it happens changes the timing.
- Under vitest with jsdom, `requestAnimationFrame` started from a React handler inside `act()` fires late and in one burst, so a test cannot observe an animation's intermediate frames, and `waitFor` against it is a flake on a loaded CI runner. Replace the frame clock instead: spy `requestAnimationFrame` / `cancelAnimationFrame` onto a map of callbacks and `performance.now` onto the same counter, and give the test an `advance(ms)` that moves the counter and runs the due callbacks inside `act()`. React's own scheduling (`MessageChannel`) and `waitFor` (`setInterval`) are not affected.
- Recommended for the browser checks of the SPA (`npm run check:*`): `make build` with `ui`, `make test`, `make docs` and `make docs-check` run `npm install` in `external/ui`, which removes a Playwright installed with `npm i --no-save playwright`, so reinstall it after each of them when a check reports that Playwright is not installed. Point the checks at an installed Chromium with `CODDY_BROWSER_PATH` (for example `/usr/bin/chromium`) instead of downloading Playwright's browsers.
- Recommended when `go test ./external/ui` times out: the package runs the vitest suite and takes most of the default 10-minute `go test` limit, so on a loaded machine it can hit the limit without any regression. Rerun it alone with a longer `-timeout` (for example `-timeout 40m`) before treating the timeout as a failure.
