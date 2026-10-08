# p2-d1-config-source: how a `*config.Config` reaches the listing cache (phase 2, dilemma D1)

Model `p2-d1-config-source.pml` (same directory), checked with `mcd 0.2.0` (built-in engine, CLI; no `simulate`, no
lint: properties are classified by hand). It **decides** D1 of `docs/plans/remote-model-provider-phase2.md` (sections 3.3,
3.5, 3.9, 7-D1 and the S1 notes of 8) with the engine. It builds on `m6-capabilities.md` (the cache carries the whole
record, a refresh never writes a key, the revision gate): here the cache is given and only the PATH from a reader's
config to it varies. Result in one line: **R1 is contradicted on two paths of the current code (the agent's reloads without the re-bind 3.5 names, the skills reload even with it); the source
should ride on `config.Paths` as one atomic cell shared by every config loaded from one `Paths` (R5), with a
backstop in `storeConfig` (R6 of the model); reads stay live; R2 and R3 are safe but cost more or leak across managers; R4 is refuted.**

## 1. Intake card

| Field | Value |
|---|---|
| Boundary | one alias, two cache keys (provider row x api_base), the manager's listing cache, the configs that point at a key, the holders of a `*Config` (manager, HTTP server, agent), one agent turn, readers. Excluded: the wire, busy/slots, usage, signatures, the content of the capability record beyond one class |
| State | remote class `tr[k]`, cache record `cv[k]`; per config `ckey`, `csrc` (R1 cell), `cbind`, `clin` (in the lineage), `cview` (frozen value); holders `mgr`, `srv`, `agt` and the handle that travels with each (`hmgr`, `hsrv`, `hagt`: R3); `lsrc` (cell on `Paths`), `gsrc` (global); the turn (`tL`, snapshot `tv1`, `ncalls`, `consec`); about 25 monotone latches (ghosts) read by the properties |
| Atomic step | a `d_step` without nondeterminism: one loader/Store step, one refresh, one remote change, one read (a reader's read is TWO steps: load the pointer, then resolve; a turn call is one) |
| Assumptions | the operator edits the config at any time (switch the alias to the other key, or an edit that leaves the row alone) through the HTTP save, the skills path (server first) or the agent's reload tool; the remote changes class at any time; a fetch completes at any time for any key whose cache differs; a refusal ends the call (the retry is not modelled, as in m6) |
| Fairness | none needed for the verdicts (all processes are bounded; liveness is also run with `weak`, section 6.4) |
| Budget | per property 40M states / 280 s / 8 GB; every verdict below is `complete` unless marked |

## 2. Facts from the code the model rests on (read in the worktree, 2026-10-07)

| # | Fact | Where |
|---|---|---|
| F1 | `storeConfig` swaps `cfgAt` under `cfgReplaceMu` and ends with `publishConfigReplaced`: observers (the HTTP server, the console) get the config after the manager has it. `NewManager` stores the config it is handed | `internal/session/manager.go:33,167-188,466-489`, `config_observers.go:24,47` |
| F2 | Four holders keep a `*Config` of their own, and the agent a fifth: `Manager.cfgAt`, `Server.cfgAt` (set by `ReplaceConfig`, also from the manager's observer), `App.cfgAt` (set after the manager reloaded), the runtime's `live`, `Agent.cfg` | `manager.go:33`, `external/httpserver/server.go:41,219,267-278`, `external/cli/app.go:51,236`, `run.go:377`, `internal/serve/liveconfig.go:17` |
| F3 | The skills reload loads a config, calls `s.ReplaceConfig(reloaded)` (server pointer and announcement) and only then `s.mgr.ReplaceConfig(reloaded)`: the server holds the config before the manager stored it | `external/httpserver/skills_mgmt.go:546-556` |
| F4 | The agent's two private reloads: after `a.configReloader` (the manager reloads and stores through `ReloadConfigForSession`) the agent calls `config.LoadWithPaths(a.cfg.Paths)` AGAIN and keeps that object as `a.cfg` and in a new registry for the rest of the turn | `internal/agent/react.go:419-433`, `resume_permission.go:393-410`, `manager.go:499,533` |
| F5 | The cache is read as `ProviderModelEntry(cfg, providerName, apiModel)`: the row is found in the `cfg` the reader passes (`cfg.FindProvider`) and keyed by `contextWindowKey(row)` = type\|name\|api_base\|proxy | `internal/session/context_window.go:154,397-415` |
| F6 | Every production constructor of a `Config` hands its `Paths` on; `LoadWithPaths(c.Paths)` is the loader of the manager's reload, the settings save, the MCP manager, the skills reload, the watcher and the agent's two reloads. The only by-value copy of a `Config` is `escapeYAMLSecrets` (serialisation). Nothing compares `Paths` with `==` | `config.go:50-61`, `jsondto.go:725,1119`, `yaml_comments.go:697`, `recovery.go:114`, `check.go:281`, `configapi.go:261,282`, `mcp/manage.go:349`, `watch.go:105` |
| F7 | One `NewManager` per production process. Tests: 256 calls in 115 files; four test functions build two managers on ONE `cfg` (a restart simulation); 47 `ReplaceConfig(` calls in tests: about a dozen hand over a by-value copy of a live config (`next := *cfg`), which carries its `Paths` and so its cell; the rest hand over helper-built configs | `internal/serve/runtime.go:259`, `cmd/coddy/main.go:382`, `external/cli/run.go:258,327`; `internal/session/context_window_test.go:500,531`, `manager_test.go` (3) |
| F8 | A turn builds its provider ONCE (`getProvider`) and rebuilds it only when `transportKey()` (model\|level) changes. `ExpectedRevision` is read in `turnProviderInput` and the level in `EffectiveReasoning(a.cfg)`: two reads of the cache at build. `coddyProvider.view` is nil until a `stale_revision` refresh sets it | `internal/agent/react.go:336,799-801,2542,2684,2753`, `coddy_provider.go:50-71`, `internal/llm/coddy.go:76-95,150-165`, `state.go:1015-1041` |
| F9 | `SwitchModelTool(cfg)` and `modelSwitchOffered(cfg)` read the resolver once when a registry is built: their text is frozen per registry, the decision inside `switch_model` is live | `internal/tools/switch_model.go:25`, `export.go:113`; registry builds `react.go:169,430`, `resume_permission.go:404` |

## 3. Model class, abstractions, variants

Finite, untimed, interleaving. **No clock at all**, declared: the TTL and the fetch latency of the cache are abstracted by
a Refresher that may complete a fetch for any key whose cache differs at any moment (a superset of every TTL schedule);
"bound within a number of steps" is read as "bound by the loader's own steps, or never". Not a timed automaton.
Abstractions (each is a justification, not a proof for the code): A1 NK=2 keys: repointing the alias and editing
`api_base` both change the cache key and nothing else, so one "switch key" edit stands for both; A2 a capability
class offers exactly one level and the revision is the class (any remote change invalidates the previous level;
only equality is ever asked); A3 one alias, one turn, at most NCALL model calls, at most NEDIT edits, NOPR remote changes,
NRD extra readers (scale rows raise NEDIT to 2 and NCALL to 3); A4 a refusal ends the call, the retry after refresh is not
modelled; A5 R1's cell, R5's cell and R2's global are single pointer stores (the real type is `atomic.Pointer`), so there
is no memory-model effect inside a step; A6 `NMGR=2` starts a second manager once on the config object the first one holds.

| Switch | Meaning |
|---|---|
| `MECH` | 1 binding on every config (R1; `REBIND`, `SRVFIRST`, `BIND2` below) - 2 process-global source (R2) - 3 explicit handle that travels with the pointer (R3) - 4 overlay config republished with the listing in the keys (R4) - 5 source on `config.Paths`, one cell per lineage, attached by `NewManager` (R5) - 6 = 5 plus a backstop in `storeConfig` for a config no loader made (`HAND=1` builds such configs) |
| `REBIND` | R1: the agent's private reload shares the old config's cell (`ShareListing`) |
| `SRVFIRST` | the skills path stores the loaded config in the server before the manager binds it (the code today, F3) |
| `BIND2` | R1 mutant: the bind is two stores; between them the source is installed but the row map is the old one |
| `R3NIL` | R3 mutant: the agent's reload forgets to carry the handle |
| `SRCCFG` | 1: the source finds the row in the config the reader passes (plan 3.3, F5); 0 mutant: in the manager's CURRENT config |
| `READ` | 0 live, one record per call - 1 live split (level and revision are two reads) - 2 frozen per turn, nothing unfreezes it - 3 frozen per config - 4 frozen per turn, unfrozen by a refusal (what the code does, F8) |
| `NMGR` `HAND` | 2 = a second manager on the same config object (F7) - `HAND=1` = tests hand a hand-built config to `ReplaceConfig` |
| `WB` `WC` `WS` | latch groups a run tracks (binding, reads, sanity), so the state count does not carry every ghost |

## 4. Properties

All are `[] !latch` safety checks unless marked; for the sanity rows `violated` means **reachable**. Classified by hand.

| Id | Text | Class / logic |
|---|---|---|
| X1 `cross` | no read is answered from another key than the reader's own config names (no cross-row answer) | safety, LTL `[] !cross` |
| X2 `xmgr` | no read is answered from another manager's cache | safety, LTL |
| X3 `halfseen` | no reader meets a half-installed bind (non-atomic bind) | safety, LTL |
| B1-B3 `unbA` `unbS` `unbM` | a decision read of the agent / the server / the manager's own settings never goes through a config with no source | safety, LTL |
| B4 `blind` | no read sees "no source" while the cache already holds the row's record | safety, LTL |
| L1 `liveA`, L2 `liveS` | `[] (agtUnb -> <> !agtUnb)`, `[] (srvUnb -> <> !srvUnb)`: a config the agent / the server holds is eventually bound | liveness, LTL, fairness none and weak |
| F1 `stalerd` | a bound read returns the cache's record at that instant (no memo) | safety, LTL |
| F2 `lost` | an accepted call never drops a level that was validated at admission and is offered by the same remote row now | safety, LTL |
| `rfstale` `rflevel` | a revision-gate refusal / a refusal for the level at a matching revision is reachable | reachability |
| CK `cK0` `cK1` `cK2` | refusals in a row after one change reach 1 / 2 / 3 (CK = K holds iff `cK<K>` is unreachable; `cK0` = a refusal is reachable) | safety, LTL |
| W1 `fnonop` | the config file never holds a key the operator did not write (m6 P2) | safety, LTL |
| W2 `stfromref` | a listing refresh never calls `storeConfig` (it cancels in-flight usage fetches) | safety, LTL |
| S `sA sS sM swapped knownread privread` | the agent read, the server read, the settings read, a config swap, a read that saw a record, a read through the agent's private config are all reachable (anti-vacuity) | reachability |
| `deadlock` | no invalid end state (the model's own, in every run) | safety |

## 5. Commands (reproduce)

`M=docs/plans/remote-model-provider-models/p2-d1-config-source.pml`. Common tail: `--no-timing --budget-states 40000000
--budget-ms 280000 --budget-mem-mb 8000`. Group flags and property lists:

- **B** `-D WB=1 -D WC=0 -D WS=0 --ltl '[] !cross' --ltl '[] !xmgr' --ltl '[] !halfseen' --ltl '[] !unbA' --ltl '[] !unbS'
  --ltl '[] !unbM' --ltl '[] !blind' --ltl '[] (agtUnb -> <> !agtUnb)' --ltl '[] (srvUnb -> <> !srvUnb)'`
- **C** `-D WB=0 -D WC=1 -D WS=0 --ltl '[] !stalerd' --ltl '[] !lost' --ltl '[] !rfstale' --ltl '[] !rflevel' --ltl '[] !cK0' --ltl '[] !cK1' --ltl '[] !cK2'`
- **R** `-D WB=0 -D WC=0 -D WS=0 --ltl '[] !fnonop' --ltl '[] !stfromref'`
- **S** `-D WB=0 -D WC=0 -D WS=1 --ltl '[] !sA' --ltl '[] !sS' --ltl '[] !sM' --ltl '[] !swapped' --ltl '[] !knownread' --ltl '[] !privread'`

Example: `mcd check --promela $M -D WB=1 -D WC=0 -D WS=0 -D NEDIT=1 -D NOPR=1 -D MECH=1 -D REBIND=0 -D SRVFIRST=1 <B list> <tail>`
(R1 without the agent's re-bind; `unbA`, `unbS`, `blind`, `liveA` `violated`). "R1 fixed" = `-D MECH=1 -D REBIND=1 -D SRVFIRST=0`. The per-row flags are in the first column of the tables
(base `-D NEDIT=1 -D NOPR=1` for B, `-D NEDIT=1 -D NOPR=2` for C, `NEDIT=0` for R4 rows, `NEDIT=2` for the scale rows). The
harness that ran the rows is a scratch script (not a deliverable); every row is one `mcd check` line of the shape above.

## 6. Results

Legend: `V` = `verified`, evidence `exhaustive`; `X` = `violated`, evidence `exhaustive` (for a safety latch: reachable,
with a decoded run); `states` = the largest product searched in the row. Every `deadlock` is `verified` / `exhaustive`
(no invalid end state) in every row, and no row is `inconclusive`.

### 6.1 Binding (group B, NEDIT=1 NOPR=1; scale rows NEDIT=2)

| variant | cross | xmgr | half | unbA | unbS | unbM | blind | liveA | liveS | states |
|---|---|---|---|---|---|---|---|---|---|---|
| R1 without the agent's re-bind (`REBIND=0 SRVFIRST=1`) | V | V | V | X | X | V | X | X | V | 580505 |
| R1 as 3.5 writes it (`REBIND=1 SRVFIRST=1`) | V | V | V | V | X | V | X | V | V | 570969 |
| R1 `REBIND=0 SRVFIRST=0` | V | V | V | X | V | V | X | X | V | 200885 |
| R1 `REBIND=1 SRVFIRST=0` (minimal fix) | V | V | V | V | V | V | V | V | V | 191349 |
| R1 `REBIND=1`, `BIND2=1 SRVFIRST=1` | X | V | X | V | X | V | X | V | V | 768681 |
| R1 `REBIND=1`, `BIND2=1 SRVFIRST=0` | V | V | V | V | V | V | V | V | V | 216045 |
| R1 fixed, `SRCCFG=0` | X | V | V | V | V | V | V | V | V | 230085 |
| R1 fixed, `NMGR=2` | V | X | V | V | V | V | V | V | V | 1022337 |
| R2 `MECH=2` | V | V | V | V | V | V | V | V | V | 362853 |
| R2 `NMGR=2` | V | X | V | V | V | V | V | V | V | 1947033 |
| R2 `SRCCFG=0` | X | V | V | V | V | V | V | V | V | 450189 |
| R3 `MECH=3` | V | V | V | V | V | V | V | V | V | 362853 |
| R3 `R3NIL=1` | V | V | V | X | V | V | X | X | V | 372389 |
| R3 `NMGR=2` | V | V | V | V | V | V | V | V | V | 1354077 |
| R5 `MECH=5` | V | V | V | V | V | V | V | V | V | 362853 |
| R5 `HAND=1` | V | V | V | X | X | X | X | X | X | 758493 |
| R5 `NMGR=2` | V | X | V | V | V | V | V | V | V | 1947033 |
| R5 `SRCCFG=0` | X | V | V | V | V | V | V | V | V | 450189 |
| R6 `MECH=6` | V | V | V | V | V | V | V | V | V | 362853 |
| R6 `HAND=1` (`SRVFIRST=1`) | V | V | V | V | X | V | X | V | V | 630639 |
| R6 `HAND=1 SRVFIRST=0` | V | V | V | V | V | V | V | V | V | 347301 |
| R6 `NMGR=2` | V | X | V | V | V | V | V | V | V | 1947033 |
| R4 `MECH=4 NEDIT=0` | V | V | V | V | V | V | V | V | V | 164761 |
| R1 without re-bind, `NEDIT=2` | V | V | V | X | X | V | X | X | V | 4386569 |
| R1 minimal fix, `NEDIT=2` | V | V | V | V | V | V | V | V | V | 1111509 |
| R6, `NEDIT=2` | V | V | V | V | V | V | V | V | V | 2062917 |
| R6 `HAND=1 SRVFIRST=0`, `NEDIT=2` | V | V | V | V | V | V | V | V | V | 2430117 |

### 6.2 Reads: live or frozen (group C, NEDIT=1, NOPR=2; `MECH=5` unless said)

In this table `X` on `rfstale` and `CK>=1` only says that a refusal is reachable (a stale view exists); `CK>=K+1` is `V` exactly when CK = K, so a row with `CK>=2` `V` keeps "at most one refusal in a row after a change".

| variant | stale | lost | rfstale | rflevel | CK>=1 | CK>=2 | CK>=3 | states |
|---|---|---|---|---|---|---|---|---|
| `READ=0` live, one record per call | V | V | X | V | X | V | V | 1247707 |
| `READ=1` live split | V | V | X | X | X | V | V | 2208843 |
| `READ=2` frozen per turn | V | V | X | V | X | X | V | 1254499 |
| `READ=3` frozen per config | X | V | X | V | X | X | V | 2040355 |
| `READ=4` per turn, unfrozen by a refusal (the code) | V | V | X | V | X | V | V | 1123235 |
| `READ=0 SRCCFG=0` | V | X | X | V | X | X | V | 1435447 |
| `MECH=1 REBIND=0` (R1 without re-bind), `READ=0` | V | X | X | V | X | V | V | 1245551 |
| `MECH=4 NEDIT=0` (R4) | X | V | X | V | X | X | V | 1207658 |
| `READ=0 NCALL=3` | V | V | X | V | X | V | V | 1470477 |
| `READ=2 NCALL=3` | V | V | X | V | X | X | X | 1691105 |
| `READ=4 NCALL=3` | V | V | X | V | X | V | V | 1322393 |

### 6.3 R4 (overlay) and sanity

| row | `fnonop` (W1) | `stfromref` (W2) | sanity `sA sS sM swapped knownread privread` (reachable?) |
|---|---|---|---|
| R4 `NEDIT=0` (164761 states) | X | X | five reachable; `privread` V (no agent reload with NEDIT=0) |
| R5 `MECH=5` (stands for R1, R2, R3, R6: no write path in the model) | V | V | all six reachable in R1 (`REBIND=1 SRVFIRST=0`), R2, R3, R5, R6 |


### 6.4 Fairness

| row (`--fairness weak`) | `liveA` | `liveS` | same verdicts as `none`? |
|---|---|---|---|
| R1 without re-bind (`r1a-wf`) | X | V | yes |
| R1 minimal fix (`r1c-wf`) | V | V | yes |
| R6 (`r6-wf`) | V | V | yes |
| R5 `HAND=1` (`r5-hand-wf`) | X | X | yes |

Weak fairness gives the same verdicts as `none` on every liveness row run with it (`r1a`, `r1c`, `r6`, `r5-hand`: `liveA`/`liveS`
identical): the result does not depend on fairness - every process is bounded, so the only infinite run is the stuttering
end (a turn parked holding its config); fairness removes no run that matters here.

## 7. Decoded counterexamples (the engine shows only the first statement of each `d_step`; the effect is read from the model text)

1. **R1 without the agent's re-bind, `unbA`/`blind`/`liveA`** (68 steps, lasso: the system stutters): the turn is admitted on `mgr.Cfg()` (bound), asks
   for a reload, the manager's `Store` runs its four steps (`jst` 1, 2, 4, 5: the config is bound and published), the agent loads
   its OWN config from the file (F4): `csrc` is 0, so every call after it reads through a config with no source; the lasso
   is the turn parked forever with that config. Cause: **system defect of R1 without the re-bind** (7-D1 says so;
   the `ShareListing` of 3.5 is `REBIND=1` and removes it: `r1b`).
2. **R1 even with the re-bind, `unbS`** (66 steps): the skills path stores the loaded config in the server (`jst==1 && jpath==1`, F3), a reader
   loads the server's pointer and resolves before `Store` binds it (`jst==2`). Cause: **system defect (order of two lines in
   `skills_mgmt.go`)**; `SRVFIRST=0` removes it. The plan's sentence "none on the manager path" is true; this is not the manager path.
3. **`BIND2`, `halfseen`/`cross`**: same shape with a two-store bind: the reader resolves between the stores and gets the
   previous row map: a cross-row answer although the interface passes `cfg`. Unreachable when the bind precedes every
   publication (`r1e`): bind atomicity matters exactly while some holder gets a config early.
4. **`SRCCFG=0`, `cross`** (66 steps): the manager already switched, the reader still holds the old config, the source takes the row
   from the manager's current config and answers the new row's record. Cause: **design defect of a weak interface**, in R1, R2 and R5 alike.
5. **`NMGR=2`, `xmgr`** (72 steps): `Test` hands a second manager the config object the first holds (`csrc[0] = 2`); the
   reader of the old config is answered from the second cache. R1, R2, R5, R6; not R3. Cause: **environment (tests)**, F7.
6. **`READ=1`, `rflevel`**: the turn reads the level, a fetch completes (`Refresher`), the turn reads the revision of the new
   record: the revision matches, the level does not. Cause: **system behaviour of two reads at provider build (F8)**.
7. **`READ=2`, `cK1`**: the turn's snapshot is older than the cache and nothing unfreezes it; two calls in a row are refused (`cK2` at NCALL=3: CK = number of calls).
   **`READ=3`**: the value frozen when the config was created is never the cache's (`stalerd`).
8. **R1 without the re-bind, `lost`** (70 steps): a level validated at admission and offered by the remote now is silently not sent after the
   agent's reload (the unbound read gives "no levels"). Not a refusal; the user is never told.
9. **R4, `fnonop` and `stfromref`** (64 steps each, 164761 states, NEDIT=0): the first refresh sets `ovpend`; the republish calls
   `storeConfig` (cancels usage fetches, `stfromref`); a settings save serves `mgr.Cfg()` and writes the overlay's key into
   `config.yaml` (`fnonop`): a listing value is now an operator key. Cause: **system defect of R4**, as the plan predicted.

## 8. Spec statements

| Statement (phase-2 plan) | Verdict |
|---|---|
| 3.5/R1 "a config published before `storeConfig` binds it: none on the manager path" | CONFIRMED for the manager path (`unbM` V in every row but `r5-hand`, `r1e`); CONTRADICTED for R1 as a whole: the skills reload (F3) publishes an unbound config to the server even with the agent's re-bind (`r1b`: `unbS`, `blind` X), and without that re-bind the agent's reloads (F4) are never bound (`r1a`: `unbA`, `blind`, `liveA` X) |
| 7/R1 "the agent's private reloads are never bound unless the agent binds them" | CONFIRMED (`r1a` X, `r1b` V); minimal fix = `REBIND=1` AND `SRVFIRST=0` (`r1c`, `r1c-e2` V); neither alone (`r1b`, `r1d`) |
| 7/R1 "a config handed to two managers (tests) is bound to the last"; 7/R2 "one cache per process, false for a test binary building several managers" | CONFIRMED (`xmgr` X in R1, R2, R5, R6; V in R3) |
| 7/R2 "a source that does not know which config's rows to key by" | CONTRADICTED for the interface of 3.3 (it passes `cfg`): `cross` V in R2, R3, R5, R6; X in every mechanism with `SRCCFG=0`. Cross-row safety is a property of the interface, not of the mechanism |
| 7/R3 "race-free and compile-checked" | CONFIRMED on the model (all V, also `NMGR=2`); the forgotten handle (`R3NIL`) fails like R1 without the re-bind: only the compiler would tell, NOT DECIDABLE here |
| 7/R4 "writes the listing back as the operator's keys; cancels in-flight usage fetches via storeConfig" | CONFIRMED (`fnonop`, `stfromref` X, exhaustive); also frozen (`stalerd` X) and CK >= 2 |
| 7/R5 "mutates the config handed to `NewManager`" | CONFIRMED as the only plain write, and only for a config with no cell, before publication. CONTRADICTED as sufficient: a config no loader made stays unbound (`HAND=1`: `unbA`, `unbS`, `unbM`, `blind`, `liveA`, `liveS` X); the `storeConfig` backstop closes it (R6: V, except the server-first order for such a config, `unbS` X, closed by `SRVFIRST=0`) |
| 7 "reads live, or frozen per config or per turn" | live CONFIRMED (CK = 1 with `READ=0`, `READ=4`, also at NCALL=3, `rflevel` V for 0 and 4); frozen per turn WITHOUT recovery CONTRADICTED (CK = NCALL); frozen per config CONTRADICTED (`stalerd` X, CK >= 2); split read: CK = 1 but a refusal at a matching revision is reachable (`rflevel` X) |
| m6 P2 "a refresh never writes a key" under each delivery | CONFIRMED structurally for R1, R2, R3, R5, R6 (no write path exists in the model: `fnonop`/`stfromref` V are by construction, not a search finding), CONTRADICTED for R4 |
| "every config a turn uses is bound within a bounded number of steps of its creation" | CONFIRMED for R2, R3, R5, R6 (bound at creation), R1 with both fixes (bound inside `Store`, before any holder sees it); CONTRADICTED for R1 without the agent's re-bind (`liveA` X: never, for the agent); with it the server's config is bound late but eventually (`liveS` V, `unbS` X) |
| `SwitchModelTool(cfg)` text frozen per registry (F9) | NOT DECIDABLE here: advisory text, the decision in `switch_model` is live; not modelled |
| Data-race freedom of the cell, behaviour of every `Paths` consumer with a pointer field | NOT DECIDABLE here (one `atomic.Pointer` store per step in the model; needs `make test-race` and `go vet`) |
| The model's verdicts carry to the code | NOT DECIDABLE here: needs the conformance argument that `Store`, `Turn`, `Rdr` match `storeConfig`, the agent's reloads and the readers of F2-F8 |

## 9. DECISION (D1)

**Pick R5 with a backstop in `storeConfig` (the model's R6): the source rides on `config.Paths` as one atomic cell shared by
every config loaded from one `Paths`, attached by `NewManager`; `storeConfig` attaches an empty cell for a config no loader
made; the source finds the row in the reader's `cfg`; reads are live.** Neither the agent nor the HTTP server needs a line.

Evidence: (1) R1 with the agent's re-bind that 3.5 names still publishes an unbound config on the skills path (`r1b`), and without it the agent's reloads stay unbound (`r1a`); its minimal
fix is two edits in two packages that no type forces (`r1c`), and every future `LoadWithPaths` call would need the same discipline. (2) R2, R3, R5, R6 have no reachable unbound read on any production path (all V, NEDIT 1 and 2 for R6).
(3) R2 and R5 leak across managers like R1 (`xmgr` X); R3 does not, but the only cases are tests (F7: four test functions on one
`cfg`, each reading through the newest manager) and R3 costs the sweep of S0's size (30 readers, five holders, the registry
builders); the lineage cell limits the blast radius of "last attach wins" to one config lineage, a process-global to the process.
(4) R5 alone fails for a config no loader made (`r5-hand`: the helper-built configs of the tests' `ReplaceConfig(` calls); the backstop closes it (`r6-hand-bindfirst` V). (5) R4 is
refuted (`fnonop`, `stfromref`). (6) Live reads keep CK = 1 and `lost` unreachable; frozen reads do not.

**Constraints the implementation must keep** (each has a mutant above that fails without it):
1. **One cell per lineage.** `config.Paths` carries an unexported pointer to a `ListingCell` (`atomic.Pointer` to the source); every loader allocates the cell when the `Paths` it is handed has none (`readConfigFile`, `recovery.go:114`, `check.go:281`); every constructor passes its `Paths` on (F6). Without this the agent's reloads are unbound (`r1a`).
2. **Attach before publish, in one store.** `NewManager` attaches before `cfgAt.Store(cfg)` (allocating the cell first for a hand-built config: the only plain write, before publication); `storeConfig` calls an attach-if-empty on `next` before `cfgAt.Store(next)`. The bind is ONE pointer store; no row map, generation or snapshot is installed after it (`BIND2`: `halfseen`, `cross` X while any holder gets a config early).
3. **The row comes from the reader's config.** `ListedModel(cfg, provider, apiModel)` and `listedFor` pass the receiver; the source never looks at the manager's current config (`SRCCFG=0`).
4. **No memo.** The resolver reads the cache on every call; nothing resolved is stored in a `Config`, an agent or a registry's data path (`READ=3`). The provider's per-turn snapshot (F8) is allowed because a refusal unfreezes it (`READ=4`: CK = 1); do not add a freeze without that recovery (`READ=2`).
5. **One record per request** (S3, `coddy_provider.go`): `coddyProviderInput` derives `in.ReasoningEffort` from the record it read for `ExpectedRevision` (the rule of `fallbackReasoningEffort`), so that a refresh between two reads cannot produce a refusal at a matching revision (`READ=1`).
6. **Last attach wins, never panics**, and a test that keeps reading through an older manager after building a newer one on the same config object builds its own config (F7).
7. **Guard test**: every production caller of `config.LoadWithPaths(` takes its `Paths` from a live `*Config` or is on an allowlist (`config.Resolve` at start-up, `serve_password.go`, the tools' `toolConfigPaths`, which only stage edits).
8. Hygiene, not required: drop the first of `s.ReplaceConfig(reloaded)` / `s.mgr.ReplaceConfig(reloaded)` in `skills_mgmt.go:555-556` (the observer already stores it in the server); it removes the one remaining window for a hand-built config.

If R1 is kept anyway, its minimal fix is exactly: `next.ShareListing(a.cfg)` between the load and `a.cfg = next` at `react.go:429` and `resume_permission.go:403`, AND `mgr.ReplaceConfig` before the server's store in `skills_mgmt.go` (rows `r1c`, `r1c-e2`); S1 then owns the two agent files.

## 10. Recommended spec changes (exact replacement text; section numbers of `docs/plans/remote-model-provider-phase2.md`)

**3.3**, replace the code block and the paragraph that follows it (the table after it stays):

> ```go
> // ListedModel is what a model-listing cache knows about one model of one provider row.
> type ListedModel struct {
> 	Multimodal        bool
> 	ReasoningLevels   []string // without "off"
> 	ReasoningDefault  string
> 	AllowReasoningOff bool
> }
> // ListingSource is the manager's cache seen from the configuration. ListedModel finds the provider row in
> // the cfg it is given (cfg.FindProvider, then the cache key of that row) and in no other configuration, and
> // answers from the cache as it is at the moment of the call.
> type ListingSource interface {
> 	ListedModel(cfg *Config, providerName, apiModel string) (ListedModel, bool)
> }
> ```
>
> The five reads go through `func (c *Config) listedFor(ent *ModelEntry) (ListedModel, bool)`, which is false unless the row's provider is of
> type `coddy`, a source is attached to the lineage of `c` (3.5) and the source's cache holds the model (no source, no entry yet, a failed read and a
> model the listing does not carry are all "not known"). It calls the source with its own receiver, so a configuration an agent or a request
> still holds after a reload is answered for its own row (a source that took the row from the manager's current configuration answered another
> row after a repoint, in every mechanism: model `p2-d1-config-source`), and it reads live on every call: nothing resolved is kept in a
> `Config`, a turn or an agent. The one frozen read is the coddy provider's per-turn snapshot, which a refusal replaces
> (`coddy.go` `setView`); `coddyProviderInput` derives `in.ReasoningEffort` from the same record it reads for `ExpectedRevision`
> (the rule of `fallbackReasoningEffort`), so level and revision of one request come from one record. Resolution, per key (for a provider type other than `coddy` every cell is today's behaviour, byte for byte):

**3.5**, replace the whole section:

> ### 3.5 A.4 Reaching the cache from a `*Config` (decided: model `p2-d1-config-source`)
>
> The manager serves many `*Config` values and builds a new one on every reload (`NewManager` `manager.go:167`, `storeConfig` `:466-489`; the loaders
> are `config.LoadWithPaths` in `configapi.go:282`, `mcp/manage.go:349`, `skills_mgmt.go:546`, `manager.go:499`, `watch.go:105`, and,
> privately for the agent, `internal/agent/react.go:425` and `resume_permission.go:399`, which keep `a.cfg = next`). `Config` is read without locks
> after publication. Four holders keep a pointer of their own (manager, HTTP server, console, runtime) and the agent a fifth.
> The source therefore rides on what every loader already hands on, `config.Paths`:
>
> - `config.Paths` gets an unexported `listing *ListingCell` (`ListingCell` holds an `atomic.Pointer` to the source; a pointer, so `Paths` stays a copyable value
>   and nothing copies a lock). `func (p Paths) WithListing() Paths` returns `p` with a fresh cell when it has none; `func (p Paths) AttachListing(src ListingSource)`
>   stores the source (the last attach wins, never a panic); `func (p Paths) AttachListingIfEmpty(src ListingSource) bool` attaches only into an empty cell.
> - Every loader allocates the cell when the `Paths` it is handed has none (`readConfigFile`, `recovery.go:114`, `check.go:281`); `JSONDTOToConfig`, `ParseConfigJSONPreservingSecrets`
>   and `yaml_comments.go:697` pass the `Paths` they are given. Everything loaded through `LoadWithPaths(c.Paths)` therefore shares the cell of `c` and is bound the moment it exists:
>   the manager's reloads, the settings save, the skills reload, the watcher and the agent's two private reloads need no step and no line of their own.
> - `NewManager` attaches the manager to its configuration's cell before `m.cfgAt.Store(cfg)` (allocating one first when `cfg.Paths` has none: the only plain write, before publication).
>   `storeConfig` calls `AttachListingIfEmpty(m)` on `next` before `m.cfgAt.Store(next)`, allocating a cell when `next.Paths` has none, so a configuration no loader made
>   (a test's `ReplaceConfig(&config.Config{...})`) is bound before it is published; it never takes over a cell another manager attached.
> - The bind is one pointer store; no per-config state is installed after it. The reader's `cfg` is what the source receives (3.3). The resolver does not memoise.
> - Not chosen, with the model's evidence: **R1** (a binding on every config) is contradicted on two paths of the current code - the agent's private reloads leave `a.cfg` unbound
>   for the rest of the turn unless the agent re-binds, and `skills_mgmt.go:555-556` stores the loaded configuration in the server before the manager binds it; its minimal fix is two coordinated
>   edits in two packages and a discipline for every later loader. **R2** (a process global) is bound everywhere but a test binary with several managers reads through the last
>   one. **R3** (an explicit handle) is the only mechanism immune to two managers on one config object and costs the sweep of S0's size for tests only. **R4** (an overlay) writes the
>   listing into the operator's keys on a save and calls `storeConfig` on every refresh.
> - A second manager built on the same configuration object (four tests in `internal/session` do) takes the lineage over: a test that keeps reading through the older manager builds its own configuration.
>   A guard test lists the production callers of `config.LoadWithPaths(` and requires each to take its `Paths` from a live `*Config` or to be on an allowlist (`config.Resolve` at start-up,
>   `cmd/coddy/serve_password.go`, the tools' `toolConfigPaths`).

**3.9**, `internal/session` bullet, replace "the binding survives `ReplaceConfig` and a loader-built config **[D1]**" with:

> `NewManager` attaches before the first read; a config loaded through `LoadWithPaths(c.Paths)`, `JSONDTOToConfig(j, c.Paths)` and the agent's two private reloads (`react.go:425`, `resume_permission.go:399`: `config_commit`, then `ReasoningChoicesFor` of `a.cfg`) answers from the listing; `ReplaceConfig` with a hand-built config binds it; a second manager on the same config object takes the lineage over; a config of another lineage is never answered from this manager's cache; the guard test over `LoadWithPaths(` callers; `SetContextWindowLister` is the stand-in.

and add to the `internal/config` bullet: *a config loaded from `c.Paths` shares `c`'s cell, a `Paths` with no cell yields "not known" for the five methods, `WithListing` / `AttachListingIfEmpty` semantics.*

**7, D1** (replace the whole entry, keep the heading and the numbering of the others):

> ### D1. How a `*config.Config` reaches the manager's listing cache - decided (model `p2-d1-config-source`)
>
> **Decision.** R5 with a backstop in `storeConfig` (3.5): the source rides on `config.Paths` as one atomic cell shared by every configuration loaded from one `Paths`, attached by `NewManager`;
> `storeConfig` attaches an empty cell for a configuration no loader made; the source finds the row in the reader's `cfg`; reads are live.
> **Evidence** (engine `mcd 0.2.0`, every verdict `exhaustive`, `docs/plans/remote-model-provider-models/p2-d1-config-source.md`): R1 fails on the skills reload even with the agent's re-bind (`unbS`, `blind`) and on the agent's private reloads without it (`unbA`, `blind`, `liveA`); its minimal fix
> (re-bind in the agent AND store in the manager first in `skills_mgmt.go`) holds; R2, R3, R5 and R6 have no reachable unbound read on any production path; a second manager on one config object is
> answered from the last one in R1, R2, R5, R6 and not in R3; R4 writes the listing into the operator's keys and calls `storeConfig` per refresh; a source that finds the row in the manager's current config
> answers another row after a repoint in every mechanism; live reads keep "refused at most once after a change", frozen per turn without recovery does not, frozen per config does not.
> **Depends** (settled): 3.3, 3.5, 3.9, S1; no change in `internal/agent/{react,resume_permission}.go` or `external/httpserver/skills_mgmt.go`.

**8, S1 row** and the sentence after the table:

> | **S1** resolver and lineage binding (A.2-A.4) | `internal/config/{listing,paths,config,recovery,check,reasoning,capability_readers_guard_test,listing_guard_test}.go` (`listing.go`: `ListedModel`, `ListingSource`, `ListingCell`, `listedFor`; `paths.go`: the `listing` field, `WithListing`, `AttachListing`, `AttachListingIfEmpty`; `config.go`, `recovery.go`, `check.go`: the loaders allocate the cell), `internal/session/{capability_listing.go, manager.go (two call lines: NewManager and storeConfig)}`, `external/httpserver/reasoning_levels_http.go`, tests | S0 | S2, S4, S6, S7 |
>
> The model of D1 is made (`p2-d1-config-source`): S1 does not touch `internal/agent/{react.go,resume_permission.go}` or `external/httpserver/skills_mgmt.go`, and S1 is not a sweep stage; the one-record rule of 3.3 is one line in `coddyProviderInput`, owned by S3 (`coddy_provider.go`). The models of D2 to D4 are made before S3 and S4 start; their reports go to `docs/plans/remote-model-provider-models/` (m8 to m10) and may change the file lists marked above.

**8a**, replace the first row's second cell: `config.ListedModel`, `config.ListingSource`, `config.ListingCell`, `config.Paths.{WithListing, AttachListing, AttachListingIfEmpty}`, the unexported `(*Config).listedFor` | S1 | S1 only; `Manager.ListedModel(cfg, provider, apiModel)` implements the source.

**9, Concurrency bullet**: replace "and the listing binding" with "the listing cell is one `atomic.Pointer`, no lock".

Optional row for the table of section 2: **W11** | 3.5/7-D1: "observers run after `storeConfig`" | `skills_mgmt.go:546-556` stores the loaded config in the HTTP server before the manager; the agent keeps its own second load (`react.go:425`, `resume_permission.go:399`) | R1 leaves the server's copy unbound until the manager binds it and, without `ShareListing`, the agent's for the rest of the turn; D1 is decided on `config.Paths` (3.5).

## 11. Limitations

Results are about the model: one alias, two cache keys, a class that offers one level, one turn of at most 2-3 calls, at most 2 local edits, 2 remote changes, 2 extra readers; the scale rows
(NEDIT=2: `r1a-e2`, `r1c-e2`, `r6-e2`, `r6-hand-e2`) keep the verdicts of NEDIT=1. A budget stop never happened (all rows `complete`). The retry after refresh, the usage cache, the wire and the
content of the capability record are outside; `lost` and `blind` are consequences read from the model, their weight for users is not measured; `fnonop`/`stfromref` are verified for the
non-R4 mechanisms only because those have no write path in the model. The engine panic of m6 (`atomic` traces) was avoided by `d_step`. Transferring a verdict to the code needs the conformance
argument of section 8 (last row). The header comment of the `.pml` was extended after the runs (no change to the model text). Own token use: about 450k tokens.

