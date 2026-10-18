# Operator commands: models and verdicts

Models of one `operator_command` request (`internal/acp/operator_command.go`, the ticket every
surface runs, and `superviseOperatorRun` in `internal/tools/shell/operator_tool.go`), checked with
the built-in engine `mcd` 0.3.4 on the workstation. A verdict is about the model; carrying it to the
code rests on the ticket tests (`internal/acp/operator_command_test.go`) and the tool tests
(`internal/tools/shell/operator_tool_test.go`, `operator_mux_test.go`).

## ticket.pml (Promela)

Two followers answer run / decline / stop in any order, at most three times each, or walk away; the
turn may be cancelled; the command may end by itself, honour or ignore the interrupt, and dies on a
terminate or a kill. `Ticket.Answer` is one critical section in Go and one `atomic` here. The wait
for the decision has no timeout by design, so it carries an `end` label: a request nobody answers
waits until the turn is cancelled.

Commands (budgets quoted, POR not used):

```bash
mcd check --promela ticket.pml --budget-states 1000000 --budget-ms 60000 --budget-mem-mb 1024 \
  --ltl '[] (started <= 1)' --ltl '[] (started == 0 || ranAnswer)' --ltl '[] (st != DECLINED || started == 0)' \
  --ltl '[] (stopc -> <> toolDone)' --ltl '[] (cancelled -> <> (toolDone && !alive))' \
  --ltl '[] !(started == 1)' --ltl '[] !(st == KILLING)' --ltl '[] !(toolDone && interrupted)'
mcd check --promela ticket.pml --fairness weak --budget-states 1000000 --budget-ms 60000 --budget-mem-mb 1024 \
  --ltl '[] (stopc -> <> toolDone)' --ltl '[] (cancelled -> <> (toolDone && !alive))' --ltl '[] (killc -> <> !alive)'
```

| Property | Meaning | Status, evidence |
| --- | --- | --- |
| `deadlock` | no invalid end state | `verified`, `exhaustive` |
| `[] (started <= 1)` | the command starts at most once | `verified`, `exhaustive` |
| `[] (started == 0 \|\| ranAnswer)` | it starts only after a run answer | `verified`, `exhaustive` |
| `[] (st != DECLINED \|\| started == 0)` | a declined request never starts | `verified`, `exhaustive` |
| `[] !(started == 1)`, `[] !(st == KILLING)`, `[] !(toolDone && interrupted)` | sanity: a run, a kill and a stopped end are reachable | `violated` (a witness each), `exhaustive` |
| `[] (stopc -> <> toolDone)` | after a stop the call ends | without fairness `violated`; with weak fairness `verified`, `exhaustive` |
| `[] (cancelled -> <> (toolDone && !alive))` | after a cancel the call ends and no process is left | without fairness `violated`; with weak fairness `verified`, `exhaustive` |
| `[] (killc -> <> !alive)` | a kill ends the command | weak fairness: `verified`, `exhaustive` |

Reports: `safety.report.json` (the first command, after the fix below), `liveness-weak.report.json`.

The first run found a model defect: the tool's `killc` guard stayed enabled after the kill, so the
tool could choose it forever instead of leaving the loop; the Go code sets `kill = nil` after the
first receive, and the model now consumes the signal once (`killed`). After the fix, the remaining
liveness counterexample without fairness is a scheduler artefact: the command loops on ignoring the
interrupt while the tool's escalation step stays enabled and never runs. The assumption that
removes it is weak fairness: a goroutine whose timer fired is scheduled eventually. It is an
assumption about the Go runtime, stated here, not a fact the model proves.

## ticket.petri.json (Petri net)

The token view of the same request: the ticket states and the command's process as places of
capacity 1, so a second start would overflow `started` or `cmd_alive` (`invalid-model`). `rest` is a
self-loop on `ended`, declared so that the final marking is not reported as a deadlock. The net has
no transition for "nobody answers": it is about runs that move, the Promela model covers the wait.

```bash
mcd check --petri ticket.petri.json --budget-states 100000 --budget-ms 30000 --budget-mem-mb 256 \
  --ltl '<> (ended == 1)' --ltl '[] (ended == 0 || cmd_alive == 0)' --ltl '[] (declined == 0 || started == 0)' \
  --ltl '[] !(cmd_dead == 1 && stopping == 1)'
```

| Property | Status, evidence |
| --- | --- |
| `deadlock` (no stuck marking besides the final one) | `verified`, `exhaustive` |
| `safe` (every place 1-bounded: no second start) | `verified`, `exhaustive` |
| `<> (ended == 1)` (every run ends) | `verified`, `exhaustive` |
| `[] (ended == 0 \|\| cmd_alive == 0)` (no command alive after the end) | `verified`, `exhaustive` |
| `[] (declined == 0 \|\| started == 0)` | `verified`, `exhaustive` |
| `[] !(cmd_dead == 1 && stopping == 1)` (sanity: a stopped command is reachable) | `violated` (witness), `exhaustive` |

Report: `petri.report.json`.
