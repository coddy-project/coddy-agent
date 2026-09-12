# Contributing guide

This page is for people: the environment, the build, the test runs, the flow of a change and what a pull request has to carry. The repository map, the build-tag rules and the code review rules that coding agents read are in [AGENTS.md](AGENTS.md), and the detailed project rules live in `.cursor/rules/*.mdc` (mirrored to `.claude/rules/*.md`); this page links to them instead of repeating them.

## Development environment

- **Go** - the version `go.mod` declares (1.25 today); CI reads it from the same file.
- **Node.js and npm** (CI uses Node 22) - for a build with the `ui` tag, where `make ui-build` bundles the SPA that `go:embed` picks up, and for `make test`, which builds those assets and runs the OpenCode rules plugin test with `node --test`.
- **golangci-lint v2.x** (CI pins v2.12.2), built with Go 1.25 or newer - for `make lint`.
- **ripgrep** - the CI test job installs it before `go test`; keep `rg` on `PATH` for the same run locally.
- **Python 3** - only for the end-to-end harnesses in `examples/`; the console driver needs `pip install -r examples/cli/requirements.txt`.

```bash
git clone https://github.com/EvilFreelancer/coddy-agent
cd coddy-agent
make hooks      # once per clone: git commit runs the gate in .githooks/pre-commit
make build TAGS="http ui scheduler memory cli gateway swarm"
./build/coddy -v
```

`make hooks` sets `core.hooksPath=.githooks`, which is local git config and is not committed, so every clone runs it once.

On Windows the `Makefile` targets need a Unix-like shell: run them from Git Bash, WSL or MSYS2, not from `cmd` or PowerShell. If `make ui-build` fails with `npm error enoent ... open '...\package.json'`, the npm on that machine mishandles `--prefix`; build the SPA from inside its directory with `(cd external/ui && npm install && npm run build:go)` and run `make build` afterwards.

## Building

`make build` writes `build/coddy`. Optional modules are Go build tags, passed through the `TAGS` variable with spaces (a raw `go build` takes the same list with commas, `-tags=http,ui,scheduler,memory,cli,gateway,swarm`):

| Command | What you get |
|---|---|
| `make build` | the lean binary: `coddy acp`, the core tools and MCP; no HTTP server, no UI, no scheduler, no memory, no console |
| `make build TAGS=http` | the HTTP API and `/docs` for `coddy serve`, no npm step |
| `make build TAGS=cli` | the interactive console, bare `coddy` on a terminal |
| `make build TAGS="http ui"` | the API plus the embedded SPA; `ui-build` runs first |
| `make build TAGS="http ui scheduler memory cli gateway swarm"` | the full set (`FULL_TAGS` in the `Makefile`): what the releases, the Docker image and the packages ship |

A subsystem enabled in `config.yaml` but not compiled into the binary is a startup error naming the tag. The tag reference, `make install`, `make print-version` and the distribution packages (`make deb`, `make rpm`, `make brew`) are in [docs/contributing/build.md](docs/contributing/build.md).

The SPA does not need a Go rebuild per edit. Run an `http`-only backend on one port and Vite's dev server on another, as the **Dev workflow** section of [DESIGN.md](DESIGN.md) describes:

```bash
make build TAGS=http
./build/coddy serve --config config.yaml --home /tmp/coddy-ui-dev-home --sessions-dir /tmp/coddy-ui-dev-sessions -H 127.0.0.1 -P 12345
```

```bash
npm --prefix external/ui install
npm --prefix external/ui run dev -- --host 127.0.0.1 --port 5173
```

What ships is still the embedded copy: a change under `external/ui/src` is complete only after `make build TAGS="http ui"` regenerated the assets `go:embed` picks up.

## Running the tests

