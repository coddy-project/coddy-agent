# Integration tests with tgfake

Coddy's Telegram bot is tested against
[tgfake](https://github.com/EvilFreelancer/tgfake), a fake Telegram Bot API
with a chat page, a simulation API and a scripted OpenAI-compatible model. It
used to live here as `internal/tgfake` and `cmd/tgfake`; it is now a project of
its own, released as binaries and imported as a Go module, so any bot can be
tested in the same sandbox. This page is what a contributor needs to run
those tests, read their failures and move Coddy to a new tgfake.

## One version, three ways in

`go.mod` pins tgfake once, and everything in this repository uses that
version:

```text
require github.com/EvilFreelancer/tgfake vX.Y.Z
tool github.com/EvilFreelancer/tgfake/cmd/tgfake
```

| Where | How it uses tgfake |
|-------|--------------------|
| `external/gateway/telegram` tests (`-tags gateway`) | `pkg/server` in-process on `httptest`, imported as `tgfake` and opened through `fakeapi_test.go`; `pkg/botapi` for the keyboard types. Every gateway test that needs Telegram goes through it. |
| `internal/agent`, `external/httpserver`, `external/cli` and gateway suites | `pkg/llmstub`, the scripted model, with `StripTags: []string{"turn_context"}` so a rule matches what the person wrote rather than the block Coddy appends to every request. |
| `examples/gateway/tg_e2e_offline.sh`, `examples/shared/wake_e2e_common.py`, `examples/cli/capture_wake.py`, `npm run check:telegram` | The `tgfake` binary: the one `CODDY_TGFAKE_BIN` names, else built from the pinned module (`go build github.com/EvilFreelancer/tgfake/cmd/tgfake`). They start it with `--llm-model coddy-demo --llm-strip-tag turn_context`. |
| CI, job **Telegram stand (tgfake)** | The release binary of the pinned version, installed by tgfake's own GitHub Action, then `tg_e2e_offline.sh` against a real `coddy serve`. |

The Go tests need nothing but `go test`: the module is downloaded like any
other dependency. Only the scripts start a process.

## Running the tests

The in-process suites are part of the ordinary runs:

```bash
go test -tags gateway ./external/gateway/telegram/
```

```bash
make test
```

The end-to-end run starts the binary and a real `coddy serve` with its bot
pointed at it: a message is answered, `/clear` and `/resume` come back to the
same session, and a background task wakes the agent into the chat.

```bash
make build TAGS=gateway
```

```bash
examples/gateway/tg_e2e_offline.sh
```

`TG_E2E_KEEP=1` leaves the stand up and prints the chat page address, where
you can go on talking to the bot. To run a prebuilt binary instead of building
one, install the release of the pinned version and name it:

```bash
go list -m -f '{{.Version}}' github.com/EvilFreelancer/tgfake
```

```bash
curl -sSfL https://raw.githubusercontent.com/EvilFreelancer/tgfake/main/scripts/install.sh | sh -s -- -b ./build vX.Y.Z
```

```bash
CODDY_TGFAKE_BIN=./build/tgfake examples/gateway/tg_e2e_offline.sh
```

The same variable feeds the background wake harnesses
(`examples/test_httpserver.sh`, `examples/test_acp.sh`, the console's
`cli_e2e_background_wake.py`) and the Mini App check (`npm run
check:telegram`, see [the web UI page](../surfaces/web-ui.md)).

For a stand to poke at by hand, start the pinned version from the repository
root and follow [Debugging against a fake Bot API](../surfaces/gateway.md#debugging-against-a-fake-bot-api):

```bash
go tool tgfake --llm --llm-strip-tag turn_context
```

## In CI

The **Telegram stand (tgfake)** job of `.github/workflows/tests-on-pr.yaml`
is the integration layer:

1. it reads the version `go.mod` pins with `go list -m` and fails when the
   workflow uses tgfake's action at a different ref, so the two cannot drift;
2. `EvilFreelancer/tgfake@vX.Y.Z` installs the release binary of that version
   for the runner, checked against the release's checksums;
3. it builds `coddy` with the `gateway` tag and runs
   `examples/gateway/tg_e2e_offline.sh` with `CODDY_TGFAKE_BIN` set.

No token, no model and no network beyond the download are involved. The
in-process suites run in the test matrix like every other test.

## Defaults worth knowing

- The stand plays `@tgfake_bot`, first name `tgfake`. The polling feature
  names its bot `coddy_fake_bot` / `Coddy Fake` through `Options`; other tests
  take the default.
- The scripted model reports `tgfake-demo`. Coddy's tests and scripts ask for
  `coddy-demo` (`Model` in Go, `--llm-model` on the command line) because their
  configs name `stub/coddy-demo`.
- Without `--llm-strip-tag turn_context` (or `StripTags` in Go) the model
  treats the `<turn_context>` block as the prompt and no rule matches.
- The default person is user 4242 `alice`, whose private chat has the same id.

## Changing tgfake

A test that needs Telegram to behave in a new way - a method the gateway starts
calling, a refusal it must survive - is a change to tgfake, not a handler
written here. Its repository documents how the server mirrors Telegram
(`AGENTS.md`, `.claude/rules/telegram-fidelity.md`).

1. Make the change in tgfake with its tests, merge it and push a tag
   `vX.Y.Z`; its release workflow publishes the binaries.
2. Here, move to the new version and keep the tool in step:

   ```bash
   go get github.com/EvilFreelancer/tgfake@vX.Y.Z
   ```

   ```bash
   go mod tidy
   ```

3. Change `uses: EvilFreelancer/tgfake@...` in `tests-on-pr.yaml` to the same
   tag, then run `make test` and `examples/gateway/tg_e2e_offline.sh`.

To work on both at once, point a workspace file outside either checkout at
both trees and use it for the commands of this repository. A workspace needs
the `replace` line as well, because `go.mod` names a version the local tree
stands in for:

```text
go 1.26.0

use (
	./coddy-agent
	./tgfake
)

replace github.com/EvilFreelancer/tgfake vX.Y.Z => ./tgfake
```

```bash
GOWORK=$PWD/../coddy-tgfake.work go test -tags gateway ./external/gateway/telegram/
```

A script built through `go build` picks the local tree up the same way. The
workspace file is never committed: CI and every other checkout use the pinned
release.
