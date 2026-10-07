# m4-auth-relay: authentication policy with the LLM-only token class, and the relay's use of a node's token

Models: `m4-auth-relay.pml` (policy, model 1) and `m4-auth-relay-relay.pml` (registry + mount + sessions fan-out, model 2).
Spec: `docs/plans/remote-model-provider.md` 4.1 (ack, alias), 4.2 first paragraph, 4.5, 4.5a, 5a (Config, Auth, Relay rows), 6.
Engine: `mcd 0.2.0` (CLI; no `simulate`, no `lint`: the properties were classified by hand with `properties-ltl-ctl.md`).
Everything below is a statement about the models, not about the Go code; where the code was read instead, it is said.

## 1. Intake card

| Field | Model 1 (policy) | Model 2 (relay) |
|---|---|---|
| Boundary | one `coddy serve` HTTP surface: `authPolicyNow`, `authGate`, `isProtectedPattern`, handler of the three LLM routes. Excluded: cookie same-origin check, `?access_token=` capabilities, `publicDocs`, handler bodies (collapsed to ALLOW/404) | relay registry entry of ONE node, the mount (`rewriteFor`), the aggregated sessions fan-out (`askNode`), the node's own gate (model 1 NEW policy, copied) |
| State | running config: main token, web login, >=1 shared token, some `shared_as`, `allow_insecure`, loopback listener, "one token in two classes" flag, out-of-band main token (`xm`); last request record | node join token class, node gate config (main, shared, login, shared_as, allow_insecure), lease (exists, age), token held by the relay, node up/down, last request record |
| Atomic step | one request decided against ONE snapshot (`rec` guard: nothing interleaves between decision and its flags), or one hot reload (single-field change; boot picks any config) | one registration/renewal, one config change, one crash/restart, one clock tick, one client request |
| Environment | caller credential in {none, main, shared, other, cookie}; route in {llm_models, llm_usage, llm_completions, other_api, v1_chat, v1_responses_agent, public_static}; model in {alias, selector} | client route in the six mounted classes or the sessions fan-out |
| Assumptions | tokens are classes; hot reload is atomic and a refused candidate leaves the running config (an assumption: the config code was not model-checked); the 403 rule keyed on the live policy | discrete clock, TTL=2, GRACE=2; DIRECT: a node that is up renews before the lease expires (tick blocked otherwise); TUNNEL: lease frozen while connected, a crash makes it offline at once; reaping is eager (observationally equal to the lazy reap, every reader reaps first); `nlg`/`nins` fixed to 0 in the main runs (a `-D FULLCFG` run covers them, TUNNEL, no crash) |
| Fairness | none (safety/reachability only) | none for safety; liveness: none, then weak (process-level, engine) |

Model class: finite, untimed; time only as the discrete clock above (declared abstraction, not a timed automaton).
Model 1 baseline 9,155 states (complete); model 2 DIRECT 415,053, TUNNEL 108,165 (complete); LIVE scenarios <= 1,275.
Property ids use the engine's names; flags `vN[k]` (NEW), `vO[k]` (OLD), `vH[k]` (OLD+H) are 0/1 "violation" variables set in the step that decides the request.
`OLD` = today's `authGate` with the new routes registered (enabled = main || login, shared tokens unknown, no handler check). `OLD+H` = OLD plus the handler's 403 rule keyed on OLD `enabled`.

## 2. Model 1: properties and results

All safety properties are `AG !v[k]` (CTL, see engine note E1); reachability sanity is `EF`. Status/evidence exactly as returned.