- **`go test ./...`** - the untagged tree with its stubs; the quick check while iterating. A single package: `go test ./path/to/pkg -run TestName -count=1`.
- **`make test`** - the express run, before every push: the OpenCode plugin test, `ui-build`, then one `go test -tags=http,ui,scheduler,memory,cli,gateway,swarm ./...` over the whole tree with every optional module compiled in. Minutes, not tens of minutes.
- **`make test-matrix`** - every tag combination in `TEST_TAG_SETS` (`Makefile`), one after another. This is CI's job, one job per combination on every pull request; do not walk it locally. When a change moved a build-tag boundary (a `_stub.go`, an `Available` const, a `//go:build` line), run that one combination by hand, `go test -tags=<set> ./...`, and leave the rest to CI.
- **`make check-windows`** and **`make lint-windows`** - whenever you touch a file behind `//go:build windows`, or a signature it shares with the rest of the tree. The host runs never compile that half; CI cross-builds it and also runs the OS-specific packages on real `windows-latest` and `macos-latest` runners, the latter driving the console through a pty with `examples/cli/cli_e2e_startup.py`.
- **`cd external/ui && npm test`** - the SPA's own vitest suite (`vitest run`, `npm run test:watch` while editing). It is not part of `make test` or the Go CI jobs, so run it yourself when `external/ui/src` changes.
- **The harnesses in `examples/`** - real binaries against a reachable model: `./examples/build_coddy.sh`, then `./examples/test_acp.sh` (ACP over stdio), `./examples/test_httpserver.sh` (a disposable `coddy serve`), `./examples/test_cli.sh` (the console in a pty, Linux only) and `./examples/test_swarm.sh` (three relays, no model needed). The layout and every script are in [examples/README.md](examples/README.md).

The happy path of a feature is an executable Gherkin spec in the repo-root `features/` directory, run by a godog harness in the package that owns the behaviour (for example `external/httpserver/bdd_*_test.go`, with `Options.Paths` pointing at `../../features/<name>.feature`). Edge and error cases are ordinary unit tests next to the code, never scenarios. Specs stay deterministic and LLM-free through a stub runner, and they run under the tag that owns them as part of `make test`. Conventions are in [.claude/rules/testing.md](.claude/rules/testing.md).

## Making a change

Branch from `main` with a prefix that matches the commit type and a short kebab-case slug: `feat/config-dry-run`, `fix/update-refresh-completions`, `docs/documentation-structure`, `chore/express-test-flow`.

The flow is red, green, then the checks - in full in [.claude/rules/workflow.md](.claude/rules/workflow.md):

1. For a feature, add or extend the happy-path `.feature` spec (and a failing unit test where one fits); for a bug, add the regression test that fails on the broken code. Run the narrowest scope that proves the failure is real.
2. Make the smallest change that turns it green.
3. `make test`, then `make lint`.

What the pull request has to carry depends on what moved:

| The change touches | Then the same pull request carries |
|---|---|
| `external/ui/**` | screenshots of every changed surface, taken from the running build: before and after for a surface that existed, narrow (390 px) and wide (1280 px) when the layout differs, light and dark when colours changed; the regenerated embedded assets (`make build TAGS="http ui"`); and an explicit note when a surface cannot be captured |
| routes, bodies, headers or status codes in `external/httpserver/server.go` | `external/httpserver/openapi.go` in step, and [docs/reference/http-api.md](docs/reference/http-api.md) when the description changed |
| a yaml-tagged field in `internal/config` | `internal/config/config.schema.json` (embedded, what `coddy -t` validates against), `make docs` for the generated tables of [docs/reference/config.md](docs/reference/config.md), `config.example.yaml`, `UISchemaMap()` in `internal/config/ui_schema.go` and `internal/skills/bundled/configure-coddy/SKILL.md`; then `make site-schema` to publish the schema to coddy.dev, held back until the release when a key was renamed or removed (`make site-schema-check` reports drift) |
| a subcommand, a `serve` verb or a flag that `printUsage` lists | `packaging/man/coddy.1`, `packaging/completions/coddy.bash`, `packaging/completions/coddy.zsh` and `topLevelCommands` in `cmd/coddy/usage_test.go`; nothing generates them |
| anything a user notices, or anything a page describes | the documentation, in the same pull request: the page that owns the area, an entry in `docs/nav.yaml` for a new page, a screenshot on the page for a visible UI or console change, `make docs` for the generated pages. Page types, capture recipes and the checks are in [docs/contributing/documentation.md](docs/contributing/documentation.md) |
| a rename of a key, a command or a flag | a sweep with `git grep -nI '<old spelling>'` that comes back empty outside `docs/plans/`: docs, `config.example.yaml`, `examples/`, Go comments, the SPA dictionaries under `external/ui/src/ui/i18n/messages/` and the bundled skills included |
| `.claude/rules/*.md` | the mirror in `.cursor/rules/*.mdc` (`paths:` becomes `globs:` and `alwaysApply:`); the `.mdc` files are the source of truth for every other agent |

