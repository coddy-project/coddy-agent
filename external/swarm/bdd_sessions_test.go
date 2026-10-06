//go:build swarm

package swarm

// Godog harness for features/swarm_sessions.feature. Stand-in agents serve the
// same /coddy/sessions shape a real node does, and a second relay is a real
// relay, so recursion is exercised rather than simulated.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// stubAgent serves the session list surface of a coddy serve node.
type stubAgent struct {
	mu       sync.Mutex
	sessions []map[string]interface{}
	ts       *httptest.Server
}

func newStubAgent() *stubAgent {
	a := &stubAgent{}
	a.ts = httptest.NewServer(http.HandlerFunc(a.serve))
	return a
}

func (a *stubAgent) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/coddy/sessions" {
		http.NotFound(w, r)
		return
	}
	needle := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	a.mu.Lock()
	rows := make([]map[string]interface{}, 0, len(a.sessions))
	for _, s := range a.sessions {
		if needle != "" {
			title, _ := s["title"].(string)
			cwd, _ := s["cwd"].(string)
			if !strings.Contains(strings.ToLower(title), needle) && !strings.Contains(strings.ToLower(cwd), needle) {
				continue
			}
		}
		rows = append(rows, s)
	}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "coddy.session_list", "sessions": rows, "hasMore": false,
	})
}

func (a *stubAgent) add(id, title string) {
	a.mu.Lock()
	a.sessions = append(a.sessions, map[string]interface{}{
		"id": id, "title": title, "updatedAt": fmt.Sprintf("2026-09-08T12:%02d:00Z", len(a.sessions)),
	})
	a.mu.Unlock()
}

type sessionsFeatureState struct {
	relay  *httptest.Server
	srv    *Server
	client string
	pair   string

	agents     []*stubAgent
	agentNames []string
	relays     []*httptest.Server
	ring       *testRing

	childUUID string
	status    int
	body      []byte
}

func (s *sessionsFeatureState) reset() {
	if s.relay != nil {
		s.relay.Close()
		s.relay = nil
	}
	for _, a := range s.agents {
		a.ts.Close()
	}
	for _, r := range s.relays {
		r.Close()
	}
	if s.ring != nil {
		s.ring.Close()
		s.ring = nil
	}
	s.agents, s.agentNames, s.relays = nil, nil, nil
	s.status, s.body, s.childUUID = 0, nil, ""
}

func (s *sessionsFeatureState) aRelay(pair, client string) error {
	s.reset()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1" // a loopback relay may reach loopback nodes
	cfg.Swarm.Name = "outer"
	cfg.Swarm.PairingTokens = []string{pair}
	cfg.Swarm.AuthToken = client
	s.pair, s.client = pair, client
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	s.srv = srv
	s.relay = httptest.NewServer(srv.Handler())
	return nil
}

func (s *sessionsFeatureState) nodeHoldsSession(node, id, title string) error {
	agent := s.agentNamed(node)
	if agent == nil {
		agent = newStubAgent()
		s.agents = append(s.agents, agent)
		if _, err := s.srv.registry.Register(swarmdto.RegisterRequest{
			Name: node, Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
			AdvertiseURL: agent.ts.URL, InstanceUUID: "uuid-" + node, Token: "tok-" + node,
		}); err != nil {
			return err
		}
		s.agentNames = append(s.agentNames, node)
	}
	agent.add(id, title)
	return nil
}

func (s *sessionsFeatureState) agentNamed(node string) *stubAgent {
	for i, name := range s.agentNames {
		if name == node && i < len(s.agents) {
			return s.agents[i]
		}
	}
	return nil
}

func (s *sessionsFeatureState) unreachableNode(node string) error {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := dead.URL
	dead.Close()
	_, err := s.srv.registry.Register(swarmdto.RegisterRequest{
		Name: node, Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: url, InstanceUUID: "uuid-" + node, Token: "tok",
	})
	return err
}

func (s *sessionsFeatureState) childRelayHolding(child, node, id, title string) error {
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1" // a loopback relay may reach loopback nodes
	cfg.Swarm.Name = child
	cfg.Swarm.AuthToken = "child-secret"
	cfg.Swarm.PairingTokens = []string{"child-pair"}
	childSrv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	s.childUUID = childSrv.UUID()

	agent := newStubAgent()
	agent.add(id, title)
	s.agents = append(s.agents, agent)
	s.agentNames = append(s.agentNames, node)
	if _, err := childSrv.registry.Register(swarmdto.RegisterRequest{
		Name: node, Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: agent.ts.URL, InstanceUUID: "uuid-" + node, Token: "tok-" + node,
	}); err != nil {
		return err
	}

	childTS := httptest.NewServer(childSrv.Handler())
	s.relays = append(s.relays, childTS)
	_, err = s.srv.registry.Register(swarmdto.RegisterRequest{
		Name: child, Kind: swarmdto.KindRelay, Transport: swarmdto.TransportDirect,
		AdvertiseURL: childTS.URL, InstanceUUID: "uuid-" + child, Token: "child-secret",
	})
	return err
}