| id | Property | NEW | OLD | OLD+H |
|---|---|---|---|---|
| P1 | a valid shared token is never allowed on a non-LLM route (other_api, v1_chat, v1_responses_agent) | verified / exhaustive | violated | violated |
| P2 | any shared token turns the gate on: anonymous or invalid credential gets 401 on every protected route | verified | violated | violated |
| P3 | share-only node (no main, no login): non-LLM routes 401 for every caller, LLM routes only for the shared token | verified | violated | violated |
| P4 | `shared_as` + no credential of any class + not `allow_insecure`: the three LLM routes answer 403 (all states, loopback or not) | verified | violated | verified |
| P5 | main token and login session reach everything (no 401/403) | verified | verified | verified |
| P6 | selector as `model` on `/coddy/llm/completions` (and `/usage`) is never served (404 after auth) | verified | verified | verified |
| P7 | a valid shared token gets the shared routes (functional) | verified | violated | violated |
| P8 | public static shell never refused | verified | verified | verified |
| P9 | `allow_insecure` changes nothing once any credential exists | verified | n/a | n/a |
| P10 | the listener address (loopback or not) changes no decision | verified | n/a | n/a |
| P11 | no credential and no shared row: node behaves as before (never 401/403) | verified | verified | verified |
| P12 | no shared token: non-LLM routes decide exactly as today (no regression) | verified | n/a | n/a |
| P13 | P4 restated for the first request after a hot reload that removed the last credential (`-D GHOST`) | verified | n/a | n/a |
| P14 | `AG !g_dup`: the running config never has one token in two classes (loader checks `config` tokens) | verified | | |

