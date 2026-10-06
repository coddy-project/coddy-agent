//go:build swarm

package swarm

import (
	"strings"
	"testing"
)

// The rows of one session arrive by every route the fan-out found, in the order
// the children happened to answer. The list keeps the shortest route and, of
// routes of the same length, the one whose hops sort first, hop by hop,
// whichever order the rows came in. That is the route ComputeRoutes gives the
// same node on the map, which every case checks on a graph built from the same
// routes, so the two tie-breaks cannot drift apart.
func TestDedupeKeepsTheRouteTheMapWouldTake(t *testing.T) {
	cases := []struct {
		name   string
		routes [][]string
		want   string
	}{
		{"the shorter route wins", [][]string{{"middle", "relay3", "agent7"}, {"shortcut", "agent7"}}, "shortcut/agent7"},
		{"a tie goes to the first hop that sorts first", [][]string{{"right", "x"}, {"left", "x"}}, "left/x"},
		{"a tie is decided at the first hop that differs", [][]string{{"a", "z", "x"}, {"a", "y", "x"}}, "a/y/x"},
		// A joined path would put "a-b/x" first, because '-' sorts before '/',
		// while the map takes the hop named "a" before the one named "a-b".
		{"hops are compared as names, not as one joined path", [][]string{{"a-b", "x"}, {"a", "x"}}, "a/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(ComputeRoutes("root", edgesAlong(tc.routes))["target"].Path, "/"); got != tc.want {
				t.Fatalf("the map reaches the node by %q, want %q", got, tc.want)
			}
			backwards := make([][]string, 0, len(tc.routes))
			for i := len(tc.routes) - 1; i >= 0; i-- {
				backwards = append(backwards, tc.routes[i])
			}
			for _, order := range [][][]string{tc.routes, backwards} {
				var rows []sessionRow
				for _, route := range order {
					rows = append(rows, sessionRow{ID: "sess_1", AgentUUID: "uuid-agent", NodePath: route})
				}
				out := dedupeByIdentity(rows)
				if len(out) != 1 {
					t.Fatalf("one session reached %d ways should be listed once, got %d rows", len(order), len(out))
				}
				if got := strings.Join(out[0].NodePath, "/"); got != tc.want {
					t.Fatalf("routes %v kept %q, want %q", order, got, tc.want)
				}
			}
		})
	}
}

// edgesAlong builds the graph a set of routes walks: from "root", one node per
// distinct run of hops, every route ending at the same node, "target".
func edgesAlong(routes [][]string) []TopologyEdge {
	var edges []TopologyEdge
	for _, route := range routes {
		from := "root"
		for i, hop := range route {
			to := "target"
			if i < len(route)-1 {
				to = strings.Join(route[:i+1], "/")
			}
			edges = append(edges, TopologyEdge{FromUUID: from, ToUUID: to, Name: hop})
			from = to
		}
	}
	return edges
}