// aRingOfThreeRelays closes the shape of docs/operate/swarm.md into a ring around
// the relay of the Background: outer asks middle and, by a shortcut, relay3;
// middle asks relay3; relay3 asks outer again. An agent sits behind relay3.
func (s *sessionsFeatureState) aRingOfThreeRelays(id, title string) error {
	outer := &ringRelay{name: "outer", srv: s.srv, ts: s.relay, token: s.client}
	ring, err := newTestRing(outer, id, title)
	if err != nil {
		return err
	}
	s.ring = ring
	return nil
}

func (s *sessionsFeatureState) listSessions() error {
	return s.get("/swarm/sessions", nil)
}

func (s *sessionsFeatureState) searchSessions(term string) error {
	return s.get("/swarm/sessions?q="+term, nil)
}

func (s *sessionsFeatureState) askAsPartOfTheSameChain() error {
	// The relay's own uuid already in the chain is exactly what a ring looks
	// like from the inside.
	return s.get("/swarm/sessions", map[string]string{SwarmPathHeader: s.srv.UUID()})
}

func (s *sessionsFeatureState) get(path string, headers map[string]string) error {
	req, err := http.NewRequest(http.MethodGet, s.relay.URL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.client)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.status = res.StatusCode
	s.body, _ = io.ReadAll(res.Body)
	return nil
}

type aggregatedList struct {
	Sessions []struct {
		ID       string   `json:"id"`
		Title    string   `json:"title"`
		NodeName string   `json:"node_name"`
		NodePath []string `json:"node_path"`
	} `json:"sessions"`
	Warnings []string `json:"warnings"`
}

func (s *sessionsFeatureState) decode() (aggregatedList, error) {
	var out aggregatedList
	if err := json.Unmarshal(s.body, &out); err != nil {
		return out, fmt.Errorf("decode: %w (%s)", err, s.body)
	}
	return out, nil
}

func (s *sessionsFeatureState) listHolds(n int) error {
	out, err := s.decode()
	if err != nil {
		return err
	}
	if len(out.Sessions) != n {
		return fmt.Errorf("list holds %d sessions, want %d: %s", len(out.Sessions), n, s.body)
	}
	return nil
}

func (s *sessionsFeatureState) sessionLabelled(id, node string) error {
	out, err := s.decode()
	if err != nil {
		return err
	}
	for _, row := range out.Sessions {
		if row.ID == id && row.NodeName == node {
			return nil
		}
	}
	return fmt.Errorf("no session %q labelled with node %q: %s", id, node, s.body)
}

func (s *sessionsFeatureState) rowsToldApartByNode() error {
	out, err := s.decode()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, row := range out.Sessions {
		key := row.NodeName + "/" + row.ID
		if seen[key] {
			return fmt.Errorf("two rows share node and id: %s", s.body)
		}
		seen[key] = true
	}
	if len(seen) != len(out.Sessions) {
		return fmt.Errorf("rows are not distinguishable: %s", s.body)
	}
	return nil
}

func (s *sessionsFeatureState) warningNamesNode(node string) error {
	out, err := s.decode()
	if err != nil {
		return err
	}
	for _, warn := range out.Warnings {
		if strings.Contains(warn, node) {
			return nil
		}
	}
	return fmt.Errorf("no warning names %q: %s", node, s.body)
}

func (s *sessionsFeatureState) reachableThroughPath(id, path string) error {
	out, err := s.decode()
	if err != nil {
		return err
	}
	for _, row := range out.Sessions {
		if row.ID == id {
			got := strings.Join(row.NodePath, "/")
			if got != path {
				return fmt.Errorf("session %q has path %q, want %q", id, got, path)
			}
			return nil
		}
	}
	return fmt.Errorf("session %q is not in the list: %s", id, s.body)
}

