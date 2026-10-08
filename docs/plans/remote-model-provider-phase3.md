# Plan: shared models of a remote Coddy, phase 3 (hardening of the relay path)

**Status: proposed; cross-reviewed 2026-10-08 (two reviewers, eight corrections applied in this text); D1 to D5 still open, the model-check engine was not available.** Design record, written before the code. Branch `feat/remote-model-provider`, phases 1 and 2 committed (`git log upstream/main..HEAD`). This plan is the third step of `docs/plans/remote-model-provider.md` (4.5, 4.5a, 5 phase 3, 6a item 4, 6b) and follows the format of `remote-model-provider-phase2.md`.

How to read it. Five dilemmas (section 10) are meant for the model-check engine **before** the stages that depend on them start (reports `p3-d1` to `p3-d5` in `docs/plans/remote-model-provider-models/`); every other point is a decision of this text. Every code reference was read in the worktree `coddy-agent-wt-remote-model` on 2026-10-08 at HEAD `5fb6801a` (phases 1 and 2, with their review fixes, committed; the tree was clean): a stage re-finds a line by the name given beside it. Statements about the Go standard library were checked against `$GOROOT` 1.26.0 (the two that matter are named in 4.1); one behaviour of `http.ServeMux` was run (`HEAD` matches a `GET` pattern; `%63ompletions` reaches `/coddy/llm/completions`; a trailing slash and `;x=1` do not; `//x` answers a 307); kernel and third-party statements are marked **[unverified]**.

## 1. Goal and non-goals

**Goal.** Six things the earlier phases left open on purpose, all on the relay path of a shared model and the node behind it:

- **A. Client scopes at the relay.** A relay client token can be limited to the three shared-model routes (`GET /coddy/llm/models`, `GET /coddy/llm/models/{alias}/usage`, `POST /coddy/llm/completions`) of chosen nodes, with a node allowlist per token. `swarm.auth_token` stays the full class and behaves byte for byte as today.
- **B. mTLS.** A verified client certificate can stand for, or be bound to, a scoped relay token; the local Coddy's `coddy` provider can present a client certificate and trust a private CA; a relay and the nodes that join it can dial each other with one. The direct listener gets mTLS only through a TLS block it does not have today (optional stage H7).
- **C. Rate limits.** A calls-per-window limit beside the five-streams slot, on the node (per credential) and on the relay (per client token, which is where the one-caller problem of 4.5 is solved).
- **D. Audit counters.** Counted outcomes per alias and credential class on the node, per client label and node on the relay: never content, never a token.
- **E. A node label** that silences the relay's `<node>: 401 Unauthorized` sessions warning for a node that joined with a shared-model token, with a badge in the swarm view instead.
- **F. The relay erasing a stale token** (decided by quorum, 6a item 4): the owner's registration replaces the token with the one it carries, an empty one included.

**Non-goals** (phase 4, later, or declined here):

- Relay discovery and multi-node failover (phase 4).
- A scope for sessions, a read-only scope or a per-alias allowlist: the relay does not parse bodies, an alias scope would be node-side.
- Named full-access entries (`scope: full`).
- Trusting a client-certificate header forwarded by a TLS terminator (spoofable): a terminator that must authenticate clients does it itself, and the node then sees only the bearer.
- CRL or OCSP: Go checks neither, revocation is the removal of an entry or a name (4.2).
- mTLS from a relay to a node that registered itself: the relay has dial settings only for `swarm.upstreams` (`registry.go:211-215`), and a node cannot ask for them.
- Limits on the listing and usage reads (the usage route is coalesced by the manager, phase 2 4.1), a Prometheus text endpoint, persistence of counters and buckets across a restart.
- The application probe O4 of phase 2 7.6 (section 9).
- A flag or an environment variable for scoped tokens (they are `${ENV}` references in the file).
- A `protocol` bump: everything is additive on protocol 1 (a refusal with a new `code` is still `kind: busy`, 5.3).

## 2. Where the earlier plans are wrong or silent (verified against the code)