Two rules from the code review section of [AGENTS.md](AGENTS.md) come up often enough to repeat: a package that builds by default must not import one behind the `http`, `ui`, `scheduler`, `memory` or `gateway` tags, and project-local configuration (`.coddy/mcp.json`, hook files, subagent definitions) is never read or executed without the trust gate.

## Code style

- `gofmt` before committing; `make lint` is the gate (`golangci-lint` over the untagged tree, then passes over the `cli`, `swarm` and `gateway` tagged surfaces).
- Comments in code, and every technical Markdown file in this repository, are in English.
- Follow the neighbouring files: import grouping, naming, error handling, table-driven tests where they clarify the cases, no real network in a test unless it is documented as integration-style.
- The SPA is formatted with Prettier (`npm run fmt` in `external/ui`); [DESIGN.md](DESIGN.md) is its contract for tokens, layout and component behaviour, and the localization rules (every dictionary changed in the same commit) are in [AGENTS.md](AGENTS.md).

The one-page version is [.claude/rules/code-style.md](.claude/rules/code-style.md).

## Pull requests

Commit messages follow `type(scope): summary`, as the log does: `feat(config): --dry-run probes what config.yaml points at before anything starts`, `fix(update): refresh the man page and the completions beside the binary`, `docs(swarm): ...`, `test(agent): ...`, `chore(test): ...`, `ci: ...`. The scope is the package or surface, the summary is one lower-case line in the imperative, and an issue goes at the end in parentheses (`(issue #195)`).

Every commit passes through the gate `make hooks` enabled: `.githooks/pre-commit` calls `scripts/checks.sh`, which runs `make lint` for a commit that touches code and, for a commit that touches the documentation (`docs/`, `README.md`, `AGENTS.md`, `DESIGN.md`, this file, the config schema), the documentation check instead (`go run ./cmd/docsgen -skip-cli`: the navigation map, links and anchors, assets, generated pages). Tests are opt-in on commit: `CODDY_HOOK_TESTS=fast` adds `go test ./...`, `full` the express `make test`, `matrix` every combination; `CODDY_HOOK_LINT=0` and `CODDY_HOOK_DOCS=0` switch one check off; `CODDY_HOOK_SKIP=1` bypasses everything, and `git commit --no-verify` bypasses one commit.

Before the push: `make test` and `make lint` green, and `make docs-check` when documentation moved. After the push, read the **Tests on PR** run with `gh pr checks` and fix whichever job it names:

| Job | What it proves |
|---|---|
| `Test suite (<tags>)`, one per combination | the tag matrix, `fail-fast: false`, so a broken stub is named by the job that failed |
| `Windows cross-build`, `Test suite (Windows)`, `Test suite (macOS)` | the platform halves the host run never compiles |
| `Distribution packages` | the `.deb` and `.rpm` still build and the Homebrew templates parse |
| `Documentation` | `make docs-check` |
| `Lint` | `golangci-lint` on Linux and on the Windows build, plus the `cli` surface |

The description says what the change is for, which tests were added or changed, how `make test` and `make lint` went, which files moved, and carries the screenshots when the UI did.

## Where things live

| Question | Where |
|---|---|
| How the pieces fit together, package boundaries, session modes | [docs/contributing/architecture.md](docs/contributing/architecture.md) |
| The repository map, build tags, the pre-commit gate, the code review rules | [AGENTS.md](AGENTS.md) |
| Tokens, layout and component contracts of the web UI | [DESIGN.md](DESIGN.md) |
| Building, `TAGS`, release binaries and packages | [docs/contributing/build.md](docs/contributing/build.md) |
| Page types, the navigation map, screenshots, generated references | [docs/contributing/documentation.md](docs/contributing/documentation.md) |
| Adding a built-in tool | [docs/contributing/custom-tools.md](docs/contributing/custom-tools.md) |
| The ReAct loop and the system prompt | [docs/contributing/react-agent.md](docs/contributing/react-agent.md) |
| The full workflow, testing and style rules | [.claude/rules/workflow.md](.claude/rules/workflow.md), [.claude/rules/testing.md](.claude/rules/testing.md), [.claude/rules/code-style.md](.claude/rules/code-style.md) |
| Executable specs and end-to-end harnesses | `features/`, [examples/README.md](examples/README.md) |
| The documentation map | [docs/nav.yaml](docs/nav.yaml) |
