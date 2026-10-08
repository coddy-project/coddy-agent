//go:build swarm

package swarm

// The scoped clients of a relay (docs/plans/remote-model-provider-phase3.md, 3.2 and 3.5; the verdicts of D1 and D5 in
// docs/plans/remote-model-provider-models/): a client with a token of its own reaches the three shared-model routes of the nodes
// its entry lists, as exact hop paths, and nothing else of the relay.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

const (
	scopedToken = "acme-secret"
	fullToken   = "full-secret"
)

// recordingNode is a stand-in node that records every request it receives.
type recordingNode struct {
	mu   sync.Mutex
	seen []string // "<METHOD> <path> auth=<Authorization>"
	ts   *httptest.Server
}

func newRecordingNode(t *testing.T) *recordingNode {
	t.Helper()
	n := &recordingNode{}
	n.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		n.seen = append(n.seen, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization")+" q="+r.URL.RawQuery)
		n.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "node answer")
	}))
	t.Cleanup(n.ts.Close)
	return n
}

func (n *recordingNode) requests() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.seen...)
}

func (n *recordingNode) reset() {
	n.mu.Lock()
	n.seen = nil
	n.mu.Unlock()
}

// scopedRelay is a relay with a full token, the given scoped entries and one node, "nas02".
func scopedRelay(t *testing.T, node *recordingNode, clients ...config.SwarmClient) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = fullToken
	cfg.Swarm.PairingTokens = []string{"pair-secret"}
	cfg.Swarm.Clients = clients
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	registerRecording(t, srv, "nas02", node)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func registerRecording(t *testing.T, srv *Server, name string, node *recordingNode) {
	t.Helper()
	if _, err := srv.registry.Register(swarmdto.RegisterRequest{
		Name: name, Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: node.ts.URL, InstanceUUID: "uuid-" + name, Token: "node-secret",
	}); err != nil {
		t.Fatal(err)
	}
}

func acme(nodes ...string) config.SwarmClient {
	return acmeNamed("acme", scopedToken, nodes...)
}

