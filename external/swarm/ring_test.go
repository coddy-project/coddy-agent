//go:build swarm

package swarm

// A ring of three real relays, in process. A mutation run over the swarm tests
// showed that nothing automated walked a ring: the loop guard was only ever fed
// a header the test wrote itself, so a relay that forgot to extend or to pass on
// the path header, a fan-out that listed one agent twice, a topology that lost
// the edge closing the ring, and a warning that vanished or was capped at one
// all went unnoticed. The tests below pin those behaviours.

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// ringRelay is one relay of a test ring: a real Server behind a real listener.
type ringRelay struct {
	name  string
	srv   *Server
	ts    *httptest.Server
	token string
}

func newRingRelay(name string) (*ringRelay, error) {
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1" // a loopback relay may reach loopback nodes
	cfg.Swarm.Name = name
	cfg.Swarm.AuthToken = "token-" + name
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return nil, err
	}
	return &ringRelay{name: name, srv: srv, ts: httptest.NewServer(srv.Handler()), token: cfg.Swarm.AuthToken}, nil
}

// knows makes the relay hold child under the given name, the way a join does.
func (r *ringRelay) knows(child *ringRelay, as string) error {
	_, err := r.srv.registry.Register(swarmdto.RegisterRequest{
		Name: as, Kind: swarmdto.KindRelay, Transport: swarmdto.TransportDirect,
		AdvertiseURL: child.ts.URL, InstanceUUID: "uuid-" + child.name, Token: child.token,
	})
	return err
}

// holdsAgent registers a stand-in agent that serves one session.
func (r *ringRelay) holdsAgent(name string, agent *stubAgent) error {
	_, err := r.srv.registry.Register(swarmdto.RegisterRequest{
		Name: name, Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: agent.ts.URL, InstanceUUID: "uuid-" + name, Token: "tok-" + name,
	})
	return err
}

// get asks the relay as a client holding its token.
func (r *ringRelay) get(path string, headers map[string]string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, r.ts.URL+path, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	return res.StatusCode, body, err
}

// sessionAnswer is what /swarm/sessions says, including the fields that tell a
// closed ring from a chain that is merely too long.
type sessionAnswer struct {
	Sessions []struct {
		ID       string   `json:"id"`
		NodePath []string `json:"node_path"`
	} `json:"sessions"`
	Warnings []string `json:"warnings"`
	Looped   bool     `json:"looped"`
}

func (r *ringRelay) sessionList(t *testing.T, headers map[string]string) sessionAnswer {
	t.Helper()
	status, body, err := r.get("/swarm/sessions", headers)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("GET /swarm/sessions = %d: %s", status, body)
	}
	var out sessionAnswer
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return out
}

func (r *ringRelay) topology(t *testing.T, headers map[string]string) Topology {
	t.Helper()
	status, body, err := r.get("/swarm/topology", headers)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("GET /swarm/topology = %d: %s", status, body)
	}
	var out Topology
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return out
}

// testRing is the shape drawn in docs/operate/swarm.md, closed into a ring:
//
//	outer --> middle --> relay3 --> agent7
//	outer --> relay3 (the shortcut, which outer knows as "shortcut")
//	relay3 --> outer (the edge that closes the ring)
type testRing struct {
	outer, middle, relay3 *ringRelay
	agent                 *stubAgent
	owned                 []*ringRelay
}

// newTestRing builds the ring around outer, or around a fresh relay when outer
// is nil. An outer passed in stays the caller's to close.
func newTestRing(outer *ringRelay, sessionID, title string) (*testRing, error) {
	ring := &testRing{outer: outer, agent: newStubAgent()}
	ring.agent.add(sessionID, title)
	fail := func(err error) (*testRing, error) {
		ring.Close()
		return nil, err
	}
	for _, slot := range []struct {
		dst  **ringRelay
		name string
	}{{&ring.outer, "outer"}, {&ring.middle, "middle"}, {&ring.relay3, "relay3"}} {
		if *slot.dst != nil {
			continue
		}
		relay, err := newRingRelay(slot.name)
		if err != nil {
			return fail(err)
		}
		*slot.dst = relay
		ring.owned = append(ring.owned, relay)
	}
	for _, edge := range []struct {
		parent, child *ringRelay
		as            string
	}{
		{ring.outer, ring.middle, "middle"},
		{ring.outer, ring.relay3, "shortcut"},
		{ring.middle, ring.relay3, "relay3"},
		{ring.relay3, ring.outer, "outer"},
	} {
		if err := edge.parent.knows(edge.child, edge.as); err != nil {
			return fail(err)
		}
	}
	if err := ring.relay3.holdsAgent("agent7", ring.agent); err != nil {
		return fail(err)
	}
	return ring, nil
}

