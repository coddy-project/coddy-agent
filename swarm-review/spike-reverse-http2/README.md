# Spike: reverse HTTP/2 tunnel

Proves a node behind NAT can dial OUT to a relay and still serve its whole HTTP surface back
through that one connection, with no new dependency (`golang.org/x/net` is already direct).

The file is kept as `.txt` so it is documentation, not a package in this module; it becomes a
real test in `internal/swarm` when the tunnel transport lands. To run it, copy it into a
scratch module requiring `golang.org/x/net`:

    go test -run TestReverseHTTP2Tunnel -v

Result (2026-09-08):

    unary ok: {"auth":"Bearer node-token","method":"GET"}
    sse ok, first chunk in 10.78µs
    8 concurrent multiplexed requests ok
    PASS
