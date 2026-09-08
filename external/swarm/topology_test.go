//go:build swarm

package swarm

import (
	"strings"
	"testing"
)

func pathOf(routes map[string]Route, uuid string) string {
	return strings.Join(routes[uuid].Path, "/")
}

func TestComputeRoutesWalksAChain(t *testing.T) {
	edges := []TopologyEdge{
		{FromUUID: "root", ToUUID: "r2", Name: "relay2"},
		{FromUUID: "r2", ToUUID: "r3", Name: "relay3"},
		{FromUUID: "r3", ToUUID: "a7", Name: "agent7"},
	}
	routes := ComputeRoutes("root", edges)
	if got := pathOf(routes, "a7"); got != "relay2/relay3/agent7" {
		t.Fatalf("route to the deepest agent = %q", got)
	}
	if got := pathOf(routes, "r2"); got != "relay2" {
		t.Fatalf("route to the first hop = %q", got)
	}
}

// A ring is the case that would spin forever without a visited set, and the
// case where "shortest" stops being obvious.
func TestComputeRoutesTakesTheShortWayRoundARing(t *testing.T) {
	// root -> a -> b -> c -> root, and root -> c directly.
	edges := []TopologyEdge{
		{FromUUID: "root", ToUUID: "a", Name: "alpha"},
		{FromUUID: "a", ToUUID: "b", Name: "bravo"},
		{FromUUID: "b", ToUUID: "c", Name: "charlie"},
		{FromUUID: "c", ToUUID: "root", Name: "back-to-root"},
		{FromUUID: "root", ToUUID: "c", Name: "charlie-direct"},
	}
	routes := ComputeRoutes("root", edges)
	if got := pathOf(routes, "c"); got != "charlie-direct" {
		t.Fatalf("route to c = %q, want the one-hop way round", got)
	}
	if got := pathOf(routes, "b"); got != "alpha/bravo" {
		t.Fatalf("route to b = %q", got)
	}
	// The long way round is still worth reporting: it is where a client fails
	// over when the short hop dies.
	alts := routes["c"].Alternates
	if len(alts) == 0 {
		t.Fatal("the ring's other route to c was discarded")
	}
	if strings.Join(alts[0], "/") != "alpha/bravo/charlie" {
		t.Fatalf("alternate route = %v", alts)
	}
}

// A diamond is not a cycle, but it does deliver the same node twice.
func TestComputeRoutesHandlesADiamond(t *testing.T) {
	edges := []TopologyEdge{
		{FromUUID: "root", ToUUID: "b", Name: "left"},
		{FromUUID: "root", ToUUID: "c", Name: "right"},
		{FromUUID: "b", ToUUID: "d", Name: "target"},
		{FromUUID: "c", ToUUID: "d", Name: "target"},
	}
	routes := ComputeRoutes("root", edges)
	got := pathOf(routes, "d")
	if got != "left/target" {
		t.Fatalf("route to d = %q, want the lexicographically first of the two equal routes", got)
	}
	if len(routes["d"].Alternates) != 1 || strings.Join(routes["d"].Alternates[0], "/") != "right/target" {
		t.Fatalf("the second equal route should be an alternate, got %v", routes["d"].Alternates)
	}
}

// Two clients asking the same relay must be told the same route, or a cached
// one stops being valid for no visible reason.
func TestComputeRoutesIsDeterministic(t *testing.T) {
	edges := []TopologyEdge{
		{FromUUID: "root", ToUUID: "x", Name: "zulu"},
		{FromUUID: "root", ToUUID: "y", Name: "alpha"},
		{FromUUID: "x", ToUUID: "z", Name: "target"},
		{FromUUID: "y", ToUUID: "z", Name: "target"},
	}
	first := pathOf(ComputeRoutes("root", edges), "z")
	for i := 0; i < 20; i++ {
		if got := pathOf(ComputeRoutes("root", edges), "z"); got != first {
			t.Fatalf("route changed between calls: %q then %q", first, got)
		}
	}
	if first != "alpha/target" {
		t.Fatalf("ties should break lexicographically, got %q", first)
	}
}

func TestComputeRoutesIgnoresAnUnreachableIsland(t *testing.T) {
	edges := []TopologyEdge{
		{FromUUID: "root", ToUUID: "a", Name: "alpha"},
		{FromUUID: "island1", ToUUID: "island2", Name: "nowhere"},
	}
	routes := ComputeRoutes("root", edges)
	if _, ok := routes["island2"]; ok {
		t.Fatal("a node with no path from the root should have no route")
	}
	if _, ok := routes["a"]; !ok {
		t.Fatal("the reachable node lost its route")
	}
}

func TestComputeRoutesGivesTheRootNoRoute(t *testing.T) {
	routes := ComputeRoutes("root", []TopologyEdge{{FromUUID: "root", ToUUID: "a", Name: "alpha"}})
	if _, ok := routes["root"]; ok {
		t.Fatal("the relay a client is attached to needs no route to itself")
	}
}

func TestDedupeEdgesKeepsDistinctConnections(t *testing.T) {
	edges := dedupeEdges([]TopologyEdge{
		{FromUUID: "a", ToUUID: "b", Name: "one"},
		{FromUUID: "a", ToUUID: "b", Name: "one"},
		{FromUUID: "a", ToUUID: "b", Name: "two"},
		{FromUUID: "", ToUUID: "b", Name: "broken"},
	})
	if len(edges) != 2 {
		t.Fatalf("dedupeEdges kept %d edges, want 2: %+v", len(edges), edges)
	}
}

// A real cycle leads an edge back to where the client already stands.
// Publishing a route from the relay to itself, and cyclic alternates for
// everything behind it, would be nonsense a client might try to follow.
func TestComputeRoutesNeverRoutesBackToTheRoot(t *testing.T) {
	cycle := []TopologyEdge{
		{FromUUID: "root", ToUUID: "a", Name: "alpha"},
		{FromUUID: "a", ToUUID: "b", Name: "bravo"},
		{FromUUID: "b", ToUUID: "root", Name: "back-to-root"},
	}
	routes := ComputeRoutes("root", cycle)
	if _, ok := routes["root"]; ok {
		t.Fatalf("a cycle produced a route to the relay itself: %v", routes["root"])
	}
	if got := pathOf(routes, "b"); got != "alpha/bravo" {
		t.Fatalf("route to b = %q", got)
	}
	for uuid, r := range routes {
		for _, alt := range r.Alternates {
			for _, hop := range alt {
				if hop == "back-to-root" {
					t.Fatalf("node %s got an alternate that loops back: %v", uuid, alt)
				}
			}
		}
	}
}
