# Session Stop and Queue Hotfix Implementation Plan

> **For agentic workers:** Use `subagent-driven-development` or `executing-plans` to implement each task with a failing regression first. Keep the active session checklist current.

**Goal:** Keep Stop and queued follow-ups usable when another client owns the running turn or a stream connection drops.

**Architecture:** Keep the existing session manager, turn lock, HTTP relay and queue. Separate server turn activity from a client's own request/relay lifetime. Read queue content and version under one lock; recover current state over existing REST routes. The operator approved a Stop/queue hotfix only on 2026-09-14. The session bus and shared permission/question gates remain deferred.

**Tech Stack:** Go 1.25, godog, React/TypeScript, Vitest, HTTP/SSE, console TUI.

## Baseline and workspace

- Main `45aee1ec`: `go mod download`, `make test`, and `cd external/ui && npm test` passed. Vitest: 167 files, 1,278 tests.
- Implementation branch: `fix/session-stop-queue` in `.coddy/worktrees/fix-session-stop-queue`.
- Do not modify `docs/plans/session-bus.md` to suggest its design has shipped.
- Keep all question/permission routing, pending records, and non-interactive policies unchanged.

## Task 1: HTTP queue snapshots

Files: `external/httpserver/queue_http.go`, `external/httpserver/queue_http_test.go`, and the existing `features/message_queue_http.feature` harness for cross-client behavior.

- [ ] Add a deterministic regression that captures the old queue, mutates the live queue, serializes the response and asserts its list and version name the same snapshot. The current helper combines a caller-supplied old list with a fresh version and must fail.
- [ ] Run `go test -tags=http ./external/httpserver -run 'Queue' -count=1` and retain the expected failure.
- [ ] Read both fields through the existing API at the serialization point:

  ```go
  queue, version := st.QueueSnapshot()
  out := map[string]interface{}{
      "object": "coddy.message_queue", "sessionId": sessionID,
      "messages": session.QueuedMessagesWire(queue), "version": version,
  }
  ```

  Remove obsolete list arguments from the private HTTP helper and its callers; preserve public response shapes and the added message receipt.
- [ ] Keep the shared-client BDD scenario green, including cancellation leaving the queue empty and allowing the next turn. Run the focused HTTP tests and the queue/session tests under `-race`.

## Task 2: Web UI Stop and queue lifecycle

Files: `external/ui/src/ui/App.tsx`, focused helper/hook and tests under `ui/chat/`, and localized Stop error copy only if required.

- [ ] Write mounted-App regressions using controlled HTTP/SSE responses: a server-active session outside the first History page still has Stop/queue controls; relay EOF/failure does not make it idle; queue hydration loads a late client's list; failed cancellation retains controls and reports an error.
- [ ] Run the narrow Vitest file and confirm failures reflect those behaviors.
- [ ] Track server activity separately from `activeComposerSidRef`. Keep local ownership/relay guards unchanged; derive composer activity from both sources. Read activity for the selected session rather than depending on filtered History rows.
- [ ] Reconcile activity and queue after session open, events reconnect and stream completion; stale asynchronous results must not repaint another session or overwrite a newer version. A local connection ending does not clear a server-owned queue.
- [ ] Await `/cancel`, validate `res.ok`, and abort local streams only after acknowledgement. A failed request remains retryable and does not report success. Do not replace the user's draft on an unrelated route.
- [ ] Preserve own-versus-watch stream separation, partial assistant text and per-session caches. Avoid broad App refactoring and changes to permission/question components.
- [ ] Run focused lifecycle tests and the whole Vitest suite; rebuild embedded assets in final verification.

## Task 3: Remote console Stop and queue lifecycle

Files: `internal/remote/events.go`, `handler.go`, `rest.go`, `prompt.go` as needed; `external/cli/app.go`, `updates.go`, `queue.go` and focused tests. Use an existing or small typed internal ACP activity update; do not introduce a new public subscription or gate API.

- [ ] Add failing tests for an idle remote console observing a browser-owned active turn: normal submission queues, Escape cancels the named session, and a finished turn re-enables ordinary sending.
- [ ] Add reconnect/load hydration coverage for activity and queue, and ensure own turn activity is not cleared by an unrelated event or a rejected concurrent prompt.
- [ ] Consume existing `turn_started`/`turn_ended` events and existing REST activity for Stop/queue controls, separately from the console's own prompt lifecycle. No new transcript subscription or shared modal behavior in this hotfix.
- [ ] Keep cancellation hooks scoped to the request that owns them. Preserve typed input on queue refusal and avoid sending a second prompt before the old admission releases.
- [ ] Cover the successful multi-client control story in `features/` through the owning godog harness. Keep headless `--remote` without a standing event stream working.
- [ ] Run `go test -tags=http,cli ./internal/remote ./external/cli ./external/httpserver` and focused `-race` tests.

## Task 4: Documentation and evidence

- [ ] Update `docs/features/message-queue.md`, `docs/features/sessions.md`, `docs/surfaces/web-ui.md`, `docs/surfaces/console.md`, `DESIGN.md` and the HTTP narrative/OpenAPI wherever the hotfix changes their described semantics. State that shared gates and cross-process queue/answer IPC remain unavailable.
- [ ] Capture running Web UI and remote console states with deterministic local test data. Use Dark 1280 for documentation, 390 when layout differs, and keep every committed asset referenced by a page. Record any genuinely unavailable capture explicitly.
- [ ] Run `make docs` and `make docs-check`; run `make site-docs` and `make site-docs-check` against the sibling site checkout. Site publication follows the main-repository merge; do not publish a page that is not on main.

## Task 5: Verification and cross-review

- [ ] Run `make test`, `cd external/ui && npm test`, the focused race tests, `make build TAGS="http ui"`, and `make lint`. Leave the tag matrix to CI.
- [ ] Review spec compliance against this narrow boundary before requesting quality review. Inspect the diff for gate changes and unintended assets.
- [ ] Request read-only reviews of the complete diff from Cursor `auto` and Claude Code `claude-fable-5-1`. The system Claude Code 2.1.236 cannot call this model (requires 2.1.251+); prepare a newer isolated CLI rather than substituting another model.
- [ ] Reproduce relevant findings, fix them with regressions, and rerun affected checks.
- [ ] Report root causes, tests and outcomes, changed files, worktree/site status, evidence, and deferred shared-control limitations. Do not claim a merge, deployment, screenshot or review that did not occur.