func acmeNamed(name, token string, nodes ...string) config.SwarmClient {
	return config.SwarmClient{Name: name, Token: token, Scope: config.ScopeSharedModels, Nodes: nodes}
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func do(t *testing.T, method, url, token string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func TestEntryAdmitsExactHopPaths(t *testing.T) {
	cases := []struct {
		nodes []string
		hops  []string
		want  bool
	}{
		{[]string{"a"}, []string{"a"}, true},
		{[]string{"a"}, []string{"a", "b"}, false}, // a bare name admits the single-hop path only
		{[]string{"a"}, []string{"b"}, false},
		{[]string{"a"}, []string{"A"}, false}, // names are case-sensitive
		{[]string{"a/b"}, []string{"a", "b"}, true},
		{[]string{"a/b"}, []string{"a"}, false},
		{[]string{"a/b"}, []string{"a", "b", "c"}, false}, // not a longer chain that starts with the entry
		{[]string{"a/b"}, []string{"b"}, false},           // not a node of the same name at another place
		{[]string{"*"}, []string{"anything"}, true},
		{[]string{"*"}, []string{"a", "b"}, false}, // `*` is any single-hop path
		{[]string{"x", "a/b"}, []string{"a", "b"}, true},
		{nil, []string{"a"}, false},
	}
	for _, tc := range cases {
		got := clientAdmitsHops(config.SwarmClient{Nodes: tc.nodes}, tc.hops)
		if got != tc.want {
			t.Errorf("entry %v, hops %v: admitted = %v, want %v", tc.nodes, tc.hops, got, tc.want)
		}
	}
}

func TestSplitHopsReadsTheDecodedChain(t *testing.T) {
	cases := []struct {
		name, rest string
		hops       []string
		route      string
	}{
		{"nas02", "/coddy/llm/models", []string{"nas02"}, "/coddy/llm/models"},
		{"edge", "/swarm/nodes/gpu/coddy/llm/completions", []string{"edge", "gpu"}, "/coddy/llm/completions"},
		{"a", "/swarm/nodes/b/swarm/nodes/c/coddy/llm/models", []string{"a", "b", "c"}, "/coddy/llm/models"},
		{"nas02", "/coddy/llm/%63ompletions", []string{"nas02"}, "/coddy/llm/completions"},
		{"nas02", "/", []string{"nas02"}, "/"},
		{"edge", "/swarm/nodes/gpu", []string{"edge", "gpu"}, ""},
	}
	for _, tc := range cases {
		hops, route, ok := splitHops(tc.name, tc.rest)
		if !ok || strings.Join(hops, "/") != strings.Join(tc.hops, "/") || route != tc.route {
			t.Errorf("splitHops(%q, %q) = %v %q %v, want %v %q", tc.name, tc.rest, hops, route, ok, tc.hops, tc.route)
		}
	}
	if _, _, ok := splitHops("a", "/swarm/nodes/%zz/x"); ok {
		t.Error("an undecodable remainder must not be read")
	}
}

func TestSharedRouteIsAClosedExactTable(t *testing.T) {
	yes := [][2]string{
		{http.MethodGet, "/coddy/llm/models"},
		{http.MethodGet, "/coddy/llm/models/coder/usage"},
		{http.MethodGet, "/coddy/llm/models/gpt-5.5/usage"},
		{http.MethodPost, "/coddy/llm/completions"},
	}
	for _, c := range yes {
		if !sharedRoute(c[0], c[1]) {
			t.Errorf("%s %s must be in the table", c[0], c[1])
		}
	}
	no := [][2]string{
		{http.MethodHead, "/coddy/llm/models"}, {http.MethodOptions, "/coddy/llm/completions"},
		{http.MethodPost, "/coddy/llm/models"}, {http.MethodGet, "/coddy/llm/completions"},
		{http.MethodPost, "/coddy/llm/completions/"}, {http.MethodPost, "/coddy/llm/completions;x=1"},
		{http.MethodGet, "/coddy/llm/models/"}, {http.MethodGet, "/coddy/llm/models/coder/usage/"},
		{http.MethodGet, "/coddy/llm/models/a b/usage"}, {http.MethodGet, "/coddy/llm/models//usage"},
		{http.MethodGet, "/coddy/llm/models/a/b/usage"}, {http.MethodPost, "/coddy/llm/embeddings"},
		{http.MethodGet, "/coddy/llm-admin"}, {http.MethodGet, "/coddy/config"}, {http.MethodGet, "/coddy/sessions"},
		{http.MethodGet, "/v1/models"}, {http.MethodPost, "//coddy/llm/completions"}, {http.MethodPost, ""},
	}
	for _, c := range no {
		if sharedRoute(c[0], c[1]) {
			t.Errorf("%s %q must not be in the table", c[0], c[1])
		}
	}
}

// A scoped client passes the gate on the mount only; every other route of the relay gives it the plain 401 an unknown token gets.
func TestScopedClientPassesTheGateOnTheMountOnly(t *testing.T) {
	node := newRecordingNode(t)
	_, ts := scopedRelay(t, node, acme("nas02"))

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/swarm/nodes"},
		{http.MethodGet, "/swarm/sessions"},
		{http.MethodGet, "/swarm/topology"},
		{http.MethodDelete, "/swarm/nodes/nas02"},
	} {
		res, body := do(t, c.method, ts.URL+c.path, scopedToken)
		unknown, unknownBody := do(t, c.method, ts.URL+c.path, "made-up-token")
		if res.StatusCode != http.StatusUnauthorized || res.StatusCode != unknown.StatusCode || body != unknownBody {
			t.Errorf("%s %s: scoped %d %q, unknown token %d %q: want the same plain 401", c.method, c.path, res.StatusCode, body, unknown.StatusCode, unknownBody)
		}
	}
	// The discovery probe stays public.
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/info", ""); res.StatusCode != http.StatusOK {
		t.Errorf("GET /swarm/info = %d", res.StatusCode)
	}
	// The full class is what it was.
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes", fullToken); res.StatusCode != http.StatusOK {
		t.Errorf("the full token on /swarm/nodes = %d", res.StatusCode)
	}
	// The mount is open to the scoped client, on the three routes.
	res, body := do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/llm/models", scopedToken)
	if res.StatusCode != http.StatusOK || body != "node answer" {
		t.Errorf("the mount for the scoped client = %d %q", res.StatusCode, body)
	}
}