| # | Statement | What the code says | Consequence |
|---|---|---|---|
| W1 | 4.5: "per-client scopes enforced by the relay's path gate (`mountAllows`)" | `mountAllows(method, rest)` (`external/swarm/mount.go:405-420`) knows no caller. The relay's `authGate` (`server.go:249-277`) is class-blind (`acceptToken(tokens, credentialOf(r))`), and the relay's **own** routes sit behind the same gate: `GET /swarm/nodes` (it lists every node's advertised `url`), `GET /swarm/sessions`, `GET /swarm/topology`, `DELETE /swarm/nodes/{node}` and the settings routes `/coddy/config*` (`settings.go:19-40`). A client token is a fleet-wide door (`docs/operate/swarm.md:416`), wider than "the mounts of every node" (4.5a) | The class is a principal the gate puts in the request context; a scoped principal passes the gate on the mount pattern only and the mount makes the fine check (3.2). `mountAllows` stays for the full class |
| W2 | (not seen) | `workspaceMediaCapability` (`server.go:324-343`) lets a GET or HEAD of a workspace raw route through the gate on a query `access_token` that "is not one of this relay's client tokens" (`:342`, `acceptToken(s.clientTokens(), token)`), and the mount keeps such a query token for the node (`mount.go:337-339`). A scoped token is not in `clientTokens()`: it would be read as a media capability, pass with no principal and be written into the node's query | The exclusion (`:342`) and `mediaCapabilityOnly` (`:347`) must cover every client token, full or scoped (3.2) |
| W3 | 4.5 sees one relay | In a chain the parent presents the child relay's own client token, and a child that set no `swarm.join[].token` falls back to its **full** `swarm.auth_token` (`internal/swarm/joinset.go:72-74`). Only the first relay's gate sees the borrower's class; the child authenticates the parent as full | The first relay judges the whole remainder of the path, every hop (the walk of `isSharedCompletions`, `mount.go:101-109`); D1 decides what the allowlist means across hops |
| W4 | 5, 7: "`golang.org/x/time/rate` for limits", "already a candidate, check go.mod" | The module is in neither `go.mod` nor `go.sum`. `go list -m all` names v0.5.0 only as a graph member of a dependency, and the local module cache holds v0.15.0 from other projects; this build requires neither (`go list -deps` over every tag finds no package of it). Adding it means `go get`, a `go.sum` change and a `make security` pass for about forty lines | An in-tree GCRA limiter with an injectable clock (5.2), no new module |
| W5 | 4.5, 5: "an mTLS option on the direct listener" | The direct listener has no TLS: `external/httpserver/serve_http.go:134-138` is a plain `srv.ListenAndServe()` and no `httpserver.tls` key exists (TLS is a reverse proxy's job there). The only TLS keys are `swarm.tls` (`internal/config/swarm.go:86-94`, `serve_swarm.go:85-87`). A `ProviderConfig` has no TLS key at all (`proxy` only), so a `coddy` row cannot trust a private CA or present a certificate today | mTLS starts at the relay listener, the provider row and the dial settings; the direct listener is the optional stage H7 behind a new `httpserver.tls` block (4.4) |
| W6 | 4.2 lists the kinds `busy` and `rate` | `kind: rate` is the **upstream's** 429 after the remote's own retries (`shared_errors.go:63-71`), and it is terminal for the client: `admit` waits only for `busy` (`coddy.go:338-340`) and `coddyNeverRetried` covers every other kind (`coddy_errors.go:198-202`). `writeSharedError` writes `Retry-After: 1` for every busy (`shared_errors.go:119-125`) | A window refusal as `rate` would read "the remote's provider is rate limiting this model" and never wait; it is `kind: busy` with a `code` (5.3, D3), and the header is computed |
| W7 | 4.5a, 6a item 4: "a registration or renewal by the lease's owner replaces the token"; "compatibility with nodes of older versions to be checked first" | Checked: `Token: c.opts.NodeToken` is in the first version of the join client (`git log -S NodeToken -- internal/swarm/join.go` gives `0dcce79b`, 2026-09-08) and `Register` (`join.go:159-229`) sends it with every registration (`omitempty` drops an empty one). A **tunnel** node has no renewal: `Run` registers once and then blocks in `serveTunnel` (`join.go:282-289`), so for it the replacement acts at the next (re)connect (the m4 report 3.1 says the same) | The rule of section 8: unconditional replacement by the owner, no migration code |
| W8 | 4.2: "the relay's own downstream probe is hardening (phase 3)" | Phase 2 delivered it (`mount.go:230`, `probeSharedCall`) | Not a phase-3 item; what is left is the HTTP/2 residual, contained by A and C (section 9) |
| W9 | 4.5a: the sessions warning "can be silenced with a node label" | `Labels` are free-form, validated nowhere and **replaced on every renewal** (`registry.go:228`); `TopologyNode` carries none (`topology.go:17-26`); the aggregation asks every online node (`sessions.go:166`, `askNode` `:278`) and turns any non-200 into a warning (`:334-336`) | The label is a node claim used for one purpose (the fan-out skip) and derived from the token the node actually sends, so it cannot drift (section 7) |

## 3. Area A: client scopes at the relay

### 3.1 Current state

- One credential class. `Server.clientTokens()` (`server.go:279-292`) returns `swarm.auth_token` plus the extra tokens of `--swarm-auth-token` and `CODDY_SWARM_TOKEN`; `authGate` admits any of them on every route but the public ones (`GET /swarm/info`, `/`, `POST /swarm/register` with a pairing token, `POST /swarm/tunnel` with a lease secret, and the media capability, `:240-265`). A relay without a token generates one on loopback and refuses to bind elsewhere (`serve_swarm.go:44-60`).
- The mount (`mount.go:162-277`) validates the node name, takes the escaped remainder (`mountRemainder`: refuses `%2f`, `%5c`, `.` and `..` segments), bounds the hops (`swarmMaxHops` = 4), applies `mountAllows`, looks the node up, refuses a settings write aimed at a child relay, then replaces `Authorization` with the node's token (`rewriteFor`, `:359-362`). `isSharedCompletions` (`:94-111`) already walks the hops of a chained path on the **decoded** remainder (the phase-2 review fix) to find the one shared route.
- Config. `SwarmConfig` (`internal/config/swarm.go:29-83`) has `auth_token`, `pairing_tokens`, `tls`, `upstreams`, `join`. The relay's form is `RelayUISchemaMap` (`ui_schema_relay.go`), its document is scoped to `swarm` and `logger` (`settings.go:30-40`), credentials are write-only and kept across a save by `preserveSwarmSecrets` (`jsondto.go:1076`). The agent's own Settings form hides `swarm` and `httpserver` (`ui_schema.go:839-841`), so these keys are edited in the file or on the relay's own form.
- The class rule of phase 1: `CheckSharedTokenClasses` (`shared_models_auth.go:88-128`) compares shared-model tokens with the main, swarm and pairing tokens (file, flags, environment). It runs at load (`config.go:159`), in `coddy -t` (`check_shared.go:32`), at start-up and at every install (`cmd/coddy/serve.go:186,726-731`, `httpserver/auth.go:112-119`).

### 3.2 Design

**Principal.** `principal{class: none|full|scoped, entry}` is resolved once per request by `(*Server).principalOf(r)`, stored in the context by `authGate` and read by the mount, the limits (5) and the counters (6).

- Resolution, every entry compared without an early exit. An entry is **eligible** only when the request shows everything the entry requires: a token-only entry needs its bearer; a certificate-only entry needs a verified certificate whose names map to it (4.2); an entry with both needs the bearer **and** a certificate mapped to **that same** entry (a bearer of one entry with a certificate of another is no principal). Among eligible entries the principal is the scoped one; a request with no eligible scoped entry falls to (3) the bearer against the full tokens. A certificate or a bearer alone never satisfies an entry that requires both.
- `cert_names` must not overlap between entries (`Validate` refuses a name listed under two entries), so one SAN never maps to two scopes.
- A bearer that matches both a scoped entry and a full token is **scoped** (least privilege, as phase 1 reads a duplicate as shared); load and install refuse the duplicate anyway.
- A certificate never yields the full class.

**Config** (all optional; no `clients` key means today's behaviour, byte for byte):

```yaml
swarm:
  auth_token: "${RELAY_CLIENT_TOKEN}"        # full class, as before
  clients:
    - name: acme                             # label of logs and counters; unique; [a-z0-9][a-z0-9_-]{0,31}; "full" and "unknown" are reserved
      token: "${ACME_RELAY_TOKEN}"           # write-only; a token belongs to one class only
      scope: shared_models                   # the only scope of this phase; required; an unknown value is an error naming the key
      nodes: [workstation, edge/gpu-box]     # hop paths this client may reach (D1); required, non-empty; "*" = any node directly below this relay
      max_streams: 2                         # relay-side limits, section 5; 0 or absent = none
      rate_per_minute: 30
      rate_burst: 10
      cert_names: []                         # section 4: SAN names of client certificates that map to this entry
```

- `internal/config/swarm_clients.go` (new, untagged): `SwarmClient`, `Normalize` (trim; names are case-sensitive like node names) and `Validate`.
- `nodes` segments follow the node-name alphabet `[A-Za-z0-9_-]{1,64}` (a local copy of `internal/swarm/names.go:21`, which `internal/config` cannot import; a test in `internal/swarm` holds the two equal), at most `SwarmMaxHops` segments, `*` only as a whole entry.
- `config.ValidSharedAlias(s)` is exported from `shared_models.go` (the relay needs the alias alphabet for the usage route).
- The borrower's side does not change: the scoped token is the `api_key` of its `coddy` row and `api_base` stays `https://relay/swarm/nodes/<node>`, or `https://relay/swarm/nodes/<edge>/swarm/nodes/<node>` through a chain, which is the path D1 judges.

**The gate.** `authGate` keeps its public patterns and the media capability.

- A request with no principal gets the plain `401` (`writeUnauthorized`).
- A **scoped** principal passes only on `pattern == "/swarm/nodes/{node}/{rest...}"`. On every other route (`/swarm/nodes`, `/swarm/sessions`, `/swarm/topology`, `DELETE /swarm/nodes/{node}`, `/coddy/config*`) it gets the same plain `401` an unknown token gets, so it learns nothing about the relay.
- A relay with no full token and no scoped entry is open exactly as before. The generated-token rule of `serve_swarm.go:47-60` is untouched: scoped entries are additive, and `coddy -t` warns when `clients` is set and no full token exists (the relay's map and settings would be reachable only with a token generated per run).
- `clientTokens()` stays the full list; a new `allClientTokens()` (full plus scoped) feeds `workspaceMediaCapability` and `mediaCapabilityOnly` (W2).

**The mount.** After `mountRemainder` and the hop bound, for a scoped principal: `hops, route := splitHops(name, decoded rest)` (the loop of `isSharedCompletions`, refactored into one function both use). Then, in this order and **before** `registry.Node(name)` so the answers cannot be told from an unknown node:

1. `entry.admits(hops)` (D1), else `404` hop error "no such node in this relay", the answer of an unknown node (`mount.go:209`).
2. `sharedRoute(method, route)`, else `404 route %q is not carried by a node mount` (the text of `mountAllows`).

`sharedRoute` is a closed table on the **decoded** route, an exact match (D5): `GET /coddy/llm/models`; `GET /coddy/llm/models/<alias>/usage` with `config.ValidSharedAlias(alias)`; `POST /coddy/llm/completions`; nothing else, `HEAD` and `OPTIONS` included (`HEAD` matches a `GET` pattern on the node). The comparison is on the decoded remainder because that is how the node routes (`%63ompletions` is the completions route there); `completions/`, `//coddy/...` and `;x=1` fail it. A prefix rule on `/coddy/llm/` is deliberately not used (D5). Everything else the mount does is unchanged for both classes: the node's token replaces `Authorization`, the query token is stripped, the phase-2 probe runs for `isSharedCompletions`.

**Chained relays.** The scoped check walks every hop of the path (W3), so a child relay's full token is never reachable through a scoped request: after the walk the route is one of the three, whatever relay forwards it. D1 decides how the allowlist reads the hops.

**Answers.** On the three routes a scoped client sees exactly what a full client sees (the node's answer, relayed). Its own refusals: `401` outside the mount; the `404` unknown-node look-alike outside the allowlist; `404` route not carried inside the allowlist; the limit refusals of 5.3.

**Class rule.** `CheckSharedTokenClasses` is generalised in place, so its six call sites change nowhere.

- It also builds the owner map from `swarm.clients[i].token`, returns early only when there is neither a shared-model token nor a scoped entry, and refuses a scoped token that equals a main, full-swarm, pairing, shared-model or other scoped token.
- Its error gains a `Where` field so `Path()` names `swarm.clients[i].token`; an empty `Where` keeps today's `httpserver.shared_models.tokens[i]` text.
- `swarm.join[].token` and `swarm.upstreams[].token` stay outside the classes (phase 1 decision).

### 3.3 Wire, config and API changes

- Config: `swarm.clients[]` (above); `config.schema.json` gets `swarm.clients` (`additionalProperties: false`, descriptions are the `doc:` lines of `coddy -t` and the table of `docs/reference/config.md`).
- Settings: `SwarmJSON.Clients []SwarmClientJSON` (`token` write-only with `token_configured`, `nodes`, `cert_names`, limits), cloned by `ConfigToJSONDTO` and `JSONDTOToConfig`; `preserveSwarmSecrets` keeps a token across a save **by `name`** (renaming an entry asks for its token again, said in the field's description); `swarmUISchema()` gets a `clients` array of objects like `join` (the form is schema-driven, no SPA code); `uiHiddenConfigKeys` is unchanged; `UISchemaCoversConfigJSONFields` and `jsondto_schema_test.go` stay green.
- HTTP: no new route and no changed answer for the full class. A scoped principal sees the refusals above.
- `coddy -t`. Errors: a missing, duplicate or reserved `name`; an unknown `scope`; empty or malformed `nodes`; a negative limit, or `rate_burst` below 1 while a rate is set; a token of two classes. Warnings: an entry with neither a token nor `cert_names` (the entry is ignored, so no client can use it: fail closed; a certificate-only entry is valid); `rate_burst` without `rate_per_minute`; `clients` without a full token; `cert_names` without `swarm.tls.client_ca_file`.

### 3.4 Edge cases

- A scoped token on a node that joined with its **main** token still reaches only the three routes: the relay is the boundary, which is the point of the class. On a node that joined with a shared-model token the node's own gate is a second line.
- A node name is not an identity. A pairing-token holder can claim a name once its lease expired (ttl + grace, 180 s) and the legitimate node is away, and a scoped client's whole conversation then goes to that node. The legitimate node re-claims at once while its lease secret is stored (`NewFileSecretStore`); `swarm.upstreams` entries are pinned (`Pin`, never expire) and cannot be claimed. The documentation tells operators to allowlist pinned or watched nodes for clients that send sensitive work (section 12).
- `GET /swarm/nodes` is closed to scoped clients, so a client cannot enumerate nodes; phase 4 may add a filtered listing. `DELETE /swarm/nodes/{node}` stays full-only.
- A settings save or reload rebuilds the relay (`Server.Close` ends its tunnels, `server.go:118`), so new entries apply at once and streams in flight are cut and resumed by the client, as for any settings save.
- The query-token exception of `credentialOf` (`server.go:360-377`) stays: a scoped token in a query on an event route resolves to a scoped principal and the route table refuses it.

### 3.5 Tests

- Unit (`-tags swarm ./external/swarm`): a table of principals (no `clients`: full only, and a golden test of the `authGate` and `handleMount` answers with and without the new code; scoped; duplicate; certificate; both).
- The scope table over method x route x spelling (plain, `%63ompletions`, trailing slash, `//`, `;x=1`, `HEAD`, `OPTIONS`, `%2e%2e`, `%2f`, a second hop, five hops) against an exhaustive expected set, plus a randomised walk asserting that no admitted request is not one of the three routes.
- The unknown-node look-alike is byte-identical to the real unknown-node answer; every relay-own route answers a scoped token the plain 401 and a full token as before.
- The media-capability path with a scoped token as `access_token` (W2: refused, never forwarded in the node's query); a scoped token never reaches the node's `Authorization`; a chain whose child presents its full token upstream cannot be driven by a scoped client outside the three routes.
- Config (`./internal/config`): validation table; class-rule table (every pair of classes, file and out-of-band); JSON and YAML round trip; write-only and `preserveSwarmSecrets` by name; schema acceptance; `coddy -t` findings with `file:line:col`.
- Feature `features/swarm_client_scopes.feature` (happy paths only: a scoped client lists and calls a shared model through a relay that dials the node and through a tunnel, is refused on `GET /swarm/nodes` and on a node outside its list), steps in `external/swarm/bdd_scopes_test.go`; the relay-settings feature gains a scenario that saves a client entry and reads back `token_configured`.

## 4. Area B: mTLS

### 4.1 What Go offers (verified in `$GOROOT/src/crypto/tls/common.go`)

- A server sets `Config.ClientAuth` (`VerifyClientCertIfGiven` and `RequireAndVerifyClientCert` are the verifying modes) and `ClientCAs`; `http.Request.TLS.VerifiedChains` carries the verified chain on HTTP/1.1 and on every HTTP/2 stream.
- `VerifyPeerCertificate` is **not invoked on resumed connections** (`common.go:660-663`); `VerifyConnection` runs for every connection, resumptions included (`:668-677`). The identity is therefore read from `r.TLS` at each request and never cached at handshake.
- Go checks neither CRL nor OCSP, and a leaf's `NotAfter` only at handshake, so a connection kept alive past it needs a per-request check.

### 4.2 The relay listener (H2a, with the keys in H1)

`SwarmTLSConfig` gains `client_ca_file` and `client_auth` (`optional` or `required`; with a CA and no value, `optional`; without a CA, none; both need `cert_file` and `key_file`).

- `serve_swarm.go` builds `server.TLSConfig` (`MinVersion` TLS 1.2, `ClientCAs`, `ClientAuth` = `VerifyClientCertIfGiven` or `RequireAndVerifyClientCert`) before `ListenAndServeTLS`, which keeps negotiating h2; `httpx.NewServer` keeps the connection in the context and `httpx.TCPConn` already unwraps a `*tls.Conn`.
- `optional` suits a relay that browsers and nodes also use. `required` rejects every peer without a valid certificate at the handshake: a dedicated share-only relay, the deployment the documentation already recommends; nodes then need `dial.cert_file` and `dial.key_file` to register and open tunnels (below), and a browser needs a certificate installed to load the SPA the relay serves.

**Mapping.** `swarm.clients[].cert_names` lists names matched exactly against the leaf's DNS and URI SANs (the CN is not consulted).

- The match runs **per request** on the live configuration: removing a name takes effect on the next request of an open connection, and a request whose leaf is past `NotAfter` has no certificate identity.
- A chain valid for `client_ca_file` whose names match no entry yields no identity; the request then needs a bearer or is `401`.
- **Entry semantics (D2):** an entry with only `token` is a bearer entry, with only `cert_names` a certificate entry, with both **both are required and must name the same entry** (the token is useless without the key). A certificate never opens the full class.

The budget of 4.5 is keyed by the matched SAN name, not by an individual certificate.

**Interplay with the credential replacement.** The relay never forwards the client's certificate, its names or its entry to the node: the node sees the node's token, as today, so per-borrower identity exists on the relay only (counters, limits). A node that is itself behind TLS (H7, or a reverse proxy) sees the relay as one client whatever certificate the borrower used.

**Dial certificates.** `SwarmDialConfig` gains `cert_file` and `key_file`, both or neither: the certificate this end presents to the other (a node joining a `required` relay, a relay dialling a pinned upstream that asks for one).

- `netx.Options` gains the two fields and sets `tls.Config.GetClientCertificate` to a loader that reads the pair at handshake and caches it by size and modification time, so a rotated certificate is used by the next connection without a restart.
- They are copied where the other dial fields are: `seedUpstreams` (`server.go:149-153`, H2a), `StartJoins` (`joinset.go:85-89`, H1 serialised before H3) and the tunnel dial (which takes `netx.Options` directly, `join.go:64`, `tunnel.go:78`, so it needs no edit of its own). H2a's required-relay join tests depend on that H1 line. `registry.go:211-215` gives a self-registered node no dial settings, which is why relay-to-node mTLS exists for pinned upstreams only.

### 4.3 The local Coddy's `coddy` provider (H5)

`ProviderConfig` gains `ca_file`, `client_cert_file`, `client_key_file`, accepted for type `coddy` only (any other type is a load error naming the key, so no one believes a certificate is presented where it is not); the pair is both or neither.

- `coddy -t` reads the files (an unreadable or mismatched pair is an error, a CA holding no certificate a warning); `--dry-run` loads the pair, reports expiry within 14 days as a warning and a past `NotAfter` as an error, and runs its listing probe with the identity.
- One new value, `netx.ClientTLS{CAFile, CertFile, KeyFile}`, rides `config.ResolvedLLM` (`resolve.go:205`, next to `ProxyURL`) and `llm.ProviderInput`, and is set at the seven sites that copy `ProxyURL`: `internal/agent/react.go:2744`, `external/httpserver/server.go:324,359`, `external/httpserver/providers_models_http.go:148`, `internal/dryrun/providers.go:71`, `internal/session/provider_usage_sources.go:70`, `internal/session/context_window.go:349`.
- The agent's Settings form shows the three keys for a `coddy` provider only: `SettingsSection.tsx:296` already hides `busy_wait_ms` for any other type, and the same rule is added (H5 owns that file and its test).

**The proxy rule holds by construction.** The three requests of a `coddy` row (completions `provider.go:297`, listing `coddy_models.go:35`, usage `coddy_usage.go:70`) are built by the one constructor `providerTransportFor`, which gains the identity as a third key part: `http/1.1|tls:<digest of the three paths with sizes and mtimes>|<proxy key>` (phase 2's HTTP/1.1-only transport, `transport.go:253-296`).

- `HTTPClientForProviderProxy(setting)` and every other provider type call it with an empty identity, so their keys and behaviour are what they were; `TestProviderProxyGuard` and `TestCoddyRowsHonourTheirProxy` stay the guard, the latter extended (a coddy row with a certificate behind `proxy: none` and behind an explicit proxy presents it to a TLS stand and shows the proxy's CONNECT).
- A certificate rotated on disk changes the key part and builds a new transport.
- An `https://` proxy receives the same `TLSClientConfig` for its own hop (`net/http/transport.go:1819-1825`), so the certificate is offered to such a proxy as well if it asks; the documentation says so.

### 4.4 The direct listener (optional stage H7)

- `httpserver` gains `tls: {cert_file, key_file, client_ca_file, client_auth}` (the rules of the relay's) and `httpserver.shared_models.cert_names`: names admitted on the three LLM routes as the class `mtls`, whose limiter key is a digest of the matched name, so each certificate has its own stream and rate budget **without a relay**.
- `serve_http.go:134-138` builds the TLS listener; `authGate` (`auth.go:172-226`) reads `r.TLS` per request, and a certificate identity passes `isSharedLLMPattern` only, never another route.
- Tunnel requests carry no certificate (the node sees the relay), and a node behind a TLS terminator sees none either: the documentation says "require the certificate at the terminator".
- H7 is independent of everything else. If it is dropped, A, C, D, E, F and the provider side stand alone.

### 4.5 Tests

- `internal/netx`: the loader (rotation by mtime; a missing file fails that handshake only).
- `external/swarm` (`-tags swarm`, real TLS on loopback with a generated CA): `optional` admits no-certificate peers to the public routes and nothing else; `required` rejects them at the handshake; a mapped certificate is a scoped principal on h1 and on h2; an entry with both fields needs both and refuses a token with another entry's certificate; a name removed from the live configuration refuses the **next request on the open connection**; a certificate past `NotAfter` on a kept-alive connection has no identity; a resumed connection (TLS 1.3 resumption forced) is judged by its request; the full class is unreachable by certificate; the node never sees the certificate (its stand records headers and remote state).
- A node joining a `required` relay by direct and by tunnel with `dial.cert_file`.
- `internal/llm` (`-run Coddy`): the ALPN stays `http/1.1`; a client certificate is presented; a private CA is trusted; the proxy tests above; key stability and change.

### 4.6 Wire, config and API changes, edge cases

- Config: `swarm.tls.{client_ca_file, client_auth}`, `swarm.{join,upstreams}[].dial.{cert_file, key_file}`, `swarm.clients[].cert_names`, `providers[].{ca_file, client_cert_file, client_key_file}` (type `coddy`), and for H7 `httpserver.tls.*` and `httpserver.shared_models.cert_names`; `config.schema.json`, the relay form (`swarmDialUISchema`, the `tls` object) and `SwarmJSON` follow. Keys are paths, not secrets (the private key stays in its file); the settings document serves them as plain strings. No new HTTP route, flag or environment variable.
- A relay whose `client_ca_file` is set but unreadable refuses to start with the key named; a `required` relay with no node holding a certificate registers none, and `net/http` logs each failed handshake as `http: TLS handshake error` through its default logger (`httpx.NewServer` sets no `ErrorLog`).
- The relay's `h2ResidualLog` and the phase-2 probe are untouched: `r.TLS` does not change what an HTTP/2 client gets.
- A client certificate with no bearer on a bearer-only entry, or the reverse, is the plain `401`; the answer never says which half was missing.

## 5. Area C: rate limits

### 5.1 Current state

- The node limits concurrent calls per credential. `sharedLimiter` (`shared_limiter.go:31-79`) is keyed by a digest of the bearer (`sharedKeyFor`, `:19`; cookie sessions and `anonymous` otherwise, `sharedCallerKey`, `shared_models_http.go:183-201`), taken right after authentication and before the body (`:259-268`), and refused as `429 kind: busy` with `Retry-After: 1`.
- The client waits out `busy` inside `provider.Stream` under `busy_wait_ms` (default 30000, `coddy.go:306-365`): the sleep is the larger of `Retry-After` and a 1 to 5 s backoff plus up to 20 % jitter upward (`busySleep`, `coddy_busy.go:201-217`); a `busy` that outlasts the budget is the spent-wait error.
- Through a relay the node sees one credential, so its limit counts every client of the relay together (4.5), and the relay has no limit of its own.

### 5.2 The limiter (`internal/shareguard`, new, untagged, H1r)

`Limiter.Take(key string, perMinute, burst int) (ok bool, retryAfter time.Duration)` is a GCRA: emission interval `T = 1 min / perMinute`, tolerance `(burst-1)*T`, one `time.Time` per key (the theoretical arrival time), the clock injected (the node passes `sharedClock.Now`, so its fake clock drives it). Parameters are read at each call from the caller's configuration, so a reload applies to the next call; the stored arrival time is clamped on every `Take` to at most `now + tolerance + T` under the **current** parameters, so a reload from 1 to 60 calls per minute does not keep refusing for up to a minute on state written under the old rate; `Prune(idle)` drops keys idle for longer than a bucket needs to refill and runs from a ticker the caller owns. No new module (W4).

### 5.3 Design

**Node** (`httpserver.shared_models.rate_per_minute`, `rate_burst`; absent or 0 = off: there is **no default limit**, because a default would change every deployment). `rate_burst` defaults to `min(rate_per_minute, max_streams)` and at least 1.

- The window counts **calls that were admitted**: in `llmCompletionsPost` the take comes **after** the slot and **before** the body is read. `slot := acquire(...)` first (a busy refusal spends no token), then `Take`; a refused take releases the slot and answers at once, without reading up to 32 MiB.
- A client waiting out `busy` at one request a second therefore spends no tokens on the node (D3); the relay refunds its own token on a node `busy` (below).

**Relay** (`swarm.clients[].max_streams`, `rate_per_minute`, `rate_burst`, keyed by the entry name): applied in `handleMount` to `isSharedCompletions` requests of a scoped principal, after the scope check and the node lookup and online check (a refusal for an unreachable node costs nothing) and before the probe: first a slot (`limits.go`, a mutex-guarded count by name, released by a deferred idempotent release exactly like `sharedSlot`), then `Take`.
- The relay cannot know before forwarding whether the node will admit the call, so its window counts **forwarded attempts**, with one correction: when the node answers a pre-stream 429 `busy` (seen in the proxied response status), the relay **refunds** the token (`Limiter.Refund`, which moves the stored arrival time back by one emission interval, never past `now`). A client waiting out the node's `busy` at one request a second therefore does not drain its relay bucket either; the claim "waiting spends no tokens" holds on both layers.

- This answers the one-caller problem: the relay is the only place that knows the client, so per-borrower fairness lives there, while the node's per-credential limit stays what protects the provider from the relay's clients **together**.
- The relay clients' limits may add up to more than the node's `max_streams`; the node's `busy` then appears and is waited out like any other (documented, not prevented).

**Answers** (always before a stream starts, as JSON with `Cache-Control: no-store`):

| Refusal | By | Status | `kind` | `code` | `Retry-After` |
|---|---|---|---|---|---|
| no free slot | node (today) | 429 | `busy` | none | `1` |
| window exhausted | node | 429 | `busy` | `rate_window` | ceil of the wait to the next token, at least 1 |
| the client's relay slots | relay | 429 | `busy` | `client_streams` | `1` |
| the client's relay window | relay | 429 | `busy` | `client_rate` | ceil of the wait, at least 1 |
| the upstream's own 429 | node, in an error frame | stream | `rate` | none | `retry_after_s` |

- The relay writes the same flat `WireError` JSON as the node without importing `internal/llm`; a test-only import pins the key set by reflection (`mount_liveness_test.go:18` already imports it).
- `writeSharedError` sets the header from `RetryAfterS` when it is above zero, else `1` (W6).
- **Why `busy` and not `rate` (D3):** the client already waits out any `kind: busy` under one budget (`coddy.go:338-364`) and ignores the `code`, so phase 1 and 2 clients wait out the new refusals unchanged, while a `rate` answer is terminal for them and means the upstream's 429. The client change is cosmetic: when the budget ends on a window refusal, `coddyBusyError` names the window instead of "no free stream slot" (H5, optional).

### 5.4 Wire, config and API changes

- Config: `httpserver.shared_models.{rate_per_minute, rate_burst}` with `(*HTTPServerConfig).EffectiveSharedRate()`; `SharedModelsJSON` and `config.schema.json` follow; `swarm.clients[].{max_streams, rate_per_minute, rate_burst}`; `coddy -t` errors for negative values and `rate_burst` below 1 while a rate is set, a warning for `rate_burst` without a rate.
- Wire: the two new `code` values on the node (`rate_window`) and the two on the relay (`client_streams`, `client_rate`), all `kind: busy`; `Retry-After` computed. OpenAPI (`openapi_shared_models.go`) and `docs/reference/http-api.md` list the codes.

### 5.5 Edge cases

- Buckets are in memory: a restart or a rebuild of the relay (every settings save) refills them. Keys exist only after authentication, so their number is bounded by the credentials and sessions that passed the gate, and `Prune` bounds them in time. A reload that lowers `rate_per_minute` applies to the next call.
- No fairness between waiting clients of one key is promised: under contention one waiter can lose every race inside its budget (D3, A6) and ends with the spent-wait error.
- The usage and listing routes take no token and no slot. Phase 2's liveness is unchanged: a vanished h1 client frees its relay slot within B; a vanished h2 client holds its relay slot until the call ends (section 9).

### 5.6 Tests

- `internal/shareguard`: a GCRA table (burst, refill, exact `retryAfter`, a parameter change between calls (rate raised and lowered, the clamp), a refund, concurrent takers never exceed `burst + rate*window` under `-race`, `Prune`).
- Node (`-tags http`): refusals at the right call; a `busy` refusal spends no token; a refused take releases the slot (`sharedLimit.inUse` is 0); the body is not read on a window refusal; the header and `retry_after_s` agree; the answer parses in `classifyCoddyAnswer` as `busy` with the code ignored; the existing busy tests stay untouched.
- Relay: a scoped client held to `max_streams` while another client of the same relay is not; the `WireError` key set; refusals cost the node nothing (its stand sees no request); both limits together (a table over both `max_streams`, the node's `busy` still waited out); a client looping on a node `busy` does not drain its relay bucket (refund); release on every exit (final, error, client gone, h1 probe abort).
- Client (`-run Coddy`): a window refusal is waited out within `busy_wait_ms`; a `Retry-After` beyond the remaining budget ends as the spent wait with the window named.

## 6. Area D: audit counters

**Current state.** The repository has no metrics layer: no `expvar`, Prometheus, OpenTelemetry or `/metrics` route in any `.go`, `.md` or `.yaml` file (searched 2026-10-08; the one `/metrics` hit is a test asserting that a mount refuses it, `swarm_test.go:464`). Operations are read from `slog` lines, and read-only state is served by authenticated `/coddy/*` JSON routes. The one audit record that exists is the per-call log line (`logSharedCall`, `shared_models_http.go:376-394`: alias, kind, status, duration, request id).

**Smallest sound design.** In-memory counters next to the limiter, the existing log line extended, one read-only route per process, no exporter. `shareguard.Counters` is a `map[[3]string]*row` under a mutex with `Add(key, Delta{Calls, InputTokens, OutputTokens, DurationMS})` and a sorted `Snapshot()`; the callers choose the labels, so cardinality is the caller's contract.

- **Node** (`shared_audit.go`): key `{alias, class, outcome}`.
  - `alias` is the row's alias, or `-` when it is unknown or not yet decoded (a probe cannot create a key).
  - `class` is the credential class, never the credential: `main`, `shared` (any shared-model token), `login`, `anonymous`, and `unknown` for a bearer the gate refused on a shared route.
  - `outcome` is one of `ok`, `busy`, `limited` (the window of 5.3), `rate` (the upstream's 429), `quota`, `upstream`, `invalid`, `auth`, `gone` (the log's `client_gone`), `write` (`write_failed`): the existing `call.kind` values plus `limited`.
  - Values: calls, input and output tokens (from `resp.InputTokens` and `resp.OutputTokens`; a failed call counts what the partial response carried, else 0), duration sum and maximum in ms.
  - Exposure: `GET /coddy/shared-models/stats` answers `{since, rows[]}`. It lives **outside** the `/coddy/llm/` namespace on purpose, so no prefix rule can ever admit it for a scoped client (D5), and it is not in `isSharedLLMPattern`, so a shared-model token gets the gate's 401 while a main token or a signed-in browser reads it. OpenAPI and `docs/reference/http-api.md` describe it. The log line gains `class`, `input_tokens` and `output_tokens`.
- **Relay** (`audit.go`, `stats.go`): key `{client, node, outcome}`.
  - `client` is the entry's `name`, `full` for the full class (one token for many people cannot be told apart), `unknown` for a refused token.
  - `node` is the first hop's name when it is in the registry or the principal's allowlist, else `-`.
  - `outcome` is `ok` (the node's answer started), `scope` (refused by the allowlist or the route table), `limit` (client slot or window), `node_error` (relay 502 or 504), `gone`.
  - Values: calls and duration sum and maximum, for the shared routes only. `GET /swarm/stats` is full-class only (a scoped principal gets the plain 401); it is not in `mountedPrefixes`, so a parent cannot read a child's.
- **Privacy.** Counters hold no token, no digest of a token, no request id, no content, no alias on the relay (it does not parse the body) and no timestamp per row. **An alias is never linked to a token, only to a class label**; on the relay a client label is the operator's own name for the entry. A reflection test fixes the JSON key sets of both routes; a needle scan over a body that follows calls with distinctive tokens, aliases and prompts finds none of them where it must not.
- **Lifetime.** Counters live in the node's `Server` and the relay's `Server`, so they reset with a restart, a rebuild of the subsystem and (relay) every settings save; `since` says from when.
- **Wire, config and API changes.** The two routes, their OpenAPI entry (node), the extended log line; no config key.
- **Edge cases.** A call that ends in the gate (a refused bearer) is counted by the gate, not by the handler, so every refusal on a shared route is counted exactly once; a request for an unknown route under the relay's mount is not counted (only the three shared routes are); a map that grows is bounded by `aliases x classes x outcomes` on the node; on the relay `entries x allowlisted nodes x outcomes` is **not** a bound for `nodes: ["*"]` or the full class (a pairing-token holder can register and expire names), so the relay keeps at most 1024 rows and folds the overflow into a fixed `-` node label, with a churn test; a removed alias keeps its rows until a restart.
- **Tests.** `shareguard.Counters` (concurrent `Add` under `-race`, sorted stable snapshots); node: one row per outcome path (`ok`, `busy`, `limited`, `rate`, `quota`, `upstream`, `invalid`, `auth`, `gone`, `write`) with the tokens of a partial response, an unknown alias counted under `-`, the route's 401 for a shared-model token and its 200 for the main token, the key-set reflection test and the needle scan of the response body; relay: every outcome, a scoped principal's 401 on `GET /swarm/stats`, the node label `-` for a guessed name; the log line carries the class and the token counts and no credential.

## 7. Area E: a node label that silences the sessions warning

**Current state.** A node that joined with a shared-model token answers `401` to its sessions route and the relay shows `<node>: 401 Unauthorized` on every aggregated call (`sessions.go:334-336`). Labels travel in the registration (`join.go:173`, `swarm.join[].labels`, schema `additionalProperties: {type: string}`; the field exists since the first version, `0dcce79b`), are stored and **replaced at every renewal** (`registry.go:228`) and are public in `GET /swarm/nodes` (`NodeInfo.Labels`), but nothing reads one.

**Design.** One reserved key, `coddy.token_class`, one recognised value, `shared_models`: "the token this node registers with opens only the shared-model routes".

- **Derived, not written.** `StartJoins` (`joinset.go:72-95`) adds the label itself when the join's resolved token equals one of `cfg.HTTPServer.EffectiveSharedTokens()` and the process registers as `agent`: `swarmdto.DerivedLabels(cfg, join, kind) map[string]string`, exported so a stand that builds `JoinOptions` directly (`bdd_remote_model_relay_test.go:102`) uses it. It is merged over a **copy** of the operator's labels, **after deleting the reserved key from that copy**, and sets it only when the token is a shared-model one; `cfg` is never mutated. A hand-written `coddy.token_class` therefore never survives on a current node whose token is a main one. The label is recomputed at every registration, so it follows the token the node sends: it cannot say `shared_models` while the token is a main one.
- **Written by hand** is meaningful only for a node of an older binary (a current node overwrites the key, above), which is how it is silenced. The relay accepts only the exact value `shared_models` and ignores any other. `coddy -t` on the node warns when the key is present and the join token is not a shared-model token ("the relay will not list this node's sessions"). A `kind: relay` registration never carries it and the relay ignores it on a relay node (a relay's sessions come through `/swarm/sessions` and hide many nodes).
- **Relay.** `handleSessions` leaves a node whose `Labels[coddy.token_class] == shared_models` and `Kind == agent` out of `fanOut`: no request, no warning, no entry in `node_more`, also when it is the `node=` filter. Whether the relay trusts the label for the skip or still asks and swallows only a `401` is **D4**.
- **What does not change:**
  - the offline warning (`offline since`, `sessions.go:170-179`) and the `not reachable` and transport warnings of every node;
  - the warning of a node that merely has an empty, stale or rotated token (no label), and of a shared-token node of an older version (no label: it still warns, as documented);
  - topology and `GET /swarm/nodes` (the node stays listed with its labels), the node's mount, and `DELETE /swarm/nodes/{node}` (an evicted node is evicted exactly as before; the label gives it no protection and no visibility change).
- **Badge.** `TopologyNode` gains `token_class` (`json:"token_class,omitempty"`), set from the label for agents; the parent copies a child's value (an older child sends none). The SPA shows a badge (`swarm-badge`, as `SwarmView.tsx:359`) on the node's result row and its topology card, key `swarm.node.sharedModels` in both dictionaries (`messagesParity.test.ts`; the Russian wording rules apply), with a screenshot (Dark, 1280) on `docs/operate/swarm.md` per the documentation contract.
- **Wire, config and API changes.** The reserved label key; `TopologyNode.token_class` (additive); no config key.
- **Edge cases.** A node that changes its token from shared to main (or back) changes its label at the next registration and appears in, or leaves, the aggregated list within a lease third (a tunnel node: at its next connect); a node registered twice through two relays carries the label in both registries independently; labels longer than the registration's 64 KiB body bound are refused as today (no new validation: the relay reads one exact key and one exact value and ignores every other label); a hand-written label with another value, a case variant or a trailing space is ignored.
- **Tests.**
  - A derivation table: shared token, main token, none, relay kind, a hand-written reserved key is dropped when the token is not a shared-model one, a copy never mutates `cfg`.
  - The relay skips a labelled agent and keeps every warning listed above; the scenario at `features/remote_model_provider_relay.feature:32-39` flips ("carries no warning for that node", with the stand using `DerivedLabels`) and a sibling scenario keeps the warning for a node without the label.
  - The label follows a token change at the next renewal; a lying node can hide only its own sessions (a labelled node holding a main token disappears from the list and nothing else changes: the cost D4 weighs).

## 8. Area F: the relay erasing a stale token

**Rule.** In `Registry.RegisterWithDial`, for the **owner's** renewal (`existing` held and `LeaseSecret` matches), `existing.token = req.Token` unconditionally: `registry.go:232-234` loses its `if req.Token != ""`.

- A first claim already stores whatever it carries (`:280`); a non-owner never reaches the line (`ErrNameTaken`, `:202-204`); `AttachTransport` does not touch the token; a pinned upstream is registered once and never renewed.
- The registration still needs the pairing token (`handleRegister`, `server.go:187-193`). Nothing else in the line changes.

**Compatibility, read from the code.**

- The token reaches the relay only in the registration document (`RegisterRequest.Token`, `omitempty`). `Client.Register` puts `JoinOptions.NodeToken` into **every** registration of **every** version since the first (W7); a relay that joins a parent sends its own `swarm.auth_token` when it has no join token (`joinset.go:72-74`); a tunnel node registers on every (re)connect.
- A node that has a token therefore carries it at each heartbeat, so the unconditional replacement leaves it untouched. A node without one never had one stored, except in the stale case this fixes.
- The one population that can be hurt is a hand-written registrar that sends the token in its first call and omits it later: its heartbeat now erases the token. `examples/swarm/swarm_e2e.py:331` sends a single registration and the BDD harness's `registration()` (`bdd_swarm_test.go:99-110`) carries a token on every call, so neither is affected.
- A relay-kind node whose client token comes only from `--swarm-auth-token` or the environment sends none already (`joinset.go:74` reads the file value only; the flag does not reach `cfg.Swarm.AuthToken`, `cmd/coddy/serve.go:150-160`). That is today's gap and is not widened.

**Migration and operation.** None in code. A relay upgraded before its nodes, and the reverse, behave as before. After the change:

- removing `swarm.join[].token` from a direct node cuts the relay's token off at its next heartbeat (at most a third of the lease, 30 s by default);
- for a tunnel node it happens at its next connect, which is when its join configuration is read again (`StartJoins` reads it at start, `serve_http.go:131`);
- the "evict it" advice stays for an immediate cut;
- with the token gone the mount sends no `Authorization` (`rewriteFor`, `:360`): a node with authentication answers 401, an open node is open to every full client as documented.

**Wire, config and API changes.** None.

**Tests.**

- `external/swarm` unit table: first claim stores; owner renewal with the same, a different and an empty token (erased), and from empty to set; a non-owner with a token changes nothing; the mount sends no `Authorization` after the erasure and the node's token after a renewal that carries it; a pinned upstream is untouched.
- `internal/swarm` with a real `Client`: a second client sharing the `SecretStore` and carrying no token erases the first's token on its heartbeat.
- Feature scenario in `features/swarm_registry.feature` ("A node that drops its token is cut off at its next heartbeat"); the harness's `registration(name, secret)` gets a variant that carries no token.
- The two documents that say the opposite are rewritten (`docs/operate/swarm.md:347-353`, plan 4.5a last bullet and the 5a Relay row).

## 9. Carried over from phase 2 (section 7.6) that belong to the relay

- **A client of a TLS relay that speaks HTTP/2 and is not the local Coddy is not probed** (`UserTimeoutFor` refuses an h2 stream; the mount logs once per connection). Phase 3 does not remove it; it **bounds** it, and does not eliminate the interference. The relay-to-node leg of a vanished h2 client's call stays alive, so the call also holds a slot of the node's shared credential until it ends; what a scoped entry changes is that the vanished client can hold at most its own `max_streams` relay slots (and spend its own bucket) instead of every slot of the credential the relay's clients share. A two-client test states the residual. The connection-scoped refcounted option stays the fallback if such a client matters.
- **A client behind an intermediary that acknowledges for it** (a TLS terminator, an HTTP proxy in front of the relay or of a direct listener) stays uncovered, and so does a node that dials a direct peer through a CONNECT proxy. The application probe O4 is **not** taken in this phase: it needs a client that answers, which only the local Coddy could be, and the phase-2 decision to keep the wire stable stands. Recorded here so a later phase starts from it.
- **Kernel and platform limits** (a live peer survives 15 to 25 s, no clamp on old kernels, no option off Linux) are the node's and the relay's alike and unchanged; nothing in this phase depends on them.
- The relay's per-client limits make the "one caller" statement of the documentation (`swarm.md:327`) false for scoped clients: the sentence is rewritten, not deleted, for the full class.

## 10. Dilemmas for model-check

Each has two defensible answers. The decision is made with the engine (`mcd`, explicit state), not by this text; a verdict is about the model and the tests are the conformance check. The places of this plan that depend on a dilemma are marked **(D1)** to **(D5)**.

### D1. What the node allowlist means when the relay chains (3.2)

**Question.** A request `/swarm/nodes/a/swarm/nodes/b/coddy/llm/completions` crosses two relays. Does `nodes: [a]` admit it, must the whole path be listed, or must every hop name be in a flat set?

**Options.** (a) the first hop only: the entry names what is directly below this relay and the child's forwarding is trusted; (b) **exact hop paths** (`a/b`; a bare `a` admits only the single-hop path `[a]`; `*` any single-hop path); (c) every hop's name in a flat set of names.

**Why it matters.** The parent's check is the only scope check on the path (W3): a child forwards with its full client token. (a) lets a client allowlisted for a child relay reach the three routes of **every** node behind it, including nodes the operator never meant; names are not identities (a child may hold a node named like one of the parent's), which breaks (c) in a collision.

**Model.** State: relays R1 and R2 with node sets, a name table where R2 also holds a node `n1`, a scoped client with an allowlist in the grammar of the option, a path of up to three hops over `{n1, child, m1, m2}`, a route from `{llm-get, llm-post, admin}`, a spelling from `{plain, encoded}`. Process: the adversary issues every request; the gate decides by the option's rule; the mount forwards with the next relay's full token. Properties: P1 no admitted request reaches a non-LLM route; P2 every admitted final node is in the operator's **intended** set (the leaves written in the intent); P3 a node name reused behind a child never widens reach; L1 an intended leaf is reachable. Expected: (a) violates P2 and P3, (c) violates P3, (b) holds; if the engine disagrees, (b) or a stricter rule is chosen.

### D2. mTLS identity against the relay's credential replacement (4.2)

**Question.** How does a verified client certificate relate to the bearer, and is the identity read per request or per connection?

**Options.** (a) a certificate alone authenticates an entry (a credential of its own); (b) **per entry: token only, certificate only, or both required and bound to the same entry**, identity read per request from `r.TLS`; (c) a listener-wide mode (`required` for everything) with the identity cached at handshake.

**Why it matters.** The relay replaces the credential (W1), so the node cannot see a certificate and the relay is the only place the binding can live. `VerifyPeerCertificate` does not run on resumed connections (4.1), so a cache filled at handshake survives revocation. (a) makes a stolen key as good as a stolen token with no way to require both.

**Model.** State: entries with `{token?, names?}`; a connection with a certificate or none, with a resumed flag; an operator who removes an entry or a name mid-connection; an attacker who holds a subset of `{token_i, key_j}`. Properties: S1 with only a stolen token no request is admitted on an entry that binds both; S2 a removed entry or name is refused on the **next request of an open connection**; S3 a chain-valid certificate with no entry reaches no scoped route; S4 the full class is not reachable by a certificate; S5 a resumed connection is judged like a fresh one; L1 a client holding both is admitted unless revoked. Expected: (a) violates S1 by design, (c) violates S2 and S5, (b) holds.

### D3. Where rate limits live, what they answer and when they spend (5.3)

**Question.** Is the window enforced on the node, on the relay or on both; does a refusal answer `busy` with a `code` or `rate`; is a token spent at the attempt or at the admission?

**Options.** Placement: (a) node only, (b) relay only, (c) **both**. Kind: (i) **`busy` + `code`**, (ii) `rate`, (iii) `quota`. Spending: (x) at the attempt, (y) **after the slot, at the admission**.

**Why it matters.** The node sees the relay as one credential, so only the relay can be fair to its clients, and only the node protects the provider from all of them. The client waits out `busy` only (W6). A client that polls every second while `busy` would drain a bucket spent at the attempt and then be refused by its own waiting.

**Model.** State: a node bucket (tokens, refill period), node slots (`M`), relay buckets and slots per client, `K` clients each looping *request, refused, sleep `max(Retry-After, 1)`, request* under a budget `W`, plus a phase-2 client that treats `busy` as today and `rate` as terminal. Properties: A1 admitted calls per window at each layer never exceed `rate*window + burst`; A2 a lone waiting client is admitted within `ceil(1/rate) + 1` ticks of a token existing; A3 a refused request never reduces tokens (y); A4 with the sum of relay `max_streams` at most `M`, a client at its relay limit never causes another client's node `busy`; A5 a phase-2 client terminates within the budget and never on a terminal error for a window refusal (i); A6 starvation under contention (`K = 2`) is **reachable** and is documented, not promised away. Expected: (x) violates A3, (ii) violates A5, (c)+(i)+(y) hold A1 to A5 and A6 stays reachable.

### D4. What the label does to the sessions aggregation (7)

**Question.** Does the relay skip a labelled node without asking, or still ask and swallow only a `401`?

**Options.** (a) **skip** (no request; the label is trusted for this one purpose); (b) ask, and swallow a `401` for a labelled node while every other outcome (rows, timeout, 5xx) behaves as for any node.

**Why it matters.** (a) saves one request per node per call and keeps the node's log quiet, but a node that is labelled and actually holds a main token disappears from the aggregated list **silently**; (b) shows it and self-corrects, at the cost of a 401 per call. The label is derived from the token actually sent (7), which makes the mislabel unreachable for current nodes but not for a hand-written label on an older node.

**Model.** State: nodes with `{labelled, token in {shared, main, none, stale}}`, a relay in mode (a) or (b). Properties: V1 every node whose sessions a full client could read without the label is listed; V2 every genuine warning of an unlabelled node still appears; V3 a labelled shared-token node never warns; V4 requests per call to labelled nodes. Expected: (a) violates V1 for the hand-written main-token case, (b) holds V1 to V3 and costs V4. The decision trades V1 against V4; if V1 is judged to matter only for hand-written labels, (a) with the `coddy -t` warning of section 7 stands.

### D5. The scope model: a closed class with an exact route table, or route patterns (3.2)

**Question.** Is a scope a fixed class whose routes the code enumerates (an exact table on the decoded remainder), or a per-entry list of route patterns the operator writes?

**Options.** (a) **closed class, exact table**; (b) a prefix on `/coddy/llm/`, in the style of `mountedPrefixes`; (c) per-entry route patterns.

**Why it matters.** The node gains routes with every release (the comment of `mountedPrefixes` says so), and the relay is the boundary. A prefix admits whatever is later added under it; patterns let a typo or a wildcard open an admin route; the closed table cannot be mis-written, at the cost of a code change for a new scoped route.

**Model.** State: the node's route set (the three, plus one route `r4` added under `/coddy/llm/` at step `k`, plus admin routes), a scoped entry in the grammar of the option, request spellings from `{plain, encoded, trailing slash, dot segment, HEAD}`. Properties: R1 at every step no admitted request is routed by the node to a handler outside the three; R2 a legitimate shared-model call is admitted; R3 the decision is by the decoded path. Expected: (b) violates R1 from step `k`, (c) holds only if its grammar has no prefix atom, (a) holds.

## 11. Stages and file ownership

A file has one owner at a time; stages with disjoint file lists run in parallel. Every stage works test first and runs narrow tests (section 13). D1 to D5 precede only the stages named.

| Stage | Owns | After | Parallel with |
|---|---|---|---|
| **H0** token erasure (F) | `external/swarm/registry.go`, new `external/swarm/registry_token_test.go`, new `internal/swarm/join_token_test.go`, `features/swarm_registry.feature` and its steps in `external/swarm/bdd_swarm_test.go` | none | all |
| **H1** configuration | `internal/swarm/joinset.go` (only the lines that copy `cert_file` and `key_file` into the join's `netx.Options`, `joinset.go:85-89`), `internal/config/{swarm.go, swarm_clients.go (new), shared_models.go, shared_models_auth.go, check_shared.go, providers.go, resolve.go, jsondto.go, ui_schema.go, ui_schema_relay.go, config.schema.json}` and their tests, `internal/netx/{dial.go and the loader, tests}`: `swarm.clients`, `swarm.tls.client_*`, `dial.cert_file/key_file`, `shared_models.rate_*`, `cert_names`, the provider keys, `config.ValidSharedAlias`, `netx.ClientTLS`, the class rule, `coddy -t` (including the warning of 7, for which `internal/config` keeps its own copy of the label key, `config.LabelTokenClass`, because it cannot import `internal/swarm`; a test holds the two equal) | none | H0, H1r |
| **H1r** limiter and counters | new `internal/shareguard/*` and tests | none | all |
| **H2a** relay principal, scope, mTLS (A, B) | `external/swarm/{server.go, mount.go (scope and `splitHops` only), serve_swarm.go, principal.go (new), scope.go (new)}`, new `external/swarm/{scopes_test.go, bdd_scopes_test.go, mtls_test.go}`, `features/swarm_client_scopes.feature` | H1; **D1, D2, D5** | H0, H3, H4, H5 |
| **H2b** relay limits and counters (C, D) | new `external/swarm/{limits.go, audit.go, stats.go}` and tests, the limit and counter call lines of `mount.go`, the `Server` fields and `New` lines of `server.go` | H2a, H1r; **D3** | H3, H4, H5 |
| **H3** label (E) | `internal/swarm/dto.go`, `internal/swarm/joinset.go` (the label lines only; the dial-field lines below are H1's, merged first), `external/swarm/{sessions.go, topology.go}`, `external/ui/src/ui/swarm/*`, `external/ui/src/ui/i18n/messages/{en,ru}.ts` and tests | H1 (the `coddy -t` warning); **D4** | H0, H2a, H4, H5 |
| **H4** node limits and counters (C, D) | `external/httpserver/{shared_models_http.go (the take, the counters, the log line), shared_errors.go, shared_limiter.go, shared_rate.go (new), shared_audit.go (new), shared_stats.go (new), auth.go (one counter line), openapi.go, openapi_shared_models.go}` and tests | H1, H1r; **D3** | H0, H2*, H3, H5 |
| **H5** the provider's identity (B) | `internal/llm/{transport.go, proxy_http_client.go, provider.go (one line), coddy_models.go, coddy_usage.go, coddy_errors.go (the window named), coddy_registration_test.go and new tests}`; one-line sites `internal/agent/react.go`, `internal/session/{provider_usage_sources.go, context_window.go}`, `external/httpserver/{server.go, providers_models_http.go}`, `internal/dryrun/{providers.go, coddy_identity.go (new)}`; `external/ui/src/ui/settings/{SettingsSection.tsx, SettingsSection.shared.test.tsx}` (the three keys for a `coddy` row only) | H1 | H0, H2*, H3, H4 |
| **H6** executable specs | `features/remote_model_provider_relay.feature` additions (scoped client, window, label), `external/httpserver/bdd_remote_model_relay_test.go` | H0, H2b, H3, H4 | H5 |
| **H7** (optional) direct listener TLS and mTLS | `external/httpserver/{serve_http.go, tls.go (new), auth.go (the principal hook)}` and tests; its config keys are H1's | H1, H4 | H6 |
| **H8** documentation and generated files | `docs/**`, `AGENTS.md` (table rows of `external/swarm`, `external/httpserver`, `internal/netx`), `DESIGN.md` (the badge), `config.example.yaml`, `internal/skills/bundled/configure-coddy/SKILL.md`, the plan sections of section 13, then `make docs`, `make site-schema` | all | none |
| **H9** integration | nothing owned; the commands of section 13 | all | none |

| Directory | Owners, in order |
|---|---|
| `internal/config`, `internal/netx` | H1 |
| `internal/shareguard` | H1r |
| `external/swarm` | H0 (`registry.go`, its tests, `bdd_swarm_test.go`), H2a, H2b (disjoint files except `mount.go` and `server.go`: H2a then H2b), H3 (`sessions.go`, `topology.go`) |
| `internal/swarm` | H0 (a new test file), H3 |
| `external/httpserver` | H4, H5 (`server.go`, `providers_models_http.go`), H6 (`bdd_remote_model_relay_test.go`), H7 |
| `internal/llm`, `internal/dryrun`, `internal/agent`, `internal/session` | H5 |
| `external/ui` | H3 (`swarm/`, `i18n/`), H5 (`settings/`) |

Stage graph (acyclic): H1 -> H2a -> H2b -> H6; H1r -> H2b; H1r -> H4 -> H6; H1 -> H4; H1 -> H3 -> H6; H1 -> H5; H0 -> H6; H4 -> H7; {all} -> H8 -> H9. No code edge runs from H0, H1r or H5 to the relay stages.

**Names the stages agree on.**

- `config.SwarmClient{Name, Token, Scope, Nodes, CertNames, MaxStreams, RatePerMinute, RateBurst}` and `cfg.Swarm.Clients`; `config.ScopeSharedModels = "shared_models"`; `config.SwarmTLSConfig.{ClientCAFile, ClientAuth}`; `config.SwarmDialConfig.{CertFile, KeyFile}`.
- `config.SharedModelsConfig.{RatePerMinute, RateBurst}` with `(*HTTPServerConfig).EffectiveSharedRate() (perMinute, burst int)`; `config.ValidSharedAlias`.
- `netx.Options.{CertFile, KeyFile}`, `netx.ClientTLS`; `config.ProviderConfig.{CAFile, ClientCertFile, ClientKeyFile}`, `ResolvedLLM.ClientTLS`, `llm.ProviderInput.ClientTLS`.
- `shareguard.Limiter.Take`, `shareguard.Counters`.
- `swarmdto.LabelTokenClass = "coddy.token_class"`, `swarmdto.TokenClassSharedModels = "shared_models"`, `swarmdto.DerivedLabels`; `TopologyNode.TokenClass`.
- Wire codes `rate_window`, `client_streams`, `client_rate`; routes `GET /coddy/shared-models/stats` and `GET /swarm/stats`.
- No new flag, subcommand or environment variable: the man page and the completions stay (`AGENTS.md`, Packaging).

## 12. Risks

- **Backward compatibility of every saved swarm config.**
  - Every new key is optional and absent means today's behaviour. A golden test loads the repository's example configs and the fixtures of `external/swarm` and `internal/config` and compares the principal, the gate and the mount answers before and after.
  - `swarm.clients` is `additionalProperties: false` like its neighbours, so an editor validating against the **published** schema flags the new keys until `make site-schema` publishes it; adding keys is safe to publish with the pull request.
  - `CheckSharedTokenClasses` returns early only when there is neither a shared-model token nor a scoped entry, so every existing configuration passes through the same lines. Phase 1 and 2 clients wait out the new refusals unchanged (5.3).
- **The relay as a security boundary.** The scope is enforced twice (the gate on the pattern, the mount on the canonical decoded remainder, W1 to W3), by a closed table and an exact match, and the unknown-node look-alike runs before the registry lookup. Residuals: a node name is not an identity (3.4); a scoped client's conversation goes wherever the allowlisted name points; `*` widens it to every node that registers; the full class is unchanged and as wide as `docs/operate/swarm.md:416` says; counters and limits reset when the relay is rebuilt.
- **mTLS.** Go checks no revocation: removing the entry or the name is the revocation and takes effect per request; a long-lived CA is a long-lived trust, said in the documentation. `optional` on a shared relay leaves token-only entries valid; `required` needs a certificate on every node that joins (the dial keys). The listener's CA pool is startup state like `swarm.tls` ("rotating them needs a restart", `docs/operate/swarm.md:483`); the dial and provider loaders reload by mtime, the listener's pool does not.
- **Cross-platform and build tags.** `internal/shareguard`, `internal/config`, `internal/netx` and `internal/llm` stay untagged and import nothing tagged; relay code is `//go:build swarm` and its tests `-tags swarm` (`http,swarm` for the stands that mount an agent); no per-OS file is added, but `internal/netx` and `internal/llm` change, so H9 runs `make check-windows` and `make check-android`. The TLS block of H7 uses only `crypto/tls`.
- **Behaviour of every other route unchanged.** The principal adds a context value and no header; the full class passes the unchanged code paths; the node's new refusals exist only when `rate_per_minute` is set; the provider's identity key part is empty for every other type; `TestProviderProxyGuard` and the proxy harness stay green; the busy tests of phases 1 and 2 are untouched.
- **Hand-written registrars** that send the token only on the first registration lose it at their next heartbeat (section 8); `examples/swarm` and the BDD harness are consistent, third-party scripts are not under our control.
- **Counters as a privacy surface.** Class labels only, a key-set reflection test and a needle scan; the node's route is main-token and login only, the relay's full-class only.
- **A limit that is too small looks like a hang.** A client waits out the window under `busy_wait_ms`, so a window longer than the budget ends as the spent-wait error: H5 names the window in it, and `coddy -t` on the lender cannot know the borrowers' budgets, so the documentation gives the arithmetic.

## 13. Verification

Narrow, per stage (tags as the code needs them):

- H0: `go test -tags swarm ./external/swarm -run 'Register|Registry|Token'` and `go test ./internal/swarm`.
- H1: `go test ./internal/config ./internal/netx -count=1`. H1r: `go test -race ./internal/shareguard`.
- H2a, H2b: `go test -tags swarm ./external/swarm -count=1`. H3: the same plus `go test ./internal/swarm` and the vitest line.
- H4: `go test -tags http ./external/httpserver -run 'Shared|Rate|Stats|Audit' -count=1`.
- H5: `go test ./internal/llm -run 'Coddy|ProviderProxyGuard|Transport|ClientTLS'`, `go test ./internal/dryrun ./internal/session ./internal/agent -run 'Coddy|ClientTLS|ProviderUsage'` and the vitest line.
- H6: the two feature runs below. H7: `go test -tags http ./external/httpserver -run 'TLS|MTLS'`.

```
go test ./internal/config ./internal/netx ./internal/shareguard ./internal/swarm ./internal/dryrun -count=1
go test ./internal/llm -run 'Coddy|ProviderProxyGuard|Transport|ClientTLS' -count=1
go test -tags=http ./external/httpserver -run 'Shared|Rate|Stats|Audit|RemoteModel|Usage' -count=1
go test -tags=swarm ./external/swarm -count=1
go test -tags=http,swarm ./external/httpserver ./external/swarm ./internal/swarm -run 'RemoteModel|Scope|Principal|Limit|Stats|Label|Registry|Mount|MTLS|Liveness' -count=1
go test -tags=http -run TestRemoteModelFeature ./external/httpserver -count=1
go test -tags=http,swarm -run TestRemoteModelRelayFeature ./external/httpserver -count=1
go test -tags=swarm -run 'TestSwarmRegistryFeature|TestSwarmSessionsFeature|TestSwarmMountFeature|TestSwarmRelaySettingsFeature' ./external/swarm -count=1
golangci-lint run ./internal/... && golangci-lint run --build-tags http,swarm,cli ./external/... ./internal/...
cd external/ui && npx vitest run src/ui/swarm src/ui/settings src/ui/i18n && npm run typecheck && npm run format:check
```

Whole tree before the push:

- `go build ./... && go build -tags=http,scheduler,memory,cli,gateway,swarm ./...`; `go test -tags=http,scheduler,memory,cli,gateway,swarm ./...`.
- `make test-race` (the limiter, the relay slots and the counters are concurrent code), `make lint`, `make lint-windows`, `make check-windows`, `make check-android`. `make test-agent-rules` is not needed (no rule or hook change).
- Russian wording: the three `git grep` lines of `.claude/rules/russian-wording.md` find only the rule files.
- Documentation: `make docs && make docs-check`, `make site-schema-check`, `make site-docs-check`. Embedded SPA: `make build TAGS="http ui"` (the badge). Screenshots under `docs/assets/swarm/` (Dark, 1280, `<feature>-<state>-<theme>-<width>.png`) for the badge and the relay form's clients list.

**Plan sections to update when this is done** (`docs/plans/remote-model-provider.md`):

- the status line and 6b (a phase-3 paragraph);
- 4.5 (the last bullet: scopes done, and the one-caller sentence for scoped clients);
- 4.5a (the last bullet becomes the rule of section 8, the sessions bullet gains the label, the "dedicated relay" bullet softens);
- 5 (phase 3 done, the direct-listener mTLS moved to H7, the application probe to later) and 5a (the Relay, Auth, Server, Config and SPA rows: the erasure case flips, the scope, label and limit cases join);
- 6a (reports `p3-d1` to `p3-d5`, one line of decision each) and 7 (the `x/time` line);
- `docs/operate/swarm.md` lines 327, 341, 347-353, 416-422 and 502; `docs/features/shared-models.md` (the security and limits sections); `docs/reference/http-api.md` (the two stats routes, the codes); `docs/reference/config.md` by `make docs`.
