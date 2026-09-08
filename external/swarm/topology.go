//go:build swarm

package swarm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// TopologyNode is one member of the swarm as the graph sees it.
type TopologyNode struct {
	UUID      string `json:"uuid"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Transport string `json:"transport,omitempty"`
	Online    bool   `json:"online"`
	Version   string `json:"version,omitempty"`
}

// TopologyEdge is one relay knowing one node, under the name that relay uses.
//
// The name belongs to the edge rather than to the node, because a node is only
// named by whoever registered it: two relays may legitimately know the same
// agent under different names.
type TopologyEdge struct {
	FromUUID string `json:"from_uuid"`
	ToUUID   string `json:"to_uuid"`
	Name     string `json:"name"`
}

// Route is how to reach a node, and how else it could be reached.
type Route struct {
	Path       []string   `json:"path"`
	Alternates [][]string `json:"alternates,omitempty"`
}

// Topology is the whole graph as one relay can see it.
type Topology struct {
	Root     TopologyNode     `json:"root"`
	Nodes    []TopologyNode   `json:"nodes"`
	Edges    []TopologyEdge   `json:"edges"`
	Routes   map[string]Route `json:"routes"`
	Warnings []string         `json:"warnings"`
	// Looped marks a branch that closes back on a relay already in the walk.
	// In a ring that is the expected end of a branch rather than a fault.
	Looped bool `json:"looped,omitempty"`
}

// ComputeRoutes finds, for every node reachable from root, the shortest route
// to it and any equally reachable alternatives.
//
// Relays are not required to form a tree: three of them may each join the other
// two, or a chain may be closed into a ring for redundancy. A breadth-first
// walk is what makes that safe. It visits by increasing hop count, so the first
// route it finds to a node is a shortest one, and a node already seen is never
// expanded twice - which is also why a ring terminates instead of spinning.
//
// Ties break lexicographically so two clients asking the same relay are told
// the same route and a cached one stays valid.
func ComputeRoutes(root string, edges []TopologyEdge) map[string]Route {
	adjacency := map[string][]TopologyEdge{}
	for _, e := range edges {
		adjacency[e.FromUUID] = append(adjacency[e.FromUUID], e)
	}
	for from := range adjacency {
		sort.Slice(adjacency[from], func(i, j int) bool {
			return adjacency[from][i].Name < adjacency[from][j].Name
		})
	}

	routes := map[string]Route{}
	depth := map[string]int{root: 0}
	queue := []string{root}
	paths := map[string][]string{root: {}}
	// The root is where the client already stands. In a real cycle an edge
	// leads back to it, and treating that as a discovery would publish a route
	// from the relay to itself, and cyclic alternates for everything behind it.
	seen := map[string]bool{root: true}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range adjacency[current] {
			if edge.ToUUID == root {
				continue
			}
			candidate := append(append([]string{}, paths[current]...), edge.Name)
			existing, known := routes[edge.ToUUID]
			if !known {
				seen[edge.ToUUID] = true
				routes[edge.ToUUID] = Route{Path: candidate}
				paths[edge.ToUUID] = candidate
				depth[edge.ToUUID] = depth[current] + 1
				queue = append(queue, edge.ToUUID)
				continue
			}
			// A second way in. The first one found is at least as short, so this
			// becomes an alternative an operator can fail over to.
			if pathsEqual(existing.Path, candidate) || containsPath(existing.Alternates, candidate) {
				continue
			}
			existing.Alternates = append(existing.Alternates, candidate)
			sort.Slice(existing.Alternates, func(i, j int) bool {
				if len(existing.Alternates[i]) != len(existing.Alternates[j]) {
					return len(existing.Alternates[i]) < len(existing.Alternates[j])
				}
				return strings.Join(existing.Alternates[i], "/") < strings.Join(existing.Alternates[j], "/")
			})
			routes[edge.ToUUID] = existing
		}
	}
	return routes
}

func pathsEqual(a, b []string) bool {
	return strings.Join(a, "/") == strings.Join(b, "/")
}

func containsPath(paths [][]string, candidate []string) bool {
	for _, p := range paths {
		if pathsEqual(p, candidate) {
			return true
		}
	}
	return false
}

func (s *Server) registerTopologyRoutes() {
	s.mux.HandleFunc("GET /swarm/topology", s.handleTopology)
}

// handleTopology walks the swarm and answers with its shape.
func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	hops, err := s.hopPath(r)
	if err != nil {
		// Coming back to ourselves during discovery is normal in a ring: the
		// caller already has everything below this point, so an empty answer
		// closes the walk rather than failing it.
		writeJSON(w, http.StatusOK, Topology{
			Nodes: []TopologyNode{}, Edges: []TopologyEdge{},
			Routes: map[string]Route{}, Warnings: []string{}, Looped: true,
		})
		return
	}

	root := TopologyNode{
		UUID: s.uuid, Name: s.relayName(), Kind: swarmdto.KindRelay, Online: true,
	}
	topo := Topology{
		Root:     root,
		Nodes:    []TopologyNode{root},
		Edges:    []TopologyEdge{},
		Warnings: []string{},
	}

	deadline := time.Duration(s.cfg.Swarm.EffectiveFanoutTimeoutSeconds()) * time.Second
	nodes := s.registry.List()

	type childResult struct {
		node  swarmdto.NodeInfo
		child *Topology
		warn  string
	}
	results := make([]childResult, len(nodes))
	var wg sync.WaitGroup
	for i, info := range nodes {
		results[i] = childResult{node: info}
		if info.Kind != swarmdto.KindRelay || !info.Online {
			continue
		}
		wg.Add(1)
		go func(i int, info swarmdto.NodeInfo) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), deadline)
			defer cancel()
			child, warn := s.childTopology(ctx, info, hops)
			results[i].child = child
			results[i].warn = warn
		}(i, info)
	}
	wg.Wait()

	seen := map[string]bool{s.uuid: true}
	for _, res := range results {
		info := res.node
		// A child relay is the authority on its own identity. What the registry
		// holds may be whatever the registration happened to carry - for a node
		// listed by hand, an id this relay invented - and an edge pointing at an
		// invented id joins nothing, leaving everything past that child
		// unreachable in the graph even though requests reach it perfectly well.
		uuid := info.InstanceUUID
		if res.child != nil && res.child.Root.UUID != "" {
			uuid = res.child.Root.UUID
		}
		node := TopologyNode{
			UUID: uuid, Name: info.Name, Kind: info.Kind,
			Transport: info.Transport, Online: info.Online, Version: info.Version,
		}
		if !seen[node.UUID] {
			topo.Nodes = append(topo.Nodes, node)
			seen[node.UUID] = true
		}
		topo.Edges = append(topo.Edges, TopologyEdge{
			FromUUID: s.uuid, ToUUID: uuid, Name: info.Name,
		})
		if res.warn != "" {
			topo.Warnings = append(topo.Warnings, res.warn)
		}
		if res.child == nil || res.child.Looped {
			// A branch that closed back on the walk contributes nothing new.
			continue
		}
		// A ring delivers the same node through more than one child, so the
		// graph is merged by identity and the duplicate simply becomes another
		// edge.
		for _, n := range res.child.Nodes {
			if n.UUID == "" || seen[n.UUID] {
				continue
			}
			// The child's own root is already represented by the edge above,
			// under the name this relay knows it by.
			if n.UUID == res.child.Root.UUID {
				continue
			}
			topo.Nodes = append(topo.Nodes, n)
			seen[n.UUID] = true
		}
		topo.Edges = append(topo.Edges, res.child.Edges...)
		for _, warn := range res.child.Warnings {
			topo.Warnings = append(topo.Warnings, info.Name+" -> "+warn)
		}
	}

	topo.Edges = dedupeEdges(topo.Edges)
	topo.Routes = ComputeRoutes(s.uuid, topo.Edges)
	sort.Slice(topo.Nodes, func(i, j int) bool { return topo.Nodes[i].Name < topo.Nodes[j].Name })
	sort.Strings(topo.Warnings)
	writeJSON(w, http.StatusOK, topo)
}

// childTopology asks one child relay for its own view.
func (s *Server) childTopology(ctx context.Context, info swarmdto.NodeInfo, hops []string) (*Topology, string) {
	node, ok := s.registry.Node(info.Name)
	if !ok || node.Transport == nil || !node.Transport.Alive() {
		return nil, info.Name + ": not reachable"
	}
	target := node.Transport.TargetURL()
	if target == nil {
		return nil, info.Name + ": no usable transport"
	}
	base := strings.TrimRight(target.Path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		target.Scheme+"://"+target.Host+base+"/swarm/topology", nil)
	if err != nil {
		return nil, info.Name + ": " + err.Error()
	}
	if node.Token != "" {
		req.Header.Set("Authorization", "Bearer "+node.Token)
	}
	req.Header.Set(SwarmPathHeader, strings.Join(hops, ","))

	res, err := node.Transport.RoundTripper().RoundTrip(req)
	if err != nil {
		return nil, info.Name + ": " + err.Error()
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != http.StatusOK {
		return nil, info.Name + ": " + res.Status
	}
	var child Topology
	if err := json.Unmarshal(body, &child); err != nil {
		return nil, info.Name + ": " + err.Error()
	}
	return &child, ""
}

// dedupeEdges keeps one entry per distinct connection.
func dedupeEdges(edges []TopologyEdge) []TopologyEdge {
	seen := map[string]bool{}
	out := make([]TopologyEdge, 0, len(edges))
	for _, e := range edges {
		if e.FromUUID == "" || e.ToUUID == "" {
			continue
		}
		key := e.FromUUID + "\x00" + e.ToUUID + "\x00" + e.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromUUID != out[j].FromUUID {
			return out[i].FromUUID < out[j].FromUUID
		}
		return out[i].Name < out[j].Name
	})
	return out
}
