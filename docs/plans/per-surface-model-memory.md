# Per-surface model memory

Status: implemented (branch `feat/per-surface-model-memory`).

## Decision

Interactive surfaces — the console TUI, the web SPA, the Telegram gateway —
remember the model the operator last picked **on that surface** and start every
new session on it. A surface's very first use starts on the alphabetically
first configured model (`config.FirstModelID`). `agent.model` stays the
required configuration default for every surface that never picks: raw
`POST /v1/responses` without `metadata.model`, `coddy acp`, scheduler jobs and
`coddy -p` print mode.

## Why

Every new session used to fall back to `agent.model`, so a picked model lasted
one chat and the next chat silently ran on the wrong provider. The memory is
deliberately per surface — the browser's pick does not leak into the console or
the bot — because each surface answers to a different habit of the same
operator.

## Persistence per surface

- **Web SPA** — the existing `coddy_llm_model` cookie (one year, client-side).
- **Console** — `$CODDY_HOME/console-state.json` (`{"last_model": ...}`),
  written atomically like the other `<home>` state files.
- **Telegram gateway** — a reserved `$last_model` entry inside
  `gateway_sessions.json`. A reserved key in the existing flat map was chosen
  over a `{"sessions": ..., "last_model": ...}` format change: older builds
  read the file as a plain map and simply ignore the extra entry, so no
  migration and no downgrade data loss.

## Applying the pick

A fresh session gets the pick through `HandleSessionSetConfigOption("model")` —
the same call a user pick makes, so nothing downstream distinguishes them.
"Fresh" is guarded so a real choice is never clobbered:

- **Console** — `applyInitialModel` runs only at proven-new sites (`Start`
  without a `--session-id` pin, `pickSessionBlocking`, `startNewSessionWorker`)
  and reads candidates from the new session's own `ConfigOptions` (a remote
  console picks from the server's advertised list). `adoptSession` is shared
  with resume, so it is never the hook.
- **Gateway** — `applyInitialModel` runs at both session-minting sites
  (`processMessage` and `ensureSession`) only when
  `SelectedModelID == "" && len(Messages) == 0`. An explicit pick on an empty
  transcript survives (`SelectedModelID` non-empty), and `/clear`'s eager mint
  counts as fresh.

## What counts as a pick

Only session-scoped changes: the model menu (web, console, telegram keyboard),
typed `/model <id>` (bare or ahead of a prompt). Turn-scoped `--once` /
`--count=N`, the agent's own `switch_model`, and the `--model` launch flag
never write the memory — a per-invocation override is not the surface's
standing choice. `session_settings` snapshots are never a memory source
either: they carry picks made on other surfaces of a shared session, which is
exactly the leakage per-surface memory exists to prevent.