func TestScopedClientRouteTable(t *testing.T) {
	node := newRecordingNode(t)
	_, ts := scopedRelay(t, node, acme("nas02"))
	cases := []struct {
		method, path string
		reached      bool
	}{
		{http.MethodGet, "/swarm/nodes/nas02/coddy/llm/models", true},
		{http.MethodGet, "/swarm/nodes/nas02/coddy/llm/models/coder/usage", true},
		{http.MethodPost, "/swarm/nodes/nas02/coddy/llm/completions", true},
		{http.MethodPost, "/swarm/nodes/nas02/coddy/llm/%63ompletions", true}, // the node routes the decoded path
		{http.MethodHead, "/swarm/nodes/nas02/coddy/llm/models", false},
		{http.MethodOptions, "/swarm/nodes/nas02/coddy/llm/completions", false},
		{http.MethodPost, "/swarm/nodes/nas02/coddy/llm/models", false},
		{http.MethodGet, "/swarm/nodes/nas02/coddy/llm/completions", false},
		{http.MethodPost, "/swarm/nodes/nas02/coddy/llm/completions/", false},
		{http.MethodPost, "/swarm/nodes/nas02/coddy/llm/completions;x=1", false},
		{http.MethodPost, "/swarm/nodes/nas02//coddy/llm/completions", false},
		{http.MethodGet, "/swarm/nodes/nas02/coddy/llm/models/a%20b/usage", false},
		{http.MethodPost, "/swarm/nodes/nas02/coddy/llm/embeddings", false}, // a route a later release adds under /coddy/llm/
		{http.MethodGet, "/swarm/nodes/nas02/coddy/config", false},
		{http.MethodGet, "/swarm/nodes/nas02/coddy/sessions", false},
		{http.MethodGet, "/swarm/nodes/nas02/v1/models", false},
		{http.MethodGet, "/swarm/nodes/nas02/swarm/nodes", false},
		{http.MethodGet, "/swarm/nodes/nas02/%2e%2e/%2e%2e/swarm/nodes", false},
		{http.MethodGet, "/swarm/nodes/nas02/coddy/llm%2fmodels", false},
		{http.MethodGet, "/swarm/nodes/nas02/swarm/nodes/x/coddy/llm/models", false},                                           // a second hop the entry does not write
		{http.MethodGet, "/swarm/nodes/nas02/swarm/nodes/a/swarm/nodes/b/swarm/nodes/c/swarm/nodes/d/coddy/llm/models", false}, // five hops
	}
	for _, tc := range cases {
		node.reset()
		res, _ := do(t, tc.method, ts.URL+tc.path, scopedToken)
		got := len(node.requests()) > 0
		if got != tc.reached {
			t.Errorf("%s %s: reached the node = %v (status %d), want %v", tc.method, tc.path, got, res.StatusCode, tc.reached)
		}
		if tc.reached && res.StatusCode != http.StatusOK {
			t.Errorf("%s %s: status %d", tc.method, tc.path, res.StatusCode)
		}
	}
}

// What the relay says to a scoped client about a node outside its list is what it says about a node that does not exist.
func TestScopedClientCannotTellAnUnlistedNodeFromAnUnknownOne(t *testing.T) {
	node := newRecordingNode(t)
	srv, ts := scopedRelay(t, node, acme("nas02"))
	other := newRecordingNode(t)
	registerRecording(t, srv, "other", other)

	_, unlisted := do(t, http.MethodGet, ts.URL+"/swarm/nodes/other/coddy/llm/models", scopedToken)
	resU, unknown := do(t, http.MethodGet, ts.URL+"/swarm/nodes/ghost/coddy/llm/models", scopedToken)
	if resU.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown node = %d", resU.StatusCode)
	}
	if strings.ReplaceAll(unlisted, "other", "X") != strings.ReplaceAll(unknown, "ghost", "X") {
		t.Errorf("an unlisted node answers %q, an unknown one %q", unlisted, unknown)
	}
	if len(other.requests()) != 0 {
		t.Error("the unlisted node was reached")
	}
	// A wrong route on a listed node and on an unlisted one are not told apart by the order of the checks either.
	_, wrongRouteUnlisted := do(t, http.MethodGet, ts.URL+"/swarm/nodes/other/coddy/config", scopedToken)
	if !strings.Contains(wrongRouteUnlisted, "no such node") {
		t.Errorf("the allowlist is judged before the route: got %q", wrongRouteUnlisted)
	}
}