func (s *sessionsFeatureState) emptyAndAlreadyWalked() error {
	var out struct {
		Sessions []json.RawMessage `json:"sessions"`
		Looped   bool              `json:"looped"`
		Reason   string            `json:"reason"`
	}
	if err := json.Unmarshal(s.body, &out); err != nil {
		return fmt.Errorf("decode: %w (%s)", err, s.body)
	}
	if len(out.Sessions) != 0 {
		return fmt.Errorf("a looping request should return nothing, got %d rows", len(out.Sessions))
	}
	if !out.Looped {
		return fmt.Errorf("the answer does not say the branch was already walked: %s", s.body)
	}
	if !strings.Contains(strings.ToLower(out.Reason), "loop") {
		return fmt.Errorf("the reason does not explain itself: %s", s.body)
	}
	return nil
}

// A ring is a shape somebody chose, so the guard firing on every request is
// normal. Reporting it as a warning would teach an operator to ignore the
// warnings that do matter.
func (s *sessionsFeatureState) noWarningRaised() error {
	out, err := s.decode()
	if err != nil {
		return err
	}
	if len(out.Warnings) != 0 {
		return fmt.Errorf("a closed ring raised warnings: %v", out.Warnings)
	}
	return nil
}

func TestSwarmSessionsFeature(t *testing.T) {
	st := &sessionsFeatureState{}
	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Step(`^a swarm relay with pairing token "([^"]*)" and client token "([^"]*)"$`, st.aRelay)
			ctx.Step(`^the node "([^"]*)" holds a session "([^"]*)" titled "([^"]*)"$`, st.nodeHoldsSession)
			ctx.Step(`^the node "([^"]*)" is registered but unreachable$`, st.unreachableNode)
			ctx.Step(`^a child relay "([^"]*)" holding a node "([^"]*)" with a session "([^"]*)" titled "([^"]*)"$`, st.childRelayHolding)
			ctx.Step(`^a ring of three relays with an agent behind the third holding a session "([^"]*)" titled "([^"]*)"$`, st.aRingOfThreeRelays)
			ctx.Step(`^I list swarm sessions with the client token$`, st.listSessions)
			ctx.Step(`^I search swarm sessions for "([^"]*)"$`, st.searchSessions)
			ctx.Step(`^the child relay asks this relay for its sessions as part of the same chain$`, st.askAsPartOfTheSameChain)
			ctx.Step(`^the list holds (\d+) sessions$`, st.listHolds)
			ctx.Step(`^the session "([^"]*)" is labelled with the node "([^"]*)"$`, st.sessionLabelled)
			ctx.Step(`^both rows are told apart by their node$`, st.rowsToldApartByNode)
			ctx.Step(`^a warning names the node "([^"]*)"$`, st.warningNamesNode)
			ctx.Step(`^the session "([^"]*)" is reachable through the path "([^"]*)"$`, st.reachableThroughPath)
			ctx.Step(`^the answer is empty and says the branch was already walked$`, st.emptyAndAlreadyWalked)
			ctx.Step(`^no warning is raised, because a closed ring is the shape and not a fault$`, st.noWarningRaised)
			ctx.Step(`^I read the swarm topology with the client token$`, st.readTopology)
			ctx.Step(`^the topology names this relay as its root$`, st.topologyRootIsThisRelay)
			ctx.Step(`^the topology holds the node "([^"]*)"$`, st.topologyHoldsNode)
			ctx.Step(`^the topology lists (\d+) nodes, each of them once$`, st.topologyListsEachNodeOnce)
			ctx.Step(`^an edge leads from relay3 back to this relay$`, st.anEdgeLeadsFromRelay3BackToThisRelay)
			ctx.Step(`^the topology raises no warning$`, st.topologyRaisesNoWarning)
			ctx.Step(`^the topology has a route to "([^"]*)"$`, st.topologyHasRouteTo)
			ctx.Step(`^the route to "([^"]*)" is "([^"]*)"$`, st.routeIs)
			ctx.Step(`^the route to "([^"]*)" is "([^"]*)", with "([^"]*)" kept as an alternate$`, st.routeKeepsAlternate)
			ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
				st.reset()
				return ctx, nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/swarm_sessions.feature", "../../features/swarm_topology.feature"},
			TestingT: t,
			Output:   os.Stdout,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("swarm sessions feature failed")
	}
}

// ---- topology steps ----

type topologyView struct {
	Root     TopologyNode     `json:"root"`
	Nodes    []TopologyNode   `json:"nodes"`
	Edges    []TopologyEdge   `json:"edges"`
	Routes   map[string]Route `json:"routes"`
	Warnings []string         `json:"warnings"`
}

func (s *sessionsFeatureState) readTopology() error {
	return s.get("/swarm/topology", nil)
}

