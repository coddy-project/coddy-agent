# m2-busy-wait: where the busy wait of a `coddy` provider lives

Spec: `docs/plans/remote-model-provider.md` 4.1b ([decide at modeling]), 4.3 (error table, "Classification and
retries", the five predicates), 5a (Client rows), 6a. Model: `m2-busy-wait.pml` (this directory). Engine: `mcd` CLI
(built-in explicit-state engine; the CLI has no simulate and no lint, so properties were classified by hand and no
pilot walk was run; every run below is exhaustive on the model).

## 1. Intake card

| Field | Value |
|---|---|
| Boundary | agent loop (first-token timer) -> resilient wrapper (retry by status) -> provider `Stream` -> remote slot gate. Excluded: the remote's own provider retries, the SSE wire, the stall guard, other 5xx recovery. |
| State | clock `now`; phase of the one logical call; wrapper attempt and shared retry allowance; first-token timer (armed, deadline); provider/agent wait deadline; request counter; slot held; remote `freeAt`; results and counters used by the properties. |
| Atomic step | one tick: (A) user Stop, (B) first-token timer, (C) all reactions of agent, wrapper, provider and remote in a fixed causal order until the call blocks on a sleep, a first token or generation. |
| Time | **discrete clock, declared abstraction, not a timed automaton.** Request and answer take 0 ticks, a sleep >= 1 tick, first token `Fd` ticks after the slot is obtained, generation 1 tick. Ties: Stop > timer > first token (`-D TIEFIRST=1` swaps timer and first token; no verdict changes). |
| Scheduler | one process (`world`), so no interleaving inside a tick. Skill pitfall 9 (synchronous round without a scheduler) is addressed by fixing the causal order inside a tick and enumerating every tie between environment events through the choice of their ticks. Weak fairness is therefore irrelevant and was checked (section 5). |
| Environment (nondeterministic) | slot-free tick `S0` in 0..SMAXE or never; first-token time `Fd` in 0..FMAX (includes `Fd >= T`); Stop tick in 0..STMAX or never; upward jitter (`JIT=1`); re-contention after our slot is released (`RECONTEND=1`: a fresh free tick or never; without it a freed slot stays free for us). |
| Constants | scale A: budget `CB=4`, first-token timeout `CT=12`, headroom `CH=1`, `llm_retry_max` 3, Retry-After 1, ceiling 2, `Fd` 0..13, `S0` 0..6/never, Stop 0..20/never (1:3 as 30 s : 90 s). Scale B: 6/18, ceiling 3. Scale C: 10/30, `CH=2`, ceiling 4. |
| Assumptions | the remote answers `busy` only before any output (no 429 after a chunk); a free slot stays free until our next poll unless `RECONTEND`; `retryBudget` gate of the wrapper modelled as `delay + elapsed + CH > CT`; `waitMinInterval` ignored (0); one logical call (`NCALL=2` for the ledger runs). |

## 2. Model class

Finite, untimed engine run, discrete-time abstraction, one process, 30k to 1.05M states per run. Over-approximation
direction: the environment is fully nondeterministic inside the declared bounds, so `verified` holds for every
schedule inside them; `violated` traces are real runs of the model, and a counterexample transfers to the system
only through the code facts quoted in section 6. Nothing about real milliseconds is claimed beyond the three scales.

## 3. Variants (switches of one file)

| Tag | Switches | Meaning |
|---|---|---|
| P0 | `VAR=0` | busy is a plain 429 `Retry-After: 1`, retried by the wrapper by status. |
| P2 | `VAR=2 TARM=0` | wait inside `provider.Stream`, one deadline, status-zero exhausted error, first-token timer armed before the call (today). |
| P2cap | `VAR=2 TARM=0 CAP=1` | as P2, budget `min(CB, CT-CH)`. |
| **P2arm** | `VAR=2 TARM=1` | as P2, the timer is armed when the slot is held (the remote answered 200). |
| P2armcap | `VAR=2 TARM=1 CAP=1` | the brief's "P2cap": timer armed on slot **and** budget capped. |
| P1 | `VAR=1` | provider returns a typed busy error at once; the agent loop sleeps (cancellable) and re-issues with a fresh timer, `resetRetries` on wake, budget per logical call. |
| Mutations | `STATZERO=0` (exhausted/busy error carries status 429), `EXHTRANS=1` (agent classes the exhausted error as transient), `NOCANCEL=1` (sleeps ignore Stop), `PREC_BROKEN=1`, `TURNLEDGER=1`, `REUSE=1 SUB=1` (P1 through `limitResetToWaitFor` as it is, in a subagent), `HELPER=1` (caller outside the ReAct loop, no timer), `RECONTEND=1`, `JIT=1` | anti-vacuity and context. |

## 4. Properties

Atoms are globals of the model. `res`: 1 OK, 2 busy error, 3 "model did not respond", 4 cancelled. Antecedents of
the liveness properties read the environment constants after `started` (a flag set once they are chosen).

| Id | Text (as passed) | Class / logic | Spec statement |
|---|---|---|---|
| deadlock, assert | the model's own; `assert` holds the precedence table (12 literal rows) | safety | 4.1b precedence, 5a Config |
| ltl1 RQ | `[](reqs <= K)`, K = 2 x NREQ1 (8 at scale A) | safety, LTL | requests per call bounded |
| ltl2 TERMNL | `[](exh -> reqs == exhreqs)` | safety, LTL | an exhausted wait is terminal: no request after it |
| ltl3 LIVE | `[]((started && S0 <= cB && Fd < cT && ST == 200) -> <>(over && res == 1))` | liveness, LTL | slot frees inside the budget and first token takes less than T: the call eventually succeeds |
| ltl4 LIVE0 | as LIVE with `&& cuts == 0` in the consequent | liveness, LTL | success without a spurious first-token cut |
| ltl5 TERM | `<>over` | liveness, LTL | the call terminates (horizon 100) |
| ltl6 TB | `[](over -> endt <= E)`, E = 2 x CB + 2 x CT (32 at scale A) | safety, LTL | bounded duration |
| ltl7 STOP | `[]((over && ST != 200 && endt >= ST) -> endt <= ST)` | safety, LTL | Stop ends the call in the same tick |
| ltl8 LEAK | `[](over -> !held)` | safety, LTL | the slot is released on every exit |
| ltl9 NOSPD | `[](busyspend == 0)` | safety, LTL | the wait does not spend `llm_retry_max` |
| ltl10 HONST | `[]((started && S0 == 200 && ST == 200) -> <>(over && res == 2))` | liveness, LTL | slot never frees: the user gets the busy error, not "model did not respond" |
| ltl11 LIVE2 | LIVE for call 2 of 2 (`calls == 2 && Srel <= cB ...`), `NCALL=2` only | liveness, LTL | budget is per call |
| ctl1..ctl7 | `EF` of: a busy answer; busy then OK; exhausted then busy error; Stop while sleeping; a timer cut; "model did not respond"; the LIVE antecedent together with a busy answer | reachability, CTL | sanity (non-vacuity) |

## 5. Reproduce

```bash
MCD=~/.claude/skills/model-check/engine/bin/mcd
M=docs/plans/remote-model-provider-models/m2-busy-wait.pml
m2() {  # m2 "-D VAR=2 -D TARM=1" [K] [E] [extra mcd args]
  $MCD check --promela $M $1 --no-timing --budget-states 12000000 \
   --ltl "[](reqs <= ${2:-8})" --ltl "[](exh -> reqs == exhreqs)" \
   --ltl "[]((started && S0 <= cB && Fd < cT && ST == 200) -> <>(over && res == 1))" \
   --ltl "[]((started && S0 <= cB && Fd < cT && ST == 200) -> <>(over && res == 1 && cuts == 0))" \
   --ltl "<>over" --ltl "[](over -> endt <= ${3:-32})" \
   --ltl "[]((over && ST != 200 && endt >= ST) -> endt <= ST)" \
   --ltl "[](over -> !held)" --ltl "[](busyspend == 0)" \
   --ltl "[]((started && S0 == 200 && ST == 200) -> <>(over && res == 2))" "${@:4}"; }
m2 "-D VAR=2 -D TARM=1"                      # decided variant, scale A
m2 "-D VAR=2 -D TARM=1 -D RECONTEND=1"       # hostile environment
m2 "-D VAR=1" ; m2 "-D VAR=0" ; m2 "-D VAR=2 -D TARM=0 -D CAP=1"
m2 "-D VAR=2 -D STATZERO=0"                  # status 429 mutation
S="-D CB=6 -D CT=18 -D CEIL=3 -D FMAX=19 -D SMAXE=8 -D STMAX=30 -D HOR=140"; m2 "-D VAR=2 -D TARM=1 $S" 8 48
S="-D CB=10 -D CT=30 -D CH=2 -D CEIL=4 -D FMAX=31 -D SMAXE=12 -D STMAX=45 -D HOR=200"; m2 "-D VAR=2 -D TARM=1 $S" 10 80
# sanity: --ctl "EF busyseen" --ctl "EF (busyseen && over && res == 1)" --ctl "EF (exh && over && res == 2)"
#   --ctl "EF (over && res == 4 && stopinwait)" --ctl "EF (cuts >= 1)" --ctl "EF (over && res == 3)"
#   --ctl "EF (started && S0 <= cB && Fd < cT && ST == 200 && busyseen)"
# two calls: add -D NCALL=2 [-D TURNLEDGER=1] and --ltl "[]((started && calls == 2 && Srel <= cB && Fd < cT && ST == 200) -> <>(over && res == 1))"
# zero budget: -D CB=0 with --ltl "[]((started && S0 > 0 && ST == 200) -> <>(over && res == 2 && endt == 0))"
# fairness: add --fairness weak.   Tight bounds: --ltl "[](reqs<=k)" for k = 1.. and "[](over->endt<=e)" for e = 0..
```

## 6. Results (scale A, every cell `exhaustive`; V = verified with complete search, X = violated with a counterexample)

| Variant | RQ | TERMNL | LIVE | LIVE0 | TERM | TB | STOP | LEAK | NOSPD | HONST |
|---|---|---|---|---|---|---|---|---|---|---|
| P0 | V (max 4) | V | **X** | **X** | V | V | V | V | **X** | V |
| P2 (timer at call) | V (5) | V | V | **X** | V | V (24 = 2T) | V | V | V | V |
| P2cap | V | V | V | **X** | V | V | V | V | V | V |
| **P2arm** | V (5) | V | V | V | V | V (28 = B+2T) | V | V | V | V |
| P2armcap | V | V | V | V | V | V | V | V | V | V |
| P1 | V (5) | V | V | V | V | V (28) | V | V | V | V |
| P2 `STATZERO=0` | **X** (13) | **X** | V | **X** | V | V | V | V | **X** | V |
| P2arm `STATZERO=0` | **X** (12) | **X** | V | V | V | V | V | V | **X** | V |
| P1 `STATZERO=0` | V | V | **X** | **X** | V | V | V | V | **X** | V |
| P2arm `EXHTRANS=1` | **X** | **X** | V | V | V | V | V | V | V | V |
| P2arm / P1 `NOCANCEL=1` | V | V | V | V | V | V | **X** | V | V | V |

Hostile environment (`RECONTEND=1`): P2 and P2cap LIVE **X**, LIVE0 X; P2arm, P2armcap and P1 stay all V except
P1 TB (E=32) **X**: the tight bound is 40 for P1 and 32 for P2arm. P0 stays X on LIVE.

Further runs (all scale A unless noted):

| Run | Result |
|---|---|
| Budget above the timeout (`CB=10 CT=6`) | P2: LIVE X, LIVE0 X, HONST **X**; P2cap: LIVE X (the cap shortens the configured wait); P2arm: all V; P2armcap: LIVE X; P1: all V. |
| Zero budget (`CB=0`) | P2, P2arm, P1: busy fails at once (`endt == 0`) V; P0: **X** (it still waits 3 ticks and spends the allowance). |
| Threshold of P2 | scale A with `RECONTEND=1`: LIVE0 V for `Fd <= 7 = CT-CB-1`, X for `Fd = 8` (P2 and P2cap alike); scale C (sticky): V at 19, X at 20 (`CT-CB-1 = 19`). P2arm and P1 V up to `Fd = CT-1` (scale A 11; P2arm scale C rc 29). |
| Tight bounds, `Fd < CT`, `RECONTEND=1` | P2arm and P1: `reqs <= 4 = NREQ1`, `endt <= 16 = CB+CT`. With `Fd >= CT` allowed: P2arm 8 and 32 (2 NREQ1, 2CB+2CT); P1 6 and 40. |
| Callers outside the ReAct loop (`HELPER=1`) | P1: LIVE X, LIVE0 X (busy ends the call at once); P2 and P2arm: all V (no timer exists; P2cap was not run in this context). |
| Two calls per turn (LIVE2) | P1 per call V; P1 with `TURNLEDGER=1` **X**; P2arm V; P2 V. |
| P1 through `limitResetToWaitFor` unchanged | `REUSE=1 SUB=1` (subagent): LIVE X, LIVE0 X; `SUB=0` all V. |
| Scales B and C, jitter, tie order, weak fairness | same verdicts as scale A (tables above: sB rows all agree, sC_v0 LIVE X, sC_v2 LIVE0 X, sC_v2arm and sC_v1 all V; `JIT=1` and `TIEFIRST=1` change no verdict; `--fairness weak` gives the same LIVE/LIVE0/TERM/HONST for P0, P2, P2arm, P1, so no result depends on fairness). |
| Precedence asserts | `assert` V with the table; `PREC_BROKEN=1` -> `assert(o == 5)` **X** (decoded: row provider 5, global explicit 0 gives the global 0 instead of 5). |
| Sanity CTL ctl1..ctl7 | all `verified` for P2, P2arm, P1, `STATZERO=0` variants; for P0 ctl3 is `violated` (no deadline wait exists, `exh` is never set), as expected. |

## 7. Decoded counterexamples

All traces were decoded from the CLI report (the CLI has no `mc_explain`); a lasso ends in a terminated state that
stutters.

1. **P2 (timer at call), LIVE0, `S0=4 Fd=11`, `CB=4 CT=12`.** Slot obtained at the deadline request (tick 4), first
   token due at 15; the timer armed at tick 0 fires at 12 (`cuts` 0->1), the slot is released, the agent re-issues
   once (`allow` 3->2), the second call finds the slot free and succeeds at tick 24 = 2T. Classification: **system
   defect of design (2)** (as specified, the wait is charged to a timer armed before it): condition `wait + Fd >= T`.
   With `RECONTEND=1` the second call may find the slot gone again and LIVE itself fails (P2, P2cap).
2. **P2 `CB=10 CT=6`, HONST.** The timer cuts twice during the wait (`cuts` 1, 2) and the user reads "model did not
   respond" instead of the busy error. Same class.
3. **P2cap / P2armcap `CB=10 CT=6`, LIVE.** `S0` in (CT-CH, CB] is a slot that frees inside the configured budget
   but after the capped wait: the cap silently shortens the user's `busy_wait_ms`. **Defect of the cap.**
4. **P0, LIVE, `S0=4`.** Requests at ticks 0,1,2,3, the wrapper spends its 3 retries (`busyspend` 3), the call ends
   `res=2` at tick 3 although the slot frees at 4 inside the budget: the wrapper waits `llm_retry_max x Retry-After`,
   not the budget, and takes the allowance that the first-token retry needs. **Rejected design.**
5. **P2 `STATZERO=0`, TERMNL, slot never frees.** First wait exhausts after 4 requests (`exh`), the wrapper retries
   it by status (delays 1, 2; `allow` 3->1) and runs a second full wait, the third wait is cut by the timer after
   its first request, the agent's first-token retry starts a fourth wait (`allow` 0): 13 requests instead of 4,
   `res=2` at tick 16. With the timer not armed (P2arm) the same mutation costs 12 requests and the
   duration is bounded only by the wrapper's gate. **Mutation shows the status-zero requirement is load-bearing.**
6. **P1 `STATZERO=0`, LIVE.** Each poll becomes up to 4 requests, the wrapper's sleeps 1+2+4 and the allowance is
   spent; the slot is taken at tick 7, first token at 18 > 12, the first-token retry is refused (`allow == 0`):
   "model did not respond". Same class as 5.
7. **P1 `TURNLEDGER=1`, LIVE2.** Call 1 sleeps 4 ticks (`waited` = `CB`), call 2 starts with an empty budget though its
   slot frees within `CB` ticks: a per-turn ledger starves later calls. **Implementation trap of P1.**
8. **`PREC_BROKEN`, assert.** Row (provider 5, global set) returned the global value: the table is a real check.

## 8. Spec statements

| Statement | Verdict |
|---|---|
| 4.1b: one monotonic deadline over requests, sleeps and jitter, last sleep cut; sleeps RA, 2RA, ... ceiling, jitter upward only | CONFIRMED (P2, P2arm: RQ, TB, TERM, `JIT=1`; at 30 s, ceiling 5 s, RA 1 s one wait is 9 requests at ticks 0,1,3,7,12,17,22,27,30) |
| 4.1b: exhausted wait is a status-zero error, not retryable, outer wrapper must not restart it | CONFIRMED with status zero; CONTRADICTED for status 429 (TERMNL, RQ X). The spec's "llm_retry_max times 30 s" is an upper bound: the wrapper's CallBudget gate and the timer cut it at about 3 waits with the defaults; with the timer deferred (P2arm) only the status zero protects. |
| 4.1b: the wait does not count against `llm_retry_max` | CONFIRMED (P2, P2arm, P1: NOSPD V); P0 and every `STATZERO=0` variant CONTRADICT it |
| 4.1b: Stop cancels the wait | CONFIRMED in P0, P1, P2, P2arm (same tick); `NOCANCEL` shows the property can fail, so the sleeps must select on the context |
| 4.1b: precedence provider > global, zero falls back, global zero = no waiting | CONFIRMED as a table (12 rows, section 9); needs `agent.shared_busy_wait_ms` to be optional (absent 30000, explicit 0 none) |
| 4.1b: "a wait inside `provider.Stream` is charged to the timer" | CONFIRMED as a defect: liveness without a spurious cut needs `Fd < CT - CB` (60 s with the defaults); under a contended remote even eventual success fails |
| 4.1b alternative (1) typed error, agent waits, fresh timer | CONFIRMED sound for the ReAct loop (all V), but REJECTED: other callers never wait (HELPER), and three implementation traps (wrapper retry by status, per-turn ledger, `limitResetToWaitFor` gates) |
| 4.1b alternative (2) wait capped at `T - headroom`, timer not armed until a slot is held | PARTLY CONTRADICTED: the arming half is what works; the cap is useless against slow models (LIVE0 X for P2cap) and harmful when `busy_wait_ms` is large (P2armcap LIVE X) |
| 4.3 table, busy row: "yes, its own budget; once spent, no; status zero" | CONFIRMED for P2 and P1; needs the three typed branches below |
| 4.3: the exhausted error must be in the five predicates | CONFIRMED necessary: `EXHTRANS` (agent recovery restarts the wait) and `STATZERO=0` (wrapper restarts it) both violate RQ and TERMNL |
| 4.3: "one turn costs up to 16 requests" (both ends retry) | NOT DECIDABLE here (the remote's retries are outside this model); busy-only multiplication is quantified in 5 above |
| Residual: upload of the body between slot acquisition and the 200 headers is covered by no first-token timer | NOT DECIDABLE here (not modelled; bounded by the 60 s body deadline of 4.1a) |
| Real milliseconds, jitter percentage | NOT DECIDABLE: discrete ticks, three scales |

## 9. DECISION

**Alternative (2), corrected: the wait lives inside `provider.Stream`, the first-token timer is armed when the slot is
held (the remote answered 200), and the wait budget is NOT capped.** Evidence:

- P2arm is the only in-provider variant with all ten properties verified in the sticky and the hostile environment,
  with `busy_wait_ms` above the timeout, at three scales; P2 and P2cap fail LIVE0 (and LIVE under contention) for
  `Fd >= CT - CB`.
- It covers every caller: `Complete` is `Stream`, so compaction (`internal/agent/compact_fold.go:196`), the direct
  completion routes (`external/httpserver/server_direct.go:44,77`), prompt enhancement and children wait without
  new code. P1 fails LIVE for them (HELPER), because the agent loop is not on their path.
- P1 is correct only for the main loop, and only if its three traps are avoided (own gate, not
  `limitResetToWaitFor`; per-call budget, not `limitWaitLedger`; typed error invisible to the wrapper); its worst-case
  duration is larger (40 against 32 under contention) because `resetRetries` renews the first-token retry on every wake.
- The cap (`min(budget, T - headroom)`) is rejected: with the timer deferred it protects nothing, and it silently
  shortens a configured budget.

Constants that must hold: for `Fd < T` (the existing contract of the timer) success needs only `S0 <= budget`, no
relation between `busy_wait_ms` and `llm_first_token_timeout_ms`; requests per wait `<= NREQ(budget)` (9 at 30 s),
per call `<= 2 NREQ`; duration `<= budget + T` for `Fd < T`, `<= 2 budget + 2 T` otherwise (default 120 s and 240 s).
**Fallback if the arming hook is refused** (P2 as specified): liveness without a spurious cut needs
`budget + Fd_max < llm_first_token_timeout_ms` (budget 30 s leaves models that answer within 60 s), config
validation must warn when `busy_wait_ms + 5 s >= llm_first_token_timeout_ms`, and the cap does not repair it.

## 10. Recommended spec changes

**4.1b**, keep the heading, the first paragraph and the two key bullets; make the global key optional and replace the
rest of the section (from "One monotonic deadline" to the end) with:

> `agent.shared_busy_wait_ms` is an optional integer: absent means 30000, an explicit `0` means no waiting. The
> effective budget is the provider's value when it is above zero, otherwise the global one (so a provider above zero
> waits even when the global key is `0`, and "no waiting for one provider only" is written as global `0` plus a
> positive value on the others).
>
> **The wait lives inside `provider.Stream` of the `coddy` type** (decided by `remote-model-provider-models/m2-busy-wait.md`),
> so every caller waits (the agent loop, compaction, the direct completion routes, prompt enhancement, children).
> The call sends the request and on `busy` sleeps and sends again under one monotonic deadline `start + budget`
> that covers the requests, the sleeps and the jitter. Each sleep is `Retry-After` (at least a second), doubles to a
> ceiling of 5 s, gets about 20% jitter upward only, and the last sleep is cut to what is left; one wait is at most 9
> requests at 30 s. A budget of zero fails at the first `busy`, after one request. A slot that frees inside the budget
> is taken at the next request, at the latest the one made at the deadline.
>
> The spent wait fails the call with a typed `*coddyBusyError` of status zero, and that error is terminal on every
> layer: `httpStatusFromError` returns 0, `isRetryableLLMError` returns false by a typed branch (not by what its message
> contains), `IsTransientProviderError` is false (the agent's provider recovery must not re-issue it), `UpstreamStatus`
> is 0. A restart by the wrapper or the recovery path would multiply the wait. The wait does not count against
> `agent.llm_retry_max`, does not touch the retry allowance and is cut by Stop within the same tick (every sleep selects
> on the context).
>
> **The first-token timer is not charged to the wait.** For a `coddy` row the agent creates the timer disarmed and
> hands the provider a callback in the context (`llm.WithFirstTokenArm`); the provider calls it when the remote
> answers `200`, that is when a slot is held, and from then on the timer counts as today (90 s by default). The wait
> budget is not capped by the timer, so `busy_wait_ms` may exceed `llm_first_token_timeout_ms`; for every other provider
> type, and for a row with `stream: false`, nothing changes. The time between the slot and the `200` (the upload of
> the body) is bounded by the body deadline of 4.1a. The same context carries a callback with which the wait reports its
> countdown, which the agent shows the way a limit wait is shown.
>
> Bounds checked on the model: at most `2 x` the requests of one wait per call (one first-token retry), a call that
> reaches the first token ends within `budget + timeout`, any call within `2 x budget + 2 x timeout`.

**4.1b** also loses the paragraph beginning "**[decide at modeling]** where the wait lives".

**4.3 table**, row `busy`: replace the last two cells with "yes, only inside the provider's own wait (4.1b), never by the
wrapper; once spent, no" and "busy error, typed, status zero, not transient".

**5a Client row**, replace "`busy` waits within one monotonic deadline ... Stop cancels it" with: "`busy` waits within one
monotonic deadline without spending `llm_retry_max`; a budget above the first-token timeout is not cut by it, and the
timer starts at the `200`; budget zero fails at the first `busy` with one request; jitter never goes below
`Retry-After`, the last sleep is cut; the exhausted error is not restarted by the wrapper, not re-issued by the
agent's provider recovery, status zero in the five predicates; a slot freed just before the deadline is taken by the
last request; Stop cancels a sleeping wait at once; `Complete` waits too; the precedence table (provider above zero
wins, zero falls back, global absent 30000, global zero no waiting) is tested row by row".

**6a**, replace the first bullet with: "- **Where the busy wait lives** (4.1b): decided by `m2-busy-wait`: inside
`provider.Stream`, the first-token timer armed when the slot is held, no cap on the budget; the typed-error variant
(agent loop waits) stays rejected because other callers do not wait and it needs its own gate, a per-call budget and a
typed error hidden from the wrapper." Keep the second bullet (capabilities) as it is.

## 11. Limitations

Discrete time at three scales, not milliseconds; one logical call (two for the ledger runs); the 429 answers are only
before output; the remote's own retries, the stall guard, mid-stream 5xx recovery and the upload gap are outside the
model; the hostile environment (`RECONTEND`) is an over-approximation of other clients, a real remote may be kinder.
The result concerns the model, not the code: the code facts used (timer armed before the call, `resetRetries`,
`limitResetToWaitFor` gates, `limitWaitLedger`, wrapper gates) were read from the repository at this commit and must
be re-checked by the unit tests of 5a. Token estimate for this agent: about 290k.
