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
	if len(refused.Warnings) != 1 {
		t.Fatalf("a chain at the budget should raise exactly one warning: %v", refused.Warnings)
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
	if len(refused.Warnings) != 1 {
		t.Fatalf("a chain at the budget should raise exactly one warning: %v", refused.Warnings)
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

func TestTopologyNamesAChildRelayThatDoesNotAnswer(t *testing.T) {
	ring := mustRing(t)
	ring.middle.ts.Close()

	topo := ring.outer.topology(t, nil)
	var named bool
	for _, w := range topo.Warnings {
		if strings.Contains(w, "middle") {
			named = true
		}
	}
	if !named {
		t.Fatalf("a relay that does not answer vanished from the topology without a word: %v", topo.Warnings)
	}
	var sawAgent bool
	for _, n := range topo.Nodes {
		if n.Name == "agent7" {
			sawAgent = true
		}
	}
	if !sawAgent {
		t.Fatalf("the rest of the swarm should survive one unreachable relay: %+v", topo.Nodes)
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
}
