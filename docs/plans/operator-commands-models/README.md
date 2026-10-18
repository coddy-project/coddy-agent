# Operator commands: models and verdicts

Models of one `operator_command` request (`internal/acp/operator_command.go`, the ticket every
surface runs, and `superviseOperatorRun` / `superviseOperatorRunAndRelease` in
`internal/tools/shell/operator_tool.go`), checked with the built-in engine `mcd` 0.3.4 on the
workstation. A verdict is about the model; carrying it to the code rests on the ticket tests
(`internal/acp/operator_command_test.go`) and the tool tests (`internal/tools/shell/operator_tool_test.go`,
`operator_mux_test.go`). Two rounds: the first models were crossreviewed with their results, and
the review's findings were fixed test-first in the code and in the models (below).

## ticket.pml (Promela)

Two followers answer run / decline / stop in any order, at most three times each, or walk away; the
turn may be cancelled at any step; the command may end by itself, keep running, honour or ignore
the interrupt, and dies on a terminate or a kill. `Ticket.Answer` is one critical section in Go and
one `atomic` here. A cancel interrupts the command and starts a delayed terminate (`Killer`); the
turn waits for the reap at most the drain (`operatorCancelDrain`) and may return with the command
still alive, but the request ends only once the process has (`end_release`). The wait for the
decision has no timeout by design, so it carries an `end` label: a request nobody answers waits until
the turn is cancelled.

Commands (budgets quoted, no partial-order reduction, one worker):

```bash
B="--budget-states 20000000 --budget-ms 600000 --budget-mem-mb 4096"
mcd check --promela ticket.pml $B \
  --ltl '[] (started <= 1)' --ltl '[] (started == 0 || ranAnswer)' --ltl '[] (st != DECLINED || started == 0)' \
  --ltl '[] (!killc || stopc)' --ltl '[] (!stopc || ranAnswer)' --ltl '[] (!(st == ENDED && started == 1) || !alive)' \
  --ltl '[] !(toolDone && stopc && !cancelled && started == 1)' --ltl '[] !(st == KILLING)' --ltl '[] !(toolDone && alive)'
mcd check --promela ticket.pml --fairness weak $B \
  --ltl '[] (stopc -> <> toolDone)' --ltl '[] (cancelled -> <> (toolDone && !alive))' --ltl '[] (killc -> <> !alive)' \
  --ltl '[] (toolDone -> <> (!alive && (st == ENDED || st == DECLINED || started == 0)))'
```

| Property | Meaning | Status, evidence |
| --- | --- | --- |
| `deadlock` | no invalid end state | `verified`, `exhaustive` |
| `[] (started <= 1)` | the command starts at most once - structural here: the start sits once in straight-line code | `verified`, `exhaustive` |
| `[] (started == 0 \|\| ranAnswer)` | it starts only after a run answer | `verified`, `exhaustive` |
| `[] (st != DECLINED \|\| started == 0)` | a declined request never starts | `verified`, `exhaustive` |
| `[] (!killc \|\| stopc)`, `[] (!stopc \|\| ranAnswer)` | a kill only follows a stop, a stop only a run | `verified`, `exhaustive` |
| `[] (!(st == ENDED && started == 1) \|\| !alive)` | the request of a run ends only once its process has | `verified`, `exhaustive` |
| `[] !(toolDone && stopc && !cancelled && started == 1)`, `[] !(st == KILLING)` | sanity: a stopped run that ends without a cancel, and a kill, are reachable | `violated` (a witness each), `exhaustive` |
| `[] !(toolDone && alive)` | the tool call can return while the process lives: a cancel whose drain expired | `violated`, `exhaustive` - the documented exception: the request stays running until the process ends |
| `[] (stopc -> <> toolDone)` | after a stop the call ends | weak fairness: `verified`, `exhaustive` |
| `[] (cancelled -> <> (toolDone && !alive))` | after a cancel the call ends and no process is left | weak fairness: `verified`, `exhaustive` |
| `[] (killc -> <> !alive)` | a kill ends the command | weak fairness: `verified`, `exhaustive` (see the mutants: it does not tell a kill from the escalation) |
| `[] (toolDone -> <> (!alive && (st == ENDED \|\| ...)))` | once the call returned, the process ends and so does the request | weak fairness: `verified`, `exhaustive` |

