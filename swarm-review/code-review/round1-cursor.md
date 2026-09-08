## Verdict: APPROVE_WITH_CHANGES

## Findings

1. [blocker] `internal/swarm/names.go:60` / `internal/config/swarm.go:50` - Docs, schema, and `allow_private_upstreams` claim advertise URLs are an SSRF boundary (refuse loopback, link-local, metadata, private ranges unless allow-listed; plan also wanted DNS pinning). `ValidateAdvertiseURL` only checks scheme / userinfo / query / fragment. A pairing-token holder can advertise `http://127.0.0.1:...` or cloud metadata and the relay will dial it. Fix: resolve host, apply the documented deny/allow policy on every register and upstream seed, refuse redirects (already true via `RoundTrip`), add the SSRF matrix tests the plan listed.

2. [major] `external/swarm/mount.go:40` - Docs and the design say mounts refuse `DELETE /swarm/nodes/{node}`. `controlPlaneRoutes` only lists register and tunnel; `/swarm/nodes` is on the allowlist, so `DELETE /swarm/nodes/<child>/swarm/nodes/<victim>` reaches a child relay with the child's credential. `TestMountAllowsOnlyTheDataPlane` encodes the hole. Fix: deny `DELETE` (and any write) under `/swarm/nodes/` in `mountAllows`, or match method+path; add a feature scenario.

3. [major] `external/swarm/tunnel.go:96` - Lease proof runs after hijack and after writing `200 OK`. Wrong/missing secret or unknown node still gets an accept line, then `AttachTransport` fails and the conn dies. Fix: look up the lease and compare the secret under the registry lock before `Hijack`; only then write the accept and build the HTTP/2 client conn.

4. [major] `external/swarm/registry.go:157` - Every `Register` renew sets `expiresAt = now+ttl`, including tunnel leases whose liveness is supposed to be the connection (`expiresAt` zero after `AttachTransport`). A refresh with the lease secret while a tunnel is up switches `onlineAt` back to the clock and can advertise a dead tunnel until TTL. Direct→tunnel renew also leaves the old direct `RoundTripper` in place until `AttachTransport`. Fix: on tunnel renew, if a live tunnel transport is attached keep `expiresAt` zero (or only bump last-seen); on transport-mode change always `closeTransportLocked` and clear/replace the transport.

5. [major] `external/swarm/transport.go:28` / `registry.go:162` - `swarm.upstreams[].dial` (proxy, CA, insecure) is never applied; `newDirectTransport` always uses a plain `http.Transport`. Join-side dial works; relay→node direct dial through a proxy or private CA does not. Fix: pass `netx.Options` into `newDirectTransport` from register/upstream seed.

6. [major] `external/swarm/mount.go:59` - Session/topology fan-out caps depth at 4 via `X-Coddy-Swarm-Path`; nested mounts do not. A client can walk a ring with `/swarm/nodes/A/swarm/nodes/B/swarm/nodes/A/...` with no hop budget. Fix: count nested `/swarm/nodes/` segments (or propagate an internal hop header on mount rewrites) and refuse past `swarmMaxHops`.

7. [major] `external/swarm/server.go:231` / `bearerOf` at `267` - Mount comments and stripping assume EventSource auth via `?access_token=` on the relay, but the auth gate only reads `Authorization`. Query tokens are dropped before the node and never accepted at the relay, so browser SSE through a mount cannot authenticate the way httpserver does. Fix: accept `access_token` on mount and other EventSource-shaped routes (mirror `httpserver`’s allowlist), then keep stripping it on the outbound hop.

8. [minor] `external/httpserver/listen.go:8` - `internal/httpx` is the hardened server swarm uses; `coddy http` still calls bare `http.ListenAndServe` with no `ReadHeaderTimeout`. Fix: route httpserver through `httpx.NewServer` the same way.

9. [minor] `external/swarm/swarm_test.go:407` / `features/swarm_mount.feature` - Specs assert register refusal and header scrubbing well, but not DELETE/tunnel refusal, SSRF, tunnel auth-before-accept, mount depth, or upstream dial. Several unit cases document the broken allowlist. Fix: turn those into failing tests first, then the code fixes above.

## What is well covered

Lease ownership (pairing token ≠ name proof), secret persistence, concurrent register, tunnel multiplex/streaming/offline detach, mount header/`access_token` scrubbing and path-confusion rejects, session merge with identity vs route and loop warnings, and topology BFS shortest routes are real and tested. Build-tag isolation (`cmd` stub, `swarm_join_stub`, tagged `external/swarm`) holds.

## Biggest remaining risk

One HTTP/2 tunnel is still a single flow-control domain: reconnect via `AttachTransport` aborts in-flight streams, and HOL blocking under mixed bulk + live turns is inherent. That is acceptable if documented. The sharper production risk is the gap between the written security model and the code - advertised SSRF controls and mount control-plane refusal are incomplete, while one client token already means transitive fleet control - so a stolen pairing token or a mount DELETE can reach further than operators are told.