func TestScopedTokenNeverReachesTheNodeOrWorksAsACapability(t *testing.T) {
	node := newRecordingNode(t)
	_, ts := scopedRelay(t, node, acme("nas02"))

	res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/llm/models", scopedToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	for _, line := range node.requests() {
		if strings.Contains(line, scopedToken) || strings.Contains(line, fullToken) {
			t.Fatalf("a client credential reached the node: %s", line)
		}
		if !strings.Contains(line, "auth=Bearer node-secret") {
			t.Fatalf("the node saw %s, want its own token", line)
		}
	}

	// The scoped token as an access_token on a workspace media route is not a node capability: refused, never forwarded.
	node.reset()
	res, _ = do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/sessions/s1/workspace/raw?access_token="+scopedToken, "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("the scoped token as a media capability = %d, want 401", res.StatusCode)
	}
	if len(node.requests()) != 0 {
		t.Errorf("the scoped token was forwarded as a capability: %v", node.requests())
	}
	// A real capability (not one of the relay's tokens) still passes to the node, as before.
	res, _ = do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/sessions/s1/workspace/raw?access_token=node-signed-capability", "")
	if res.StatusCode != http.StatusOK {
		t.Errorf("a node capability = %d, want it carried", res.StatusCode)
	}
}

func TestQueryTokenIsNotAScopedCredentialExceptOnEventStreams(t *testing.T) {
	node := newRecordingNode(t)
	_, ts := scopedRelay(t, node, acme("nas02"))
	res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/llm/models?access_token="+scopedToken, "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a scoped token in the query on an ordinary route = %d, want 401", res.StatusCode)
	}
	// On an event route the query token resolves to the scoped principal, and the route table refuses the route.
	res, _ = do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/sessions/s1/composer-stream?access_token="+scopedToken, "")
	if res.StatusCode != http.StatusNotFound || len(node.requests()) != 0 {
		t.Errorf("a stream route for a scoped client = %d (node reached: %v)", res.StatusCode, node.requests())
	}
}

// A class that matches both lists is the narrower one.
func TestATokenOfBothClassesIsScoped(t *testing.T) {
	node := newRecordingNode(t)
	both := acme("nas02")
	both.Token = fullToken
	_, ts := scopedRelay(t, node, both)
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes", fullToken); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a token in both lists opened the relay's own routes: %d", res.StatusCode)
	}
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/llm/models", fullToken); res.StatusCode != http.StatusOK {
		t.Errorf("a token in both lists is refused on the mount: %d", res.StatusCode)
	}
}

// With no scoped entry the relay behaves as it did: the full token opens everything, no token is refused, a relay with no token is open.
func TestARelayWithoutScopedClientsIsUnchanged(t *testing.T) {
	node := newRecordingNode(t)
	_, ts := scopedRelay(t, node)
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes", fullToken); res.StatusCode != http.StatusOK {
		t.Errorf("full token = %d", res.StatusCode)
	}
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token = %d", res.StatusCode)
	}
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/sessions", fullToken); res.StatusCode != http.StatusOK {
		t.Errorf("the full token on a node route = %d", res.StatusCode)
	}

	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	open, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ots := httptest.NewServer(open.Handler())
	defer ots.Close()
	if res, _ := do(t, http.MethodGet, ots.URL+"/swarm/nodes", ""); res.StatusCode != http.StatusOK {
		t.Errorf("a relay with no credential at all is open as before: %d", res.StatusCode)
	}
}

// A relay that has scoped entries and no full token is not open: only the scoped clients reach it, and only the mount.
func TestScopedEntriesWithoutAFullTokenDoNotOpenTheRelay(t *testing.T) {
	node := newRecordingNode(t)
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.Clients = []config.SwarmClient{acme("nas02")}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	registerRecording(t, srv, "nas02", node)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no credential on a relay with scoped entries = %d, want 401", res.StatusCode)
	}
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/llm/models", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no credential on the mount = %d, want 401", res.StatusCode)
	}
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/llm/models", scopedToken); res.StatusCode != http.StatusOK {
		t.Errorf("the scoped token on the mount = %d", res.StatusCode)
	}
}