`deadlock` (the model's own) is `verified / exhaustive` in every run. Sanity (`EF`, all `verified / exhaustive`, so the antecedents are reachable):
share-only config with a shared token served on llm_models; share-only config, anonymous on other_api = 401; the 403 state on a non-loopback AND on a loopback listener;
shared token + main token present, shared token on other_api = 401; main token on v1_responses_agent = ALLOW; selector with main token = 404; login cookie on other_api = ALLOW;
open node with `shared_as` and `allow_insecure` serves anonymous; open node without shared row serves anonymous; shared token on completions with alias = ALLOW;
anonymous with `allow_insecure` but a shared token configured = 401; `EF rej_dup` (a duplicate candidate is refused, `-D GHOST`); the first request after a reload that removed the last credential gets 403 on a loopback listener (`-D GHOST`).

### 2.1 Anti-vacuity: mutants of the NEW policy (same property list, `violated` = the property can fail)

| Mutant (`-D`) | What it breaks | Properties violated | Decoded counterexample (final state) |
|---|---|---|---|
| `MUT_STARTUP` (403 rule keyed on the credential state at start-up, like the `authOn` of `serve_http.go`) | hot reload takes effect only in part | P3, P4, P5, P7, P9, P13 | booted with a credential, a reload removed it: `shared_as=1`, no credential, anonymous llm_models -> ALLOW; booted open, a reload added a shared token: valid shared holder -> 403 |
| `MUT_LOOPBACK` (403 rule skipped on a loopback listener, the exemption of the `serve_http.go` warning) | loopback is not a boundary | P4, P10, P13 | `loopback=1`, `shared_as=1`, no credential, anonymous llm_models -> ALLOW |
| `MUT_NOCLASS` (shared token admitted like a main token) | the class | P1, P3 | share-only, shared token on `/coddy/sessions` -> ALLOW |
| `MUT_NODUP` (no duplicate check) | `AG !g_dup` | P14, plus P5 when the duplicate resolves to least privilege | `main=1 shared_tok=1 dup=1`: main holder on other_api -> 401 (lockout) |
| `MUT_NODUP -D MAIN_FIRST` | same, duplicate resolves to main | P14, P1 | `dup=1`: shared holder on other_api -> ALLOW (escalation) |

### 2.2 Out-of-band main token (new finding, not in the spec)

`-D XTRA` adds a main token that arrives by `--auth-token` / `CODDY_HTTP_TOKEN` (`SetExtraAuthTokens`, `authPolicyNow` appends it to `pol.tokens`). The loader never sees it.

| Variant | `AG !g_dup` | P1 | P5 | Reading |
|---|---|---|---|---|
| `XTRA` (loader-only check), shared-first | violated | verified | **violated** | a shared token equal to `--auth-token` is accepted; the operator's own main token is cut off from every non-LLM route (lockout, fails safe) |
| `XTRA -D MAIN_FIRST` | violated | **violated** | verified | the same, the holder of the shared token gets the whole API (escalation) |
| `XTRA -D CHECK_EXTRA` (shared-first and main-first) | verified | verified | verified | the check runs where both lists meet, every property holds (the XTRA runs have 22,887 states) |

### 2.3 Two reads of the policy (`-D TWO_READ -D SMALL`, 21,027 states, complete)

The gate reads the policy (config A); a hot reload may fall between gate and handler; the handler of 4.2 "is refused by the live policy" reads again (config B).
PT1 (H1, handler re-reads): "a request that is ALLOWed is allowed under A or under B" is **violated**. PT2 (H2, handler uses A's snapshot) is **verified**.
Decoded PT1: A has no credential and no `shared_as` (gate off, the anonymous request passes the gate), reload to B (shared token + `shared_as`), handler under B finds the alias and answers ALLOW:
the request is 404 under A, 401 under B, and is served. Classification: system/spec defect (minor, one in-flight request per reload, no stored state), fixable by one policy snapshot per request.
The same run reports P1 violated on the final answer: **property defect** (the credential was judged against B while the gate decided under A, an open node); it is not a finding.

### 2.4 Decoded counterexamples of the OLD logic (the broken comparison)

| Property | Final state of the trace | Classification |
|---|---|---|
| P1, P2, P3 (OLD and OLD+H) | `main=0 login=0 shared_tok=1`: shared token on `/coddy/sessions` -> ALLOW; anonymous on llm_models -> ALLOW | the F2 defect of the plan's history as I read it: `enabled = len(tokens)>0 \|\| login.enabled` leaves a share-only node open on every route (4.5, fail-closed bullet) |
| P4 (OLD) | `shared_as=1`, no credential, `allow_insecure=0`, loopback=0: anonymous llm_models -> ALLOW | the F3 defect as I read it: shared routes served on a node with no authentication (4.5, "require authentication unconditionally") |
| P7 (OLD) | `login=1 shared_tok=1`: valid shared token on llm_models -> 401 | the class is unknown to the gate, so the token never works next to a main token or login |
| P7 (OLD+H) | `shared_tok=1 shared_as=1`, share-only: valid shared token on llm_models -> 403 | coupling: the 403 rule keyed on OLD `enabled` refuses the very credential the node was configured with; "credential configured" must be ONE definition shared by gate and handler |

I could not find the labels F2/F3 in the repository or the plan; the mapping above is from the plan's text, please map it to your history.

## 3. Model 2: properties and results

`w1..w7` are `AG !w[k]`, `w8..w10` are `EF w[k]` (witnesses), per transport.

| id | Property | DIRECT | TUNNEL | `OLD_NODE` (TUNNEL) | `MUT_FALLBACK` (TUNNEL) |
|---|---|---|---|---|---|
| w1 | while the lease is alive, a relay client's privilege never exceeds that of the registered token (node auth on) | verified / exhaustive | verified | violated | violated |
| w2 | a share-only join token: never other_api / v1_chat / v1_responses through the mount (node auth on) | verified | verified | violated | verified |
| w3 | node auth on + empty token: every mounted call, LLM routes included, is 401 | verified | verified | violated | violated |
| w4 | empty token + open node + `shared_as` + not `allow_insecure`: the LLM routes answer 403 | verified | verified | verified | verified |
| w5 | sessions warning iff the node's token cannot reach the sessions route (node online and reachable) | verified | verified | violated | violated |
| w6 | no lease, no token (expiry clears the entry) | verified | verified | verified | verified |
| w7 | a share-only join token works on the LLM routes of a node that holds shared tokens | verified | verified | violated | verified |
| w8 | EF: empty token + open node: other_api ALLOWed (the documented hazard) | verified (reachable) | verified | | |
| w9 | EF: join config has no token, relay still presents the old main token, other_api ALLOWed | verified (reachable) | verified | | |
| w10 | EF: shared token stale, node gate off, other_api ALLOWed through a share-only join | verified (reachable) | verified | | |

States: DIRECT 415,053, TUNNEL 108,165, OLD_NODE 112,059, MUT_FALLBACK 107,685; with `-D FULLCFG -D TUNNEL -D NO_CRASH` (node login and `allow_insecure` also change) 27,871, all ten as above.
Sanity (`EF`, DIRECT and TUNNEL, all `verified / exhaustive`): 403 on an open node through the mount; 401 with an empty token and node auth on; share-only join serves models and completions(alias);
the sessions warning appears with 401 and is absent with ALLOW; a share-only join gets 401 on other_api; a main join gets ALLOW; a mount call on an offline lease is 502; an unknown node is "none"; an offline node warns.

### 3.1 Stale token after the node's configuration drops it (liveness, scenario `-D LIVE`)

Start: lease holds `main`, node joined with `main`; the one reconfiguration is `n_join := empty`. A process `idle` keeps the world moving (engine note E2). phi1 = `[] ((n_join == 0) -> <> (reg_tok == 0))`, phi2 = `(<>[] (node_up == 0)) -> phi1` (explicit premise: the node falls silent for good).

| Scenario | phi1 no fairness (`AG(... -> AF ...)`) | phi1 weak | phi2 no fairness | phi2 weak |
|---|---|---|---|---|
| DIRECT, node never crashes | violated | violated | verified (vacuous: premise impossible) | verified (vacuous) |
| DIRECT, crash, no restart | violated | verified (fairness artefact: weak fairness forces the crash) | violated (fairness artefact: the clock may starve) | verified |
| DIRECT, crash + restart | violated | ENGINE-PANIC (E1), inconclusive | violated | verified |
| TUNNEL, same three | violated, violated, violated | violated (never crashes), verified (artefact), ENGINE-PANIC | verified (vacuous), violated, violated | verified (vacuous), verified, verified |
| DIRECT, `MUT_EMPTY_ERASES` (empty token erases), never crashes | violated | verified (dropped at the next heartbeat) | | |

Reachability (`EF`, DIRECT and TUNNEL, verified): the token is dropped (`has && reg_tok == 0 && n_join == 0`: the node stayed down longer than TTL+GRACE ticks and returned);
the node restarts inside the window and the lease keeps the stale `main` token (`crashed && node_up && has && reg_tok == main && n_join == 0`); a stale token is presented while the node is up.
Reading: the stale token is dropped **only if the node stops renewing its lease for longer than TTL+GRACE** (2 x ttl, 180 s by default). A node that keeps heartbeating (or keeps its tunnel) keeps the stale token **forever**; the weakly fair lasso is heartbeat, tick, heartbeat, tick. Classification: spec incompleteness (4.5a last bullet), consequence of the code line `if req.Token != ""` in `registry.go`.
The model contrasts the rejected alternative: with "empty token erases" the token disappears at the next DIRECT heartbeat (weak fairness), but a TUNNEL node registers only on (re)connect, so there the erase would act only at the next reconnect (not rerun after the `idle` fix; it follows from the construction).

## 4. Statements of the plan: CONFIRMED / CONTRADICTED / NOT DECIDABLE

| # | Statement (section) | Verdict | Evidence |
|---|---|---|---|
| 1 | routes always registered, refusal by the live policy, a hot reload takes effect without restart (4.2) | CONFIRMED with a correction | P4, P13, `MUT_STARTUP` violated; the handler must use the gate's snapshot (PT1 violated, PT2 verified) |
| 2 | `authGate` admits a shared token only on the three LLM routes, refuses it elsewhere like an unknown token; the main token works everywhere (4.5) | CONFIRMED | P1, P5, P7; `/v1/chat/completions` and `/v1/responses` are in P1 |
| 3 | any shared token turns the gate on; a share-only node refuses everything else to every caller, anonymous included (4.5) | CONFIRMED, wording to tighten | P2, P3; OLD violates (F2). "everything" is everything protected: static shell and sign-in routes stay public (P8), `/docs` and `/openapi.*` when `public_docs` |
| 4 | `coddy -t` says that its API and web UI are closed (4.5) | CONTRADICTED in wording | the shell is served (`isProtectedPattern("/") == false`); only the API is closed and the UI cannot sign in |
| 5 | the start-up `authOn := tokenOn \|\| loginOn` learns the shared tokens too (4.5) | NOT DECIDABLE here | it only drives two log warnings (`serve_http.go` 75-97); enforcement reads the live policy. The warning's loopback exemption must not leak into the 403 rule (`MUT_LOOPBACK`) |
| 6 | a shared-model token equal to a main token or a swarm token is a load error, so a token is never of two classes (4.5, 5a Config) | CONTRADICTED in part | true for `httpserver.auth_token`; `--auth-token` / `CODDY_HTTP_TOKEN` are added by `SetExtraAuthTokens` after load, the loader cannot see them (section 2.2). Swarm tokens (`--swarm-auth-token`, `CODDY_SWARM_TOKEN`, the generated token) have the same shape, not modelled |
| 7 | shared routes need a credential unconditionally; `403 kind auth` on an open node, on a loopback listener, after a hot reload that removes the last credential; `allow_insecure` the only way out (4.5, 5a Auth) | CONFIRMED | P4, P9, P10, P13; P11/P12: only the three LLM routes refuse, the rest of an open node stays open as today. Anonymous gets 403 only while NO credential exists, once any exists it gets 401 |
| 8 | `provider/model` as `model` on `/coddy/llm/completions` is a 404 (4.2, 5a) | CONFIRMED | P6; the 404 comes after authentication, an anonymous caller gets 401/403 |
| 9 | a shared token on `/v1/chat/completions` is refused (feature) | CONFIRMED | P1 |
| 10 | a rejected config leaves the running one (4.1 ack, 4.5 duplicates) | NOT DECIDABLE | modelled as an assumption (a refused candidate changes nothing); the 4.1 ack/alias rules are load-time validation, no behaviour to check |
| 11 | the token travels in the registration for both transports and is used by the mount, the sessions fan-out and the topology of child relays only (4.5a) | CONFIRMED for mount and sessions; topology NOT CHECKED | code read (`rewriteFor`, `askNode`), model w1..w7; topology code was not read |
| 12 | a tunnel node's own gate judges tunnel requests exactly as direct ones (4.5a) | CONFIRMED | `Handler()` is `corsMiddleware(authGate(mux))` (`server.go:350`); w1..w10 give identical verdicts for both transports |
| 13 | no token: node auth on = every mounted call 401; node auth off = open to every relay client; no fallback to the relay's own token for agents (4.5a) | CONFIRMED, with one refinement | w3, w8; `joinset.go:73` falls back only for `KindRelay`; `MUT_FALLBACK` violates w1, w3, w5. Refinement: through an open node the LLM routes still answer 403 when `shared_as` is set and `allow_insecure` is not (w4) |
| 14 | joining with a shared-model token works through the relay for both transports with no relay change; the sessions aggregation shows `<node>: 401 Unauthorized`; opening the node as an environment is refused (4.5a) | CONFIRMED | w7, w5 (DIRECT, TUNNEL). The warning is also produced by a node with auth on and an EMPTY token; a sessions call never answers 403 (only the LLM routes do) |
| 15 | one join token means one privilege (4.5a) | CONFIRMED, qualified | w1, w2 hold while the node's auth is on; if the node later drops all credentials the node is open and the join token no longer limits anything (w10 reachable) |
| 16 | a relay client token opens the mounts of every node, each with that node's token; share-only nodes belong on a dedicated relay (4.5a) | CONFIRMED by construction, not model-checked as a product | mounts are independent per node (`mountedPrefixes` carries `/v1/` and `/coddy/` for all); a two-node product was not built |
| 17 | a re-registration with an empty Token does not erase the old one; the relay keeps presenting it "until the lease expires" (4.5a) | CONFIRMED literally, CONTRADICTED as an operational statement | section 3.1: a node that keeps renewing keeps the stale token forever |
| 18 | sessions warning appears iff the node's token cannot reach the sessions route (task) | CONFIRMED (online, reachable node) | w5; plus the `offline since` and transport-error warnings of `sessions.go` |

## 5. Decisions

The plan has no `[decide at modeling]` item in sections 4.1, 4.2 (first paragraph), 4.5, 4.5a. Two decisions the models force, recommended:

1. **Duplicate resolution** (when a token is in both classes despite the check): least privilege (shared wins) is the only variant for which a missed duplicate cannot grant the whole API (P1 holds, P5 fails safe). Evidence: sections 2.1, 2.2. The check itself must run where both lists meet.
2. **Policy snapshot** for the handler: one `authPolicyNow()` per request, passed to the handler (H2). Evidence: section 2.3 (PT1 violated, PT2 verified).

## 6. Recommended spec changes (replacement text)

**4.2, first paragraph, last sentence.** Replace "a request is refused by the live authentication policy in the handler (4.5), so a hot reload of the configuration takes effect without a restart." with:
"A request is judged by the live authentication policy: `authGate` takes ONE snapshot of it per request (`authPolicyNow()`), stores it in the request context, and the handler of the three routes reads that snapshot and never re-reads the configuration, so a hot reload takes effect on the next request without a restart and a request that straddles a reload is answered by one policy, never a mixture of the two."

**4.5, Fail-closed bullet.** Replace the sentence "`authPolicyNow` counts it next to the main tokens and the web login" with:
"`authPolicyNow` keeps the shared-model tokens in a field of their own (`sharedTokens`), never in `tokens`: `enabled = len(tokens) > 0 || login.enabled || len(sharedTokens) > 0`, `acceptBearer(tokens, ...)` stays class-blind for the main class only, and every other consumer of `tokens` (the media-capability signing key `workspaceSigningKey` uses `tokens[0]`, `coddyAuthMeGet`, `decorateConfigDocument`) ignores `sharedTokens`. A shared token must never become key material or count as 'a token is configured' for the UI." (Reason: if shared tokens were appended to `pol.tokens`, a share-only node would derive the workspace capability key from a shared token, and the holder could mint capabilities. This is a hazard found by reading `workspace_viewer.go:378-385`, not by the model; the model only shows the class-blind variant as `MUT_NOCLASS`.)

**4.5, same bullet, last sentence.** Replace "A shared-model token equal to a main token or a swarm token is a load error, so a token cannot be of two classes at once." with:
"No token may be of two classes. The check compares the shared-model tokens with every main-class token, the ones in the configuration AND the ones given by `--auth-token` / `CODDY_HTTP_TOKEN`, and with the swarm tokens (configured, `--swarm-auth-token`, `CODDY_SWARM_TOKEN`); it runs at start-up, at every configuration install (hot reload, `PUT /coddy/config`) and in `coddy -t`, which sees the environment. A refused candidate leaves the running configuration. If a duplicate ever reaches the gate, the token is treated as shared (least privilege)."

**4.5, same bullet, second sentence.** Replace "A node with only shared-model tokens refuses everything else to every caller, and `coddy -t` says that its API and web UI are closed." with:
"A node with only shared-model tokens refuses every protected route except the three LLM routes to every caller, anonymous included; what stays public is what is public on any node (the static shell and the sign-in routes, and `/docs`, `/openapi.*` under `public_docs`), and `coddy -t` says that its API is closed and the web UI cannot sign in."

**4.5, 'shared routes require authentication' bullet, add.** "The refusal is `403 kind: auth` while no credential of any class is configured; as soon as one is (a main token, the login or a shared-model token) an anonymous caller gets the gate's `401`. The condition 'no credential is configured' is one function used by the gate and by the handler; the handler never evaluates it from the start-up state or from the listen address."

**4.5a, third bullet (node joins with no token).** Append: "With node auth off the node is open to every relay client on every route except the three LLM routes, which still answer `403 kind: auth` when a row has `shared_as` and `allow_insecure` is not set; sharing through a relay therefore needs a token on the node."

**4.5a, last bullet.** Replace with: "A re-registration with an empty `Token` does not erase the old one (`registry.go`). The stale token therefore survives the removal of `swarm.join[].token` for as long as the lease is renewed: a direct node renews on every heartbeat and a tunnel node keeps one lease for the whole connection, so for a node that stays joined it never expires on its own. It is dropped only when the node is silent for longer than the offline grace (ttl + grace, 180 s by default) and registers again, or at once by rotating to a different non-empty token, by `DELETE /swarm/nodes/<name>` on the relay (the node's next registration is a fresh lease with the new, empty token), or by restarting the relay. To cut a node off the relay, evict it, do not only edit its configuration."

**4.5a, second bullet (sessions).** Replace the warning text by: "`<node>: 401 Unauthorized` whenever the node answers 401 to its sessions route: a join token that is a shared-model token, an empty token on a node with authentication, or a stale or rotated token; an unreachable node warns with the transport error and an expired lease with `offline since`."

**5a, Auth row, add cases.** (a) a shared token equal to `--auth-token` or `CODDY_HTTP_TOKEN` is refused at start-up and on reload; (b) the handler is called with a gate snapshot that differs from the live configuration and answers by the snapshot; (c) `public_docs` on a share-only node leaves `/docs` open and nothing else; (d) the workspace capability key does not change with the shared-model tokens (add and remove one, a minted capability stays valid); (e) the first request after a hot reload that removed the last credential answers 403 on a loopback listener.
**5a, Relay row, add cases.** a stale token survives a heartbeat with an empty token; `DELETE /swarm/nodes/<name>` followed by the node's next registration leaves the entry without a token; the sessions warning for a share-only join and for an empty join token on an authenticated node.

## 7. Engine notes (for the tool owner)

- **E1.** `mcd check --ltl '[] !vO[1]'` on `m4-auth-relay.pml` (violated safety on a 9k-state graph) and `--ltl phi1 --fairness weak` on `m4-auth-relay-relay.pml -D LIVE` (DIRECT/TUNNEL, crash + restart) end in `panic: runtime error: index out of range` in `explore.(*cycleSearch).stateOf` (`cycle.go:998`, from `lasso`), exit non-zero, no report; `--bfs` does not help. Safety was therefore asked as CTL (`AG !v`, complete graph, counterexample works). The two panicking liveness cells are reported `inconclusive` above.
- **E2.** Under `--fairness weak`, a model whose processes all end blocked yields a `violated` lasso made only of null steps (`-`, "no process moves") through an accepting claim state, although the claim state is blocked in the final state. Adding an always-enabled `idle` process (the world does not stop) removed it; the four `violated` weak cells of the first liveness run (NO_RESTART, both transports, phi1 and phi2) were of this kind and are not reported.
- **E3.** Bitwise operators and `?:` are outside the subset; the models use `if` instead.

## 8. Reproduce

```
cd docs/plans/remote-model-provider-models
M=~/.claude/skills/model-check/engine/bin/mcd
# model 1, one process per k (1..12; vO/vH for OLD, OLD+H); add -D for the mutants
$M check --promela m4-auth-relay.pml --ctl 'AG !vN[1]' --no-timing --budget-ms 300000 --budget-states 3000000
$M check --promela m4-auth-relay.pml --ctl 'AG !g_dup' --no-timing
$M check --promela m4-auth-relay.pml -D MUT_STARTUP|MUT_LOOPBACK|MUT_NOCLASS|MUT_NODUP [-D MAIN_FIRST] --ctl 'AG !vN[k]' --no-timing
$M check --promela m4-auth-relay.pml -D XTRA [-D CHECK_EXTRA] [-D MAIN_FIRST] --ctl 'AG !vN[k]' --no-timing
$M check --promela m4-auth-relay.pml -D GHOST --ctl 'AG !vN[13]' --no-timing
$M check --promela m4-auth-relay.pml -D TWO_READ -D SMALL --ctl 'AG !vN[14]' --no-timing     # also vN[15]
$M check --promela m4-auth-relay.pml --ctl 'EF (rec && g_sh && !g_m && !g_lg && !xm && rq_c == 2 && rq_r == 0 && dn == 0)' --no-timing   # sanity
# model 2
$M check --promela m4-auth-relay-relay.pml [-D TUNNEL] [-D OLD_NODE|-D MUT_FALLBACK] --ctl 'AG !w[1]' --no-timing   # w1..w7; 'EF w[8]'..'EF w[10]'
$M check --promela m4-auth-relay-relay.pml -D LIVE [-D TUNNEL] [-D NO_CRASH|-D NO_RESTART] --ltl '[] ((n_join == 0) -> <> (reg_tok == 0))' --fairness weak --no-timing
$M check --promela m4-auth-relay-relay.pml -D LIVE [...] --ltl '(<>[] (node_up == 0)) -> [] ((n_join == 0) -> <> (reg_tok == 0))' --fairness none|weak --no-timing
$M check --promela m4-auth-relay-relay.pml -D LIVE [...] --ctl 'AG ((n_join == 0) -> AF (reg_tok == 0))' --no-timing    # no fairness
```
The runner scripts are in the scratchpad (`runp.sh`, `runr.sh`, `runlive.sh`, `sanity.sh`); they only loop over the commands above.

## 9. Limits (what the models do not say)

- The results are about the models. `external/httpserver/auth.go` is the pre-feature code: the NEW policy is the plan's design, so "CONFIRMED" means the design is consistent, not that the code does it.
- Tokens are classes; constant-time compare, `Bearer` parsing, `?access_token=` for the two SSE routes and the cookie same-origin rule are out of scope.
- Model 2 keeps the node's login and `allow_insecure` at 0 in the main runs (covered once with `-D FULLCFG`), has one node (independence argument for item 16), and an abstract clock with TTL=2, GRACE=2; the heartbeat-within-TTL assumption is part of the DIRECT model.
- The phi1-weak cell with crash and restart is `inconclusive` (engine panic); the verdicts around it (no-fairness `violated`, reachability of a reconnect that keeps the stale token) support "violated", they do not replace it.
