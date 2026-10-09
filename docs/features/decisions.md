# Command safety (decisions)

Bypass mode, the command allowlist, a session's "always allow" grant and a hook's `allow` all let a shell command run with no one looking at it. The decisions check puts a second opinion in front of exactly those calls: before the command runs, Coddy asks the NeuralDeep decisions endpoint - the same `sk-` key as the rest of the hub, a separate `POST /v1/decisions` quota - whether the command is safe to run unattended, and a command whose probability of being unsafe reaches the configured threshold is not executed. The refusal comes back as the `run_command` result - `command rejected as unsafe: the decisions model frida-decisions put the unsafe option at 0.99, at or above the threshold 0.50; ask the operator or use a safer alternative` - so the session transcript records it, the tool call card in the web UI shows it with a cancelled status, and the model reads it and can choose another path.

The check is off by default. Turn it on in Settings → **Command safety (decisions)** or in `config.yaml`:

```yaml
decisions:
  enable: true
  model: frida-decisions   # or clef-flash
  threshold: 0.5           # reject once p(unsafe) reaches this
```

## When the check runs

Exactly the calls no human is about to confirm:

- `run_command` under **bypass** permissions;
- a command the `tools.command_allowlist` or a session grant auto-approves in `ask` / `accept_edits` mode;
- a call a `PreToolUse` hook allowed past the prompt.

A command the operator approved in a permission prompt is **not** checked again - the human in the loop is the stronger verdict, on the fresh call and on the resume of an answered prompt alike. Foreground and background commands (`background: true`) are both checked before the process starts, and subagent children run under the same gate with their inherited configuration.

## The question and the verdict

The state sent to the endpoint is the command text plus the working directory it would run in; the question is a single `choice` with two described options - `safe` (a routine development command: reads, builds, tests, installs, changes only rebuildable project files) and `unsafe` (broad or forced deletion, disk wipes, database drops, force-push, piping a download into a shell, anything irreversible beyond the project). The endpoint answers with the chosen option and the probability of each; Coddy rejects the command when the probability of `unsafe` reaches `decisions.threshold` (default 0.5, the point where the two options swap) and runs it below that - the threshold is the operator's dial, not the endpoint's own pick, so an answer that chose unsafe at 0.7 still runs under a threshold of 0.9, and a borderline 0.6 is rejected under the default. A bare answer without probabilities counts as certainty. `rm -rf /` scores around p(unsafe)=0.99 on both models; a plain `echo` or `go test ./...` is classified safe.

## The model

`decisions.model` picks between the two models the endpoint serves:

| Model | State budget | Typical latency | Quota units per request |
|---|---|---|---|
| `frida-decisions` (default) | 512 tokens of command text | ~20 ms | 1 |
| `clef-flash` | 8192 tokens | ~150 ms | 4 |

`frida-decisions` is an encoder pass with no generation, which is why it is both the default and the cheap option; `clef-flash` is the bigger generative model for commands whose context runs long. The check draws on the decisions quota bucket, separate from the chat class - the balance is served by `GET /v1/decisions/quota` on the hub.

## The credential

The check resolves its credential exactly like chat requests through the neuraldeep provider: an explicit `providers[].api_key` (or `api_key_command`) on the first `neuraldeep` row, then the `NEURALDEEP_API_KEY` environment variable, then a stored hub sign-in (`coddy providers login neuraldeep`); `api_base` picks the deployment (`.ru` or `.tech`) and `providers[].proxy` applies. A machine with none of those still works: the row can be absent, and the env key or the stored login under the default name is enough.

## When the endpoint cannot answer

A safety net that is on must not fail silently open:

- **no credential, a rejected key (401/403), an undecodable answer** - the command is not executed, with the reason and the fix in the result: provide a credential or switch `decisions.enable` off;
- **rate limit (429) and transient failures (network, timeout, 5xx)** - the call is retried inside a two-minute window, honouring the endpoint's `Retry-After`; a window that runs out also stops the command, with the last error in the result.

A turn cancelled while waiting ends the wait and the command with it.

## Testing

The offline suites stand in for the endpoint: `internal/llm/decisions_test.go` pins the request shape, the answer shapes and the error kinds on an `httptest` server, and `internal/agent/decisions_test.go` plus `features/decisions_check.feature` drive the gate itself - the rejection, the fail-closed paths, the retry window and the paths the check must not touch. Two live probes run against the real endpoint, both skipped unless `NEURALDEEP_API_KEY` is set: the transport probe `internal/llm/decisions_live_test.go` (`rm -rf /` unsafe, `echo` safe, both models) and the end-to-end probe `internal/agent/decisions_live_e2e_test.go`, a full turn in bypass mode whose scripted model requests `rm -rf /` and an `echo` - the destructive call comes back rejected in the transcript, the echo runs:

```
NEURALDEEP_API_KEY=... go test ./internal/agent -run TestLiveDecisionsE2E -count=1 -v
```

![The Command safety (decisions) tab of the Settings drawer](../assets/decisions/decisions-settings-dark-1280.png)

*Settings → Command safety (decisions): the enable switch and the model picker*
