## Resolution
1. RESOLVED (`internal/swarm/tunnel.go`) — successful writes now refresh tunnel activity.
2. RESOLVED (`external/swarm/topology.go`, `docs/swarm.md`) — alternate-route scope is precisely documented.
3. RESOLVED (`internal/netx/dial.go`) — environment proxy selection respects target and proxy schemes, `NO_PROXY`, and malformed values.
4. RESOLVED (`internal/config/jsondto.go`) — secrets follow destinations across unambiguous renames without following redirects.
5. RESOLVED (`external/swarm/topology_test.go`) — the test now detects repeated node identities along actual edges.
6. RESOLVED (`internal/config/swarm_test.go`) — the test exercises a real YAML write/reload with a single dollar.

## Anything blocking left
None. Non-inherited alternates past merge points are now an explicit documented limitation, not a defect. Fresh affected-package tests pass.

## Verdict: APPROVE
tokens used
71 776