func (r *testRing) Close() {
	for _, relay := range r.owned {
		relay.ts.Close()
	}
	r.agent.ts.Close()
}

func mustRing(t *testing.T) *testRing {
	t.Helper()
	ring, err := newTestRing(nil, "sess_ring", "ring work")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ring.Close)
	return ring
}

// hops makes a path header of n relays this relay has never met.
func hops(n int) string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("relay-uuid-%d", i)
	}
	return strings.Join(ids, ",")
}

func soloRelayWithOneAgent(t *testing.T) *ringRelay {
	t.Helper()
	relay, err := newRingRelay("solo")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(relay.ts.Close)
	agent := newStubAgent()
	agent.add("sess_solo", "solo work")
	t.Cleanup(agent.ts.Close)
	if err := relay.holdsAgent("agent1", agent); err != nil {
		t.Fatal(err)
	}
	return relay
}

// One relay down must cost exactly one warning and nothing else while another
// way round exists, and two relays down exactly two. A warning for one branch
// must not hide the silent loss of another, which a cap on the warning list or a
// swallowed transport error would do.
func TestRingSurvivesOneRelayDownAndSaysSoOnce(t *testing.T) {
	ring := mustRing(t)

	healthy := ring.outer.sessionList(t, nil)
	if len(healthy.Sessions) != 1 || len(healthy.Warnings) != 0 {
		t.Fatalf("a healthy ring should list the session once and quietly: %+v", healthy)
	}

	ring.middle.ts.Close()
	oneDown := ring.outer.sessionList(t, nil)
	if len(oneDown.Sessions) != 1 || strings.Join(oneDown.Sessions[0].NodePath, "/") != "shortcut/agent7" {
		t.Fatalf("the shortcut should still reach the agent: %+v", oneDown)
	}
	if len(oneDown.Warnings) != 1 || !strings.Contains(oneDown.Warnings[0], "middle") {
		t.Fatalf("one relay down should cost exactly one warning, naming it: %v", oneDown.Warnings)
	}

	ring.relay3.ts.Close()
	twoDown := ring.outer.sessionList(t, nil)
	if len(twoDown.Sessions) != 0 {
		t.Fatalf("with both routes down nothing can be listed: %+v", twoDown.Sessions)
	}
	if len(twoDown.Warnings) != 2 {
		t.Fatalf("two lost branches should raise two warnings, got %d: %v", len(twoDown.Warnings), twoDown.Warnings)
	}
	joined := strings.Join(twoDown.Warnings, "\n")
	for _, name := range []string{"middle", "shortcut"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("no warning names %q: %v", name, twoDown.Warnings)
		}
	}
}

// A request that has already crossed as many relays as the swarm carries is
// refused with a warning, because something out there cannot be reached and
// saying nothing would make it vanish. It is not "looped": that word is for a
// ring, where nothing is lost. A header one short of the budget is still served.
func TestAChainAtTheHopBudgetIsRefusedWithAWarningAndNotCalledLooped(t *testing.T) {
	relay := soloRelayWithOneAgent(t)

	served := relay.sessionList(t, map[string]string{SwarmPathHeader: hops(swarmMaxHops - 1)})
	if len(served.Sessions) != 1 || len(served.Warnings) != 0 || served.Looped {
		t.Fatalf("a chain one short of the budget should be served quietly: %+v", served)
	}

	refused := relay.sessionList(t, map[string]string{SwarmPathHeader: hops(swarmMaxHops)})
	if len(refused.Sessions) != 0 {
		t.Fatalf("a chain at the budget was served: %+v", refused.Sessions)
	}
	if refused.Looped {
		t.Fatal("a chain that is merely too long was reported as a closed ring")
	}
	if len(refused.Warnings) != 1 || !saysTooDeepAt(refused.Warnings[0], relay.name) {
		t.Fatalf("a chain at the budget should raise exactly one warning, saying it is too deep here: %v", refused.Warnings)
	}
}