// A chain: the first relay judges every hop, whatever the child relay (which is dialled with its own full token) would let through.
func TestTheFirstRelayJudgesTheWholeChain(t *testing.T) {
	leaf := newRecordingNode(t)

	// The child relay "edge" holds the node "gpu".
	childCfg := &config.Config{}
	childCfg.Swarm.Host = "127.0.0.1"
	childCfg.Swarm.AuthToken = "child-full"
	child, err := New(childCfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	registerRecording(t, child, "gpu", leaf)
	childTS := httptest.NewServer(child.Handler())
	defer childTS.Close()

	// The parent relay knows the child as a relay node and presents the child's full token.
	parentCfg := &config.Config{}
	parentCfg.Swarm.Host = "127.0.0.1"
	parentCfg.Swarm.AuthToken = fullToken
	parentCfg.Swarm.Clients = []config.SwarmClient{
		acme("edge/gpu"),
		{Name: "wide", Token: "wide-secret", Scope: config.ScopeSharedModels, Nodes: []string{"*"}},
		{Name: "edgeonly", Token: "edge-secret", Scope: config.ScopeSharedModels, Nodes: []string{"edge"}},
	}
	parent, err := New(parentCfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parent.registry.Register(swarmdto.RegisterRequest{
		Name: "edge", Kind: swarmdto.KindRelay, Transport: swarmdto.TransportDirect,
		AdvertiseURL: childTS.URL, InstanceUUID: "uuid-edge", Token: "child-full",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(parent.Handler())
	defer ts.Close()

	reached := func(token, method, path string) bool {
		leaf.reset()
		do(t, method, ts.URL+path, token)
		return len(leaf.requests()) > 0
	}
	const completions = "/swarm/nodes/edge/swarm/nodes/gpu/coddy/llm/completions"
	if !reached(scopedToken, http.MethodPost, completions) {
		t.Error("the exact path edge/gpu must reach the node behind the child")
	}
	// Outside the three routes, behind the child, with the child's full token on the second hop.
	for _, path := range []string{
		"/swarm/nodes/edge/swarm/nodes/gpu/coddy/sessions",
		"/swarm/nodes/edge/swarm/nodes/gpu/v1/models",
		"/swarm/nodes/edge/swarm/nodes/gpu/coddy/config",
	} {
		if reached(scopedToken, http.MethodGet, path) {
			t.Errorf("a scoped client reached %s through the chain", path)
		}
	}
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes/edge/swarm/sessions", scopedToken); res.StatusCode == http.StatusOK {
		t.Error("a scoped client read the child relay's sessions through the chain")
	}
	// `*` is a single hop: it does not reach through a relay.
	if reached("wide-secret", http.MethodPost, completions) {
		t.Error("`*` admitted a two-hop path")
	}
	// A bare `edge` admits the single hop `[edge]` and not `[edge, gpu]`.
	if reached("edge-secret", http.MethodPost, completions) {
		t.Error("an entry `edge` admitted the chain edge/gpu")
	}
	// The full class is judged by the legacy rule, as before.
	if !reached(fullToken, http.MethodGet, "/swarm/nodes/edge/swarm/nodes/gpu/coddy/sessions") {
		t.Error("the full token no longer reaches through a chain")
	}
}

// A node that dialled out is reached through its tunnel exactly like a reachable one.
func TestScopedClientReachesATunnelNode(t *testing.T) {
	stand := newTunnelStand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "tunnel "+r.Method+" "+r.URL.Path)
	}))
	stand.srv.SetClients([]config.SwarmClient{acme("inner")})
	res, body := do(t, http.MethodGet, stand.relay.URL+"/swarm/nodes/inner/coddy/llm/models", scopedToken)
	if res.StatusCode != http.StatusOK || body != "tunnel GET /coddy/llm/models" {
		t.Fatalf("through the tunnel: %d %q", res.StatusCode, body)
	}
	res, _ = do(t, http.MethodGet, stand.relay.URL+"/swarm/nodes/inner/coddy/sessions", scopedToken)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("a route outside the table through the tunnel = %d", res.StatusCode)
	}
}

func registerRequestFor(name, url string) swarmdto.RegisterRequest {
	return swarmdto.RegisterRequest{
		Name: name, Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: url, InstanceUUID: "uuid-" + name, Token: "node-secret",
	}
}

func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
