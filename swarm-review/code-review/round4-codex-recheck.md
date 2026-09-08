## Resolution

1. RESOLVED — `internal/netx/dial.go` bounds CONNECT, closes on cancellation, and clears the deadline after establishment.
2. REMAINS — `internal/swarm/tunnel.go` records reads only, so low-volume one-way output can suppress relay PINGs while providing no inbound activity, causing a live tunnel to close.
3. REMAINS — `external/swarm/topology.go` keeps ancestry for each node’s primary path, not each route; shortest paths remain correct, but alternatives are never propagated to descendants.
4. REMAINS — `internal/netx/dial.go` always queries `HTTPS_PROXY`, silently dials directly on proxy parsing errors, and treats SOCKS environment proxies as HTTP CONNECT proxies.
5. RESOLVED — `examples/swarm/swarm_e2e.py` uses the real outer UUID and verifies an edge returns to it.
6. REMAINS — `internal/config/jsondto.go` prevents redirection, but both matching keys include the name, so renaming an entry drops its secrets rather than preserving them.
7. RESOLVED — `external/swarm/sessions.go` and `external/swarm/topology.go` distinguish loops from excessive depth and retain warnings for the latter.
8. RESOLVED — `external/swarm/topology.go` includes the relay identity in looped topology responses.
9. RESOLVED — `external/swarm/mount.go` rejects decoded dot segments while preserving legitimate escapes such as `%20`.
10. RESOLVED — `internal/config/jsondto.go` copies both swarm slices and dollar-escapes their proxy URLs without mutating live configuration.

## New defects

- [minor] `external/swarm/topology_test.go:158` checks repeated edge names rather than UUID ancestry; its labels are all distinct, so it would also pass with the previous cyclic alternate.
- [minor] `internal/config/swarm_test.go:72` starts with an already doubled dollar and never reloads the YAML, so the “round-trip” test would pass even if escaping were a no-op.

## Verdict: REWORK
tokens used
98 245