// The same two rules hold for the topology walk, which has its own answer shape.
func TestTopologyOfAChainAtTheHopBudgetWarnsAndIsNotLooped(t *testing.T) {
	relay := soloRelayWithOneAgent(t)

	served := relay.topology(t, map[string]string{SwarmPathHeader: hops(swarmMaxHops - 1)})
	if len(served.Nodes) != 2 || len(served.Warnings) != 0 || served.Looped {
		t.Fatalf("a chain one short of the budget should be walked quietly: %+v", served)
	}

	refused := relay.topology(t, map[string]string{SwarmPathHeader: hops(swarmMaxHops)})
	if refused.Looped {
		t.Fatal("a chain that is merely too long was reported as a closed ring")
	}
	if len(refused.Nodes) != 0 {
		t.Fatalf("a chain at the budget was walked: %+v", refused.Nodes)
	}
	if len(refused.Warnings) != 1 || !saysTooDeepAt(refused.Warnings[0], relay.name) {
		t.Fatalf("a chain at the budget should raise exactly one warning, saying it is too deep here: %v", refused.Warnings)
	}
	if refused.Root.UUID != relay.srv.UUID() {
		t.Fatal("the answer still has to say who answered")
	}
}

// Coming back to a relay already in the walk is how a ring ends a branch. It is
// not a fault, so it carries no warning, but it still says who answered so the
// parent can record the edge.
func TestTopologyOfALoopedChainIsSilentAndStillNamesTheRelay(t *testing.T) {
	relay := soloRelayWithOneAgent(t)

	closed := relay.topology(t, map[string]string{SwarmPathHeader: relay.srv.UUID()})
	if !closed.Looped {
		t.Fatalf("a request that already passed here should be answered as looped: %+v", closed)
	}
	if len(closed.Warnings) != 0 {
		t.Fatalf("a closed ring raised warnings: %v", closed.Warnings)
	}
	if len(closed.Nodes) != 0 {
		t.Fatalf("a looped answer should carry no nodes: %+v", closed.Nodes)
	}
	if closed.Root.UUID != relay.srv.UUID() {
		t.Fatalf("a looped answer should still name its root, got %q", closed.Root.UUID)
	}
}

// The topology walk is held to the same count as the sessions walk: one relay
// down costs exactly one warning naming it while the rest of the swarm survives
// the other way round, and two relays down cost two.
func TestTopologyNamesAChildRelayThatDoesNotAnswer(t *testing.T) {
	ring := mustRing(t)
	ring.middle.ts.Close()

	oneDown := ring.outer.topology(t, nil)
	if len(oneDown.Warnings) != 1 || !strings.Contains(oneDown.Warnings[0], "middle") {
		t.Fatalf("a relay that does not answer should cost exactly one warning, naming it: %v", oneDown.Warnings)
	}
	var sawAgent bool
	for _, n := range oneDown.Nodes {
		if n.Name == "agent7" {
			sawAgent = true
		}
	}
	if !sawAgent {
		t.Fatalf("the rest of the swarm should survive one unreachable relay: %+v", oneDown.Nodes)
	}
	if !topologyHolds(oneDown, "middle") {
		t.Fatalf("a relay that does not answer is still known here and should stay on the map: %+v", oneDown.Nodes)
	}

	// A warning for one lost branch must not hide the loss of another.
	ring.relay3.ts.Close()
	twoDown := ring.outer.topology(t, nil)
	if len(twoDown.Warnings) != 2 {
		t.Fatalf("two relays that do not answer should cost two warnings, got %d: %v", len(twoDown.Warnings), twoDown.Warnings)
	}
	joined := strings.Join(twoDown.Warnings, "\n")
	for _, name := range []string{"middle", "shortcut"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("no warning names %q: %v", name, twoDown.Warnings)
		}
	}
}

// A node that registered for a tunnel but has not connected one yet is still
// inside its lease, so it is online and gets asked. The relay cannot carry a
// request to it, and both walks have to say so by name instead of dropping it,
// without costing the nodes that do answer. Two such nodes cost two warnings.
func TestANodeWithoutAConnectionIsNamedByBothWalksAndTheRestSurvives(t *testing.T) {
	relay := soloRelayWithOneAgent(t)

	agentReq := tunnelRequest("pending-agent")
	if _, err := relay.srv.registry.Register(agentReq); err != nil {
		t.Fatal(err)
	}
	relayReq := tunnelRequest("pending-relay")
	relayReq.Kind = swarmdto.KindRelay
	if _, err := relay.srv.registry.Register(relayReq); err != nil {
		t.Fatal(err)
	}

	sessions := relay.sessionList(t, nil)
	if len(sessions.Sessions) != 1 {
		t.Fatalf("the agent that answers should still be listed: %+v", sessions)
	}
	if len(sessions.Warnings) != 2 {
		t.Fatalf("two nodes without a connection should cost two warnings, got %d: %v", len(sessions.Warnings), sessions.Warnings)
	}
	joined := strings.Join(sessions.Warnings, "\n")
	for _, name := range []string{"pending-agent", "pending-relay"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("no warning names %q: %v", name, sessions.Warnings)
		}
	}

	topo := relay.topology(t, nil)
	if len(topo.Warnings) != 1 || !strings.Contains(topo.Warnings[0], "pending-relay") {
		t.Fatalf("the topology should name the relay it cannot walk, and only it: %v", topo.Warnings)
	}
	var sawAgent bool
	for _, n := range topo.Nodes {
		if n.Name == "agent1" {
			sawAgent = true
		}
	}
	if !sawAgent {
		t.Fatalf("the rest of the topology should survive one relay without a connection: %+v", topo.Nodes)
	}
	for _, name := range []string{"pending-agent", "pending-relay"} {
		if !topologyHolds(topo, name) {
			t.Fatalf("a node without a connection is still known here and should stay on the map: no %q in %+v", name, topo.Nodes)
		}
	}
}