Reports: `safety.report.json`, `liveness-weak.report.json` (all searches `complete`).

**Mutants** (the property must catch a missing handler; same budgets): without the stop guard
`[] (stopc -> <> toolDone)` is `violated`; without the cancel branch `[] (cancelled -> <> (toolDone
&& !alive))` is `violated`; without the wait for the reap before the request ends `[] (!(st ==
ENDED && started == 1) || !alive)` is `violated`. Without the kill guard `[] (killc -> <> !alive)`
stays `verified`, because the escalation after the grace ends the command too: an untimed model
cannot tell "kill at once" from "terminate after 3 s", so the second Stop's immediacy rests on the
tool test, not on this model.

**Fairness.** Without fairness the liveness properties fail on runs in which the command loops
(keeps running, or ignores the interrupt) while the tool's escalation step stays enabled and never
runs. Weak fairness is per process here. In Go the escalation is a timer channel that stays ready
once it fired, in one `select` with the frame ticker; `select` picks uniformly at random among the
ready cases, so the escalation is taken with probability 1. The model leaves the ticker out. That
is an assumption about the Go runtime, stated here, not a fact the model proves.

**Found by the models and their review, fixed in the code test-first:** a Stop that lost the
`select` to the shell's exit was reported as a plain end and its group was not terminated
(`TestOperatorStopIsNotLostToTheShellsExit`); a cancelled turn whose drain expired ended the
request while the process still ran, so a Stop was refused (`TestOperatorCancelKeepsTheRequestOpenUntilTheProcessEnds`);
a second `Wait` on an ended ticket read as a run (`TestOperatorCommandTicketWaitAfterTheEnd`). The
first model also had a defect of its own: a kill signal consumed once in Go and forever in the model.

Not modelled: the `finished` channel, a command that fails to start, the frames. The multiplexed
runner sets the pane's pid before it returns the run (tmux reads it from `new-window -P`, screen
waits for the launcher's pid file), so a Kill never meets a pid of 0.

## ticket.petri.json (Petri net)

The token view of the same request: the ticket states and the command's process as places of
capacity 1, so a second start would overflow `started` or `cmd_alive` (`invalid-model`). `rest` and
`rest_declined` are self-loops on the two final places, declared so that a final marking is not
reported as a deadlock; a declined request stays `declined`, as in Go. The net assumes every command
ends (it has no "keeps running" transition) and has no transition for "nobody answers": it is about
runs that move, the Promela model covers the rest.

```bash
mcd check --petri ticket.petri.json --budget-states 100000 --budget-ms 30000 --budget-mem-mb 256 \
  --ltl '<> (ended == 1 || declined == 1)' --ltl '[] (ended == 0 || cmd_alive == 0)' \
  --ltl '[] (declined == 0 || started == 0)' --ltl '[] !(cmd_dead == 1 && stopping == 1)'
```

| Property | Status, evidence |
| --- | --- |
| `deadlock` (no stuck marking besides the final ones) | `verified`, `exhaustive` |
| `safe` (every place 1-bounded) - structural: `run` takes the only `waiting` token | `verified`, `exhaustive` |
| `<> (ended == 1 \|\| declined == 1)` (every run reaches a final marking, given every command ends) | `verified`, `exhaustive` |
| `[] (ended == 0 \|\| cmd_alive == 0)` (no command alive after the end) | `verified`, `exhaustive` |
| `[] (declined == 0 \|\| started == 0)` | `verified`, `exhaustive` |
| `[] !(cmd_dead == 1 && stopping == 1)` (sanity: a stopped command is reachable) | `violated` (witness), `exhaustive` |

Report: `petri.report.json`.