func (s *sessionsFeatureState) decodeTopology() (topologyView, error) {
	var out topologyView
	if err := json.Unmarshal(s.body, &out); err != nil {
		return out, fmt.Errorf("decode topology: %w (%s)", err, s.body)
	}
	return out, nil
}

func (s *sessionsFeatureState) topologyRootIsThisRelay() error {
	out, err := s.decodeTopology()
	if err != nil {
		return err
	}
	if out.Root.UUID != s.srv.UUID() {
		return fmt.Errorf("root uuid = %q, want this relay's own %q", out.Root.UUID, s.srv.UUID())
	}
	return nil
}

func (s *sessionsFeatureState) topologyHoldsNode(name string) error {
	out, err := s.decodeTopology()
	if err != nil {
		return err
	}
	for _, n := range out.Nodes {
		if n.Name == name {
			return nil
		}
	}
	return fmt.Errorf("no node %q in the topology: %s", name, s.body)
}

// A relay reached by two ways, and a relay that walks back to where it started,
// must each appear once: the graph is merged by identity, not by the number of
// routes that arrived at it.
func (s *sessionsFeatureState) topologyListsEachNodeOnce(want int) error {
	out, err := s.decodeTopology()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, n := range out.Nodes {
		if seen[n.UUID] {
			return fmt.Errorf("node %q (%s) is listed twice: %s", n.Name, n.UUID, s.body)
		}
		seen[n.UUID] = true
	}
	if len(out.Nodes) != want {
		return fmt.Errorf("the topology lists %d nodes, want %d: %s", len(out.Nodes), want, s.body)
	}
	return nil
}

// The edge that closes a ring is how a client learns the swarm is a ring and not
// a tree, so it must survive being answered with "already walked". It has to be
// the edge from relay3 to the first relay, under the name relay3 knows it by, not
// merely some edge that happens to end there.
func (s *sessionsFeatureState) anEdgeLeadsFromRelay3BackToThisRelay() error {
	if s.ring == nil {
		return fmt.Errorf("the scenario has no ring")
	}
	out, err := s.decodeTopology()
	if err != nil {
		return err
	}
	for _, e := range out.Edges {
		if e.FromUUID == s.ring.relay3.srv.UUID() && e.ToUUID == s.srv.UUID() {
			if e.Name != "outer" {
				return fmt.Errorf("the closing edge is named %q, want %q: %s", e.Name, "outer", s.body)
			}
			return nil
		}
	}
	return fmt.Errorf("no edge leads from relay3 back to this relay, so the ring looks like a tree: %s", s.body)
}

func (s *sessionsFeatureState) topologyRaisesNoWarning() error {
	out, err := s.decodeTopology()
	if err != nil {
		return err
	}
	if len(out.Warnings) != 0 {
		return fmt.Errorf("a healthy ring raised topology warnings: %v", out.Warnings)
	}
	return nil
}

func (s *sessionsFeatureState) topologyHasRouteTo(name string) error {
	_, err := s.routeTo(name)
	return err
}

func (s *sessionsFeatureState) routeIs(name, want string) error {
	got, err := s.routeTo(name)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("route to %q is %q, want %q", name, got, want)
	}
	return nil
}

// The long way round a ring is where a client fails over when the short hop
// dies, so it has to survive the merge as an alternate of the relay it leads to.
func (s *sessionsFeatureState) routeKeepsAlternate(name, want, alternate string) error {
	route, err := s.routeOf(name)
	if err != nil {
		return err
	}
	if got := strings.Join(route.Path, "/"); got != want {
		return fmt.Errorf("route to %q is %q, want %q", name, got, want)
	}
	for _, alt := range route.Alternates {
		if strings.Join(alt, "/") == alternate {
			return nil
		}
	}
	return fmt.Errorf("route to %q keeps no alternate %q: %v", name, alternate, route.Alternates)
}

func (s *sessionsFeatureState) routeTo(name string) (string, error) {
	route, err := s.routeOf(name)
	if err != nil {
		return "", err
	}
	return strings.Join(route.Path, "/"), nil
}

func (s *sessionsFeatureState) routeOf(name string) (Route, error) {
	out, err := s.decodeTopology()
	if err != nil {
		return Route{}, err
	}
	for _, n := range out.Nodes {
		if n.Name != name {
			continue
		}
		route, ok := out.Routes[n.UUID]
		if !ok {
			return Route{}, fmt.Errorf("node %q has no route: %s", name, s.body)
		}
		return route, nil
	}
	return Route{}, fmt.Errorf("no node %q in the topology: %s", name, s.body)
}