// A second mutation pass, over the warnings that have to cross a hop, found
// faults the tests above let through: a parent that dropped the warnings of a
// child relay, kept only the first of them or lost the branch they came from,
// a topology that kept only its first warning, and a chain that the forged
// headers above never build for real. The tests below pin those behaviours.

// knowsRelayAt makes the relay hold a child relay at url, the way a join does,
// whatever answers there.
func (r *ringRelay) knowsRelayAt(name, url string) error {
	_, err := r.srv.registry.Register(swarmdto.RegisterRequest{
		Name: name, Kind: swarmdto.KindRelay, Transport: swarmdto.TransportDirect,
		AdvertiseURL: url, InstanceUUID: "uuid-" + name, Token: "tok-" + name,
	})
	return err
}

// deadURL is an address that nothing answers on any more.
func deadURL() string {
	gone := httptest.NewServer(http.NotFoundHandler())
	url := gone.URL
	gone.Close()
	return url
}

// saysTooDeepAt reports whether a warning is the hop budget running out at the
// named relay rather than some other fault.
func saysTooDeepAt(warning, relay string) bool {
	return strings.Contains(warning, "too deep") && strings.Contains(warning, relay)
}

// topologyHolds reports whether the topology has a node by that name.
func topologyHolds(topo Topology, name string) bool {
	for _, n := range topo.Nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}

// namedOnce reports whether name appears in exactly one of the warnings.
func namedOnce(warnings []string, name string) bool {
	n := 0
	for _, w := range warnings {
		if strings.Contains(w, name) {
			n++
		}
	}
	return n == 1
}

// A relay's answer carries the warnings of everything behind it, and the client
// of the outermost relay has to get every one of them, prefixed with the branch
// it came from: a node that is down two hops away is as down as one next door,
// and the prefix is what tells an operator which relay to look behind. The two
// walks shape this differently - the sessions list folds a child's warnings
// into one entry for that branch, the topology keeps one entry per warning -
// and each is pinned as it is.
func TestWarningsFromBehindAChildRelaySurviveTheHopAndNameTheirBranch(t *testing.T) {
	outer, err := newRingRelay("outer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(outer.ts.Close)
	inner, err := newRingRelay("inner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(inner.ts.Close)
	if err := outer.knows(inner, "inner"); err != nil {
		t.Fatal(err)
	}
	agent := newStubAgent()
	t.Cleanup(agent.ts.Close)
	agent.add("sess_near", "behind the child")
	if err := inner.holdsAgent("near", agent); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lost-a", "lost-b"} {
		if err := inner.knowsRelayAt(name, deadURL()); err != nil {
			t.Fatal(err)
		}
	}

	sessions := outer.sessionList(t, nil)
	if len(sessions.Sessions) != 1 || strings.Join(sessions.Sessions[0].NodePath, "/") != "inner/near" {
		t.Fatalf("the agent that answers should still be listed through the child: %+v", sessions.Sessions)
	}
	if len(sessions.Warnings) != 1 || !strings.HasPrefix(sessions.Warnings[0], "inner") {
		t.Fatalf("the child's warnings should arrive as one entry for its branch, named first: %v", sessions.Warnings)
	}
	for _, name := range []string{"lost-a", "lost-b"} {
		if !strings.Contains(sessions.Warnings[0], name) {
			t.Fatalf("a warning from behind the child was lost on the way: no %q in %v", name, sessions.Warnings)
		}
	}

	topo := outer.topology(t, nil)
	if len(topo.Warnings) != 2 {
		t.Fatalf("the child's two warnings should both survive the hop, got %d: %v", len(topo.Warnings), topo.Warnings)
	}
	for _, w := range topo.Warnings {
		if !strings.HasPrefix(w, "inner") {
			t.Fatalf("a warning from behind the child does not name its branch: %v", topo.Warnings)
		}
	}
	for _, name := range []string{"lost-a", "lost-b"} {
		if !namedOnce(topo.Warnings, name) {
			t.Fatalf("every relay the child cannot walk should be named once: no single %q in %v", name, topo.Warnings)
		}
	}
	if !topologyHolds(topo, "near") {
		t.Fatalf("the rest of the child's branch should survive: %+v", topo.Nodes)
	}
}

// lineOfRelays builds n relays, each joined by the one before it under its own
// name: line0 -> line1 -> ... -> line(n-1).
func lineOfRelays(t *testing.T, n int) []*ringRelay {
	t.Helper()
	relays := make([]*ringRelay, n)
	for i := range relays {
		relay, err := newRingRelay(fmt.Sprintf("line%d", i))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(relay.ts.Close)
		relays[i] = relay
		if i > 0 {
			if err := relays[i-1].knows(relay, relay.name); err != nil {
				t.Fatal(err)
			}
		}
	}
	return relays
}

// namesTheWay reports whether a warning names every relay of way, in order.
func namesTheWay(warning string, way []string) bool {
	rest := warning
	for _, hop := range way {
		i := strings.Index(rest, hop)
		if i < 0 {
			return false
		}
		rest = rest[i+len(hop):]
	}
	return true
}

// A line of relays longer than the hop budget is cut where the budget runs out.
// The hop-budget tests above forge the header for one relay; here the path is
// built for real, so every relay on the way has to extend it and pass it on, or
// the far end is reached after all. What lies inside the budget is still served,
// and the cut comes back as a single warning that says the chain is too deep
// and names the way to it, so "something out there cannot be reached" also
// says where.
func TestALineOfRelaysLongerThanTheBudgetIsCutWithOneWarningNamingTheWay(t *testing.T) {
	relays := lineOfRelays(t, swarmMaxHops+1)
	nearAgent, farAgent := newStubAgent(), newStubAgent()
	t.Cleanup(nearAgent.ts.Close)
	t.Cleanup(farAgent.ts.Close)
	nearAgent.add("sess_near", "the last relay inside the budget")
	farAgent.add("sess_far", "the first relay past it")
	if err := relays[swarmMaxHops-1].holdsAgent("near", nearAgent); err != nil {
		t.Fatal(err)
	}
	if err := relays[swarmMaxHops].holdsAgent("far", farAgent); err != nil {
		t.Fatal(err)
	}

	var way []string
	for _, relay := range relays[1:] {
		way = append(way, relay.name)
	}
	cutAt := relays[swarmMaxHops].name
	nearPath := strings.Join(way[:len(way)-1], "/") + "/near"

	sessions := relays[0].sessionList(t, nil)
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != "sess_near" {
		t.Fatalf("only the session inside the budget should be listed: %+v", sessions.Sessions)
	}
	if got := strings.Join(sessions.Sessions[0].NodePath, "/"); got != nearPath {
		t.Fatalf("the session inside the budget has path %q, want %q", got, nearPath)
	}
	if sessions.Looped {
		t.Fatal("a line of relays was reported as a closed ring")
	}
	if len(sessions.Warnings) != 1 || !saysTooDeepAt(sessions.Warnings[0], cutAt) || !namesTheWay(sessions.Warnings[0], way) {
		t.Fatalf("the cut should come back as one warning naming the way to it: %v", sessions.Warnings)
	}

	topo := relays[0].topology(t, nil)
	if topo.Looped {
		t.Fatal("a line of relays was reported as a closed ring")
	}
	if len(topo.Warnings) != 1 || !saysTooDeepAt(topo.Warnings[0], cutAt) || !namesTheWay(topo.Warnings[0], way) {
		t.Fatalf("the cut should come back as one warning naming the way to it: %v", topo.Warnings)
	}
	byName := map[string]TopologyNode{}
	for _, n := range topo.Nodes {
		byName[n.Name] = n
	}
	// The relay that cut the walk still answered, so it is on the map; what it
	// holds is not.
	for _, relay := range relays {
		if _, ok := byName[relay.name]; !ok {
			t.Fatalf("relay %q is missing from the topology: %+v", relay.name, topo.Nodes)
		}
	}
	if _, ok := byName["far"]; ok {
		t.Fatalf("a node past the budget was walked: %+v", topo.Nodes)
	}
	near, ok := byName["near"]
	if !ok {
		t.Fatalf("the agent inside the budget is missing from the topology: %+v", topo.Nodes)
	}
	if got := strings.Join(topo.Routes[near.UUID].Path, "/"); got != nearPath {
		t.Fatalf("the route to the agent inside the budget is %q, want %q", got, nearPath)
	}
}
