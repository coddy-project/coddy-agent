## Findings resolution

1. RESOLVED — `internal/netx/egress.go` and `external/swarm/server.go` scope relaxation to the named host while always rejecting metadata and link-local addresses.
2. RESOLVED — `internal/config/jsondto.go`, `internal/config/config.go`, and `internal/config/ui_schema.go` add the redacted round-trip, normalization, validation, and UI exclusion.
3. REMAINS — `internal/netx/dial.go`: the cancellation guard starts after `dialRelay`, so a silent HTTP proxy can still block indefinitely during CONNECT.
4. REMAINS — `internal/swarm/tunnel.go`: HTTP/2 `Server.IdleTimeout` explicitly ignores PING frames and is disabled by active streams; use `ReadIdleTimeout` and `PingTimeout`.
5. RESOLVED — `internal/swarm/joinset.go` creates one process identity and supplies it to every join client.
6. RESOLVED — `external/swarm/sessions.go` now symmetrically collects unknown fields during unmarshalling.
7. REMAINS — `external/swarm/topology.go`: only edges returning to the root are excluded; non-root cycles still produce cyclic alternates, and alternate routes are not propagated to descendants.
8. RESOLVED — `external/swarm/sessions.go` includes stale leases as filtered, sorted warnings.
9. REMAINS — `internal/netx/dial.go` and `external/swarm/transport.go`: explicit proxies work, but empty proxy options still ignore environment proxies for tunnels, while pinned direct transports disable `ProxyFromEnvironment`.
10. RESOLVED — `external/swarm/mount.go` now assigns decoded `Path` and escaped `RawPath`.
11. REMAINS — `examples/swarm/swarm_e2e.py` registers the back edge with a fabricated UUID and checks repeated names, so topology does not see an identity ring and the no-route-home assertion is ineffective.

## New defects

- [major] `internal/config/jsondto.go:828` — upstream secrets are preserved by name alone, so changing an upstream URL transfers its credential and proxy to a new destination; normalize first and match both upstreams and joins by canonical name plus URL.
- [major] `external/swarm/sessions.go:406` and `external/swarm/topology.go:142` — depth overflow and an actual cycle share one error path, so an acyclic fifth relay is reported as `looped` and disappears without a warning; return distinct loop and depth outcomes.
- [major] `external/swarm/topology.go:147` — a looped topology response omits its root identity, preventing static back edges from being reconciled with the real relay UUID; include `Root` even when `Looped` is true.
- [major] `external/swarm/mount.go:148` — `%2e` and `%2e%2e` pass validation and are then decoded into dot segments; reject segments after URL-unescaping them.
- [minor] `internal/config/jsondto.go:878` — swarm proxy URLs are omitted from dollar escaping, so credentials containing `$` are corrupted by environment expansion after a config save and reload.

## Verdict: REWORK
tokens used
186 733
