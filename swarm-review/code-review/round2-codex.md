## Verdict: REWORK

## Findings

1. [blocker] external/swarm/server.go:63 - Configuring one `allow_private_upstreams` host enables every private address, while allowlisted hosts bypass even metadata/link-local checks - keep exemptions host-scoped and always reject metadata/link-local addresses.
2. [major] internal/config/jsondto.go:12 - `Swarm` is absent from the config DTO and secret-preservation path, so any Settings PUT deletes the entire `swarm:` configuration; normal config validation also omits `Swarm.Normalize/Validate` - add a redacted round-trip DTO or preserve the complete section, and integrate validation.
3. [major] internal/swarm/tunnel.go:67 - Tunnel upgrade writes and reads ignore `ctx` and have no deadline; a peer that accepts TCP but never replies makes `JoinSet.Stop` hang indefinitely - close the connection on cancellation and bound both tunnel-upgrade and proxy-CONNECT handshakes.
4. [major] internal/swarm/tunnel.go:105 - Only the relay detects a half-open tunnel; after it closes its side, the node can remain blocked in `ServeConn` and never reconnect - add a node-side activity watchdog driven by the relay’s HTTP/2 pings or bounded TCP liveness.
5. [major] internal/swarm/joinset.go:58 - When joining multiple parents, each `NewClient` generates a different instance UUID - generate one UUID per process before the loop so session identity and ring deduplication remain stable.
6. [major] external/swarm/sessions.go:322 - Multi-hop aggregation unmarshals child rows into `sessionRow`, but there is no matching `UnmarshalJSON`; all flattened extra fields such as activity and permission state disappear after one relay hop - implement symmetric unmarshalling and test a two-hop row with extras.
7. [major] external/swarm/topology.go:76 - The root is not treated as already routed, so a real cycle creates a route back to the root and cyclic alternates; alternate paths also are not propagated to descendants - walk simple path states with UUID ancestry and explicitly exclude the root.
8. [major] external/swarm/sessions.go:116 - Offline leases are excluded before aggregation, so expired or disconnected nodes produce no warning; the existing “gone” test only covers an online lease whose TCP request fails - emit warnings for retained offline leases and add expired-direct and disconnected-tunnel cases.
9. [major] external/swarm/registry.go:179 - Direct targets are resolved locally before proxy selection, and pinned transports disable environment proxies; proxy-only DNS names cannot work with SOCKS5H/HTTP proxies and the documented environment fallback is ineffective - give trusted configured upstreams a proxy-aware resolution path while retaining pinning for registrations.
10. [minor] external/swarm/mount.go:142 - The escaped remainder is assigned to both `URL.Path` and `URL.RawPath`, causing `%20` to become `%2520` upstream - maintain separate decoded and escaped paths and test what the backend actually receives.
11. [minor] examples/swarm/swarm_e2e.py:217 - The advertised three-relay “ring” is a directed diamond with no edge returning to an earlier relay, so the live stand never exercises cycle handling - add a reverse join and assert no root or cyclic route is emitted.

## What is well covered

The BDD scenarios test registration, authentication, mounting, streaming, aggregation, and warnings through real HTTP boundaries. Tunnel tests use actual HTTP/2 and cover multiplexing and connection replacement; the targeted race suite and combined `http,swarm` tests pass.

## Biggest remaining risk

Tunnel recovery under real network faults remains the largest risk. Current tests use orderly cancellation or replacement, not silent blackholes during upgrade or after establishment, packet loss, or saturation near the stream limit. A fault-injection stand should prove bounded failure and reconnection while long streams are active, and verify that non-idempotent requests are never replayed.