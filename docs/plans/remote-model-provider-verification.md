# Plan: model-checking every change of the remote model provider branch

**Status: running (2026-10-09).** A standing directive of the session, recorded here so the work survives a restart: every change on `feat/remote-model-provider` is implemented **and** checked with the model checker, in Promela and in Petri nets, the session going on by itself without waiting for a request between steps. Models and reports live in `remote-model-provider-models/`; the engine is `mcd` (skill `model-check`).

## 1. The procedure of one step

1. **Pick** the change and what a model of it must decide or hold (the properties, the one assumption that matters, the abstraction).
2. **Write** the model (`p5-<name>.pml`, or `.json` for a net) with a header that names the code it stands for, the options, the properties and what the model is not. One property per run for Promela (`-D ASSERT_PROP=<latch>`), a ghost latch per property, sanity latches that must be reachable, mutants that must be caught.
3. **Parse** it (`mcd parse`), then **check the host's resources before the run**: `wait-for-capacity.sh` (or `uptime`, `free -m`, `df -h`) here, and on `ssh relay-huron` for a run that needs more than a million states. A run is never started into a saturated machine.
4. **Run** the property matrix and the mutants; keep the commands.
5. **Cross-review the model and its results** (the crossreview skill; briefs small enough for the smallest reviewer), verify every finding by re-running the model or a mutant of it, **fix** the model, and where a finding is about the code, fix the code test-first.
6. **Cross-review the fixed results**, write the report (`p5-<name>.md`: commands, verdicts, what is not modelled, what the review changed) and record the verdict in the plan of the change.

## 2. What the branch changed and which model holds it

| Change | Model | Formalism |
|---|---|---|
| The per-credential slot, the one release (phase 1) | `m1-slots` | Promela (done) |
| The busy wait inside the provider (phase 1) | `m2-busy-wait` | Promela (done) |
| The stream frames, the client's typed errors (phase 1) | `m3-stream` | Promela (done) |
| The token classes, the fail-closed gate, the relay's use of a node's token (phase 1) | `m4-auth-relay` | Promela (done) |
| The three timers, the heartbeat, the per-write deadline (phase 1) | `m5-guards` | Promela (done) |
| How the capabilities of a row arrive (phase 2) | `m6-capabilities` | Promela (done) |
| Config source, usage cache, vanished peer (direct, tunnel, h2), the busy notice (phase 2) | `p2-d1` to `p2-d4` | Promela (done) |
| Scoped relay clients: exact hop paths, the per-request identity, the windows and the refund, the label, the closed route table (phase 3) | `p3-d1` to `p3-d5` | Promela (done) |
| The application probe: first-ping arming, the grace, the tie, the foreign pinger (phase 4a) | `p4-probe` | Promela (done) |
| **The registry's lease and token** (H0: the owner's renewal replaces the token, an empty one included; eviction, expiry, the grace, a pinned upstream, `swarm.node_tls` inherited by a lease with no dial of its own) | `p5-registry` | Promela, **done**, two review rounds: [report](remote-model-provider-models/p5-registry.md). R1 to R7 hold in the code's configuration (1.3 M states); the second round found that a missing or late reap was invisible to every latch (R7 now guards it), and that a disclosed secret is seen by R6 and not by R2 |
| **The node's gate with every credential class** (main, shared, client certificate name, cookie, anonymous, `allow_insecure`, `required` / `optional` client auth; the four shared routes against every other) | `p5-gate` | Promela, **done**, two review rounds: [report](remote-model-provider-models/p5-gate.md). G1 to G14 hold in the model of the code (34,945 states = 2 x 17,472 + 1 inputs), each of 20 mutants is caught by its designed latch, and the decision is an ordered chain as the code is. The review found properties that read the same route classification as the decision (blind to a regression of the route test), no latch for the other direction (a configured credential refused) and none for a wrong token; open to the operator: under `client_auth: required` a request over the reverse tunnel carries no certificate (documented) |
| **The client certificate loader** (the pair read at each handshake, compared by its bytes, a rotation that replaces the two files one after the other, a renewal that keeps the key, concurrent handshakes) | `p5-certloader` | Promela, **done**, two review rounds: [report](remote-model-provider-models/p5-certloader.md). The model found the defect of the first loader (a pair replaced with the same size and time was served stale: fixed test-first); the code holds L1 to L4 in every configuration, and the second round added a regression test for a key replaced alone and for bytes cached before the parse |
| **The client's pinger and the node's probe handler** (the goroutine ends with the stream however it ends, the join cannot deadlock, a ping racing the stop) | `p5-pinger` | Promela, **done**, two review rounds: [report](remote-model-provider-models/p5-pinger.md). N1 to N7, C1 and C2 hold (1.0 M states, 3.3 M at three pings) and a companion model with the handler as a process of its own (`p5-pinger-split`, 4.1 M states) gives the same verdicts; the review found a double-cancel mutant that was an artefact of the timer (now one-shot) and a guard that cuts and never cancels that no latch caught (N7) |
| **The registry's lease lifecycle** as a net (free, live, offline, pinned; the home state is reachable from every marking, one owner at a time, a release only with a request behind it; and with no one there, only the clock frees a lease) | `p5-lease-net`, `p5-lease-unattended-net` | Petri nets, **done**, two review rounds: [report](remote-model-provider-models/p5-nets.md) |
| **The relay's per-client slot and window with the refund** as a net (conservation of slots, the refund restores exactly one token, a refused call spends none, the node held by another client) | `p5-relay-limits-net`, `p5-relay-limits-busy-net` | Petri nets, **done**, two review rounds: [report](remote-model-provider-models/p5-nets.md). The first version could not see the refund (the clock restores every token): R7 and the busy net now do |
| **The probe call's slot** as a net (one slot, one release whichever of the completion, the cut and the other cancels comes first, no leaked marking) | `p5-probe-slot-net` | Petri net, **done**, two review rounds: [report](remote-model-provider-models/p5-nets.md) |
| **TLS at the transport level only, with a built-in PKI** (phase 4b, [plan](remote-model-provider-tls-builtin.md)): `coddy tls ensure` run at once by the installer, the update, the service start and an operator, with a crash at every step; a valid pair is never replaced by an invalid one, no leaf is left signed by a CA that is gone, the CA key changes only by `--force-ca`, a second run does nothing | `p7-pki-plan` | Promela, **planned** (stage T6) |
| **The credentials that remain after the certificate identity is removed** (main token, shared token, cookie, nothing, the handshake admission by CA; no input makes a certificate change an authorization decision) | `p7-gate` | Promela, **planned** (stage T6); it supersedes `p5-gate` and `p3-d2-mtls-identity` for the code after stage T1 |

Not modelled, and why: the certificate verification rules (Go's `crypto/x509`: extended key usage, names, anchors) and the configuration checks are decided by a library and by tables, and are held by the end-to-end tests on self-signed certificates (`docs/operate/certificates.md`, section 11 there) rather than by a model.

## 3. Order and bookkeeping

`p5-registry`, `p5-lease-net`, `p5-gate`, `p5-certloader`, `p5-pinger`, `p5-relay-limits-net`, `p5-probe-slot-net`, in that order, a commit and a short entry in the table above (verdict and report) per model. A finding that reaches the code is fixed test-first in the same step.
