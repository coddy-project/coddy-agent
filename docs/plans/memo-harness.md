# Experiential harness memory (MemoHarness direction)

Research record, not a decision. It keeps the reading of arXiv 2607.14159, *MemoHarness: Agent
Harnesses That Learn from Experience* (Huang et al.), against Coddy, made on 2026-07-18 at the
author's request. Nothing here is implemented.

## Two kinds of memory

Coddy's long-term memory (`external/memory`, tag `memory`) is declarative: facts, preferences and
project context that a memory subagent recalls for a turn and delivers in the turn context block.
MemoHarness memory is procedural: an experience bank of how the harness itself was configured for a
case and how that went, over six dimensions - context, tools, generation, orchestration, memory and
output. The two complement each other; neither replaces the other.

## Where the harness configuration lives today

The configuration MemoHarness calls `W` is not materialized in Coddy. Its parts are scattered: the
mode decides the tool set (`internal/agent/toolsets.go`), the model row decides generation
parameters and reasoning, the ReAct loop in `internal/agent/react.go` (`Agent.Run`,
`runReActLoop`) fixes the orchestration, and compaction and memory have their own settings. Making
`W` one value per turn is the first step of any of the phases below.

## Proposed path

A separate optional module, `external/harness` behind a `harness` tag, mirroring how
`external/memory` is wired:

1. **Trace.** Record per turn the case features, the configuration used, the trajectory and a reward
   proxy built from signals Coddy already has: tool-call statuses, the stop reason, a user Stop,
   follow-up corrections, the exit codes of test, build and lint commands.
2. **Diagnosis and experience bank.** Turn traces into entries of the form "for cases like this,
   this configuration worked or failed, because".
3. **Test-time adaptation.** Let a turn pick its configuration from the bank, starting with the
   cheapest knobs that are already per-session levers: reasoning effort and the turn budget.
4. **Offline search** for a better default configuration, optional.

## Open problem

The reward. An interactive session is unlabeled: a turn the user did not correct is not necessarily
a good one, and the signals above are proxies. Any implementation has to start by measuring how well
they predict a turn the user would call successful.
