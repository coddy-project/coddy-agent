//go:build swarm

package swarm

// The ping of the application probe through a relay (docs/plans/remote-model-provider-probe.md, 2): the closed table of D5 gains one
// exact entry, POST /coddy/llm/alive; the ping spends no slot and no token and is not counted as a call.

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type pingNode struct {
	ts   *httptest.Server
	mu   sync.Mutex
	seen []string
}

func newPingNode(t *testing.T) *pingNode {
	t.Helper()
	n := &pingNode{}
	n.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		n.seen = append(n.seen, r.Method+" "+r.URL.Path+" id="+r.Header.Get("X-Coddy-Probe-Id")+" auth="+r.Header.Get("Authorization"))
		n.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(n.ts.Close)
	return n
}

func (n *pingNode) requests() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.seen...)
}

func doPing(t *testing.T, base, path, method, token string) int {
	t.Helper()
	req, err := http.NewRequest(method, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Coddy-Probe-Id", "0123456789abcdef0123456789abcdef")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

const pingRoute = "/swarm/nodes/nas02/coddy/llm/alive"

func pingRelay(t *testing.T, node *pingNode, clients ...config.SwarmClient) (*Server, *httptest.Server) {
	t.Helper()
	rn := &recordingNode{ts: node.ts}
	return scopedRelay(t, rn, clients...)
}

func TestTheScopedTableCarriesThePingAndNothingNearIt(t *testing.T) {
	node := newPingNode(t)
	_, ts := pingRelay(t, node, acme("nas02"))
	if got := doPing(t, ts.URL, pingRoute, http.MethodPost, scopedToken); got != http.StatusNoContent {
		t.Fatalf("a scoped client's ping of a listed node: %d", got)
	}
	seen := node.requests()
	if len(seen) != 1 || seen[0] != "POST /coddy/llm/alive id=0123456789abcdef0123456789abcdef auth=Bearer node-secret" {
		t.Fatalf("the node saw %v: the id header passes through and the credential is the node's own", seen)
	}
	for name, c := range map[string]struct{ path, method string }{
		"GET":              {pingRoute, http.MethodGet},
		"HEAD":             {pingRoute, http.MethodHead},
		"trailing slash":   {pingRoute + "/", http.MethodPost},
		"matrix param":     {pingRoute + ";x=1", http.MethodPost},
		"a sibling route":  {"/swarm/nodes/nas02/coddy/llm/alive2", http.MethodPost},
		"an unlisted node": {"/swarm/nodes/other/coddy/llm/alive", http.MethodPost},
		"an old path form": {"/swarm/nodes/nas02/coddy/llm/calls/0123456789abcdef0123456789abcdef/alive", http.MethodPost},
	} {
		if got := doPing(t, ts.URL, c.path, c.method, scopedToken); got != http.StatusNotFound {
			t.Errorf("%s: %d, want the answer of an unknown route", name, got)
		}
	}
	if n := len(node.requests()); n != 1 {
		t.Errorf("a refused ping reached the node: %d requests", n)
	}
}

func TestTheFullClassPingsToo(t *testing.T) {
	node := newPingNode(t)
	_, ts := pingRelay(t, node, acme("nas02"))
	if got := doPing(t, ts.URL, pingRoute, http.MethodPost, fullToken); got != http.StatusNoContent {
		t.Fatalf("%d", got)
	}
}

// A ping is not a call: it takes no slot, spends no token and leaves no row of the counters.
func TestAPingSpendsNothingAndIsNotCounted(t *testing.T) {
	node := newPingNode(t)
	// One slot and a window of one call a minute: a call would be refused at once after the first.
	client := limited("acme", scopedToken, 1, 1, 1)
	srv, ts := pingRelay(t, node, client)
	for i := 0; i < 5; i++ {
		if got := doPing(t, ts.URL, pingRoute, http.MethodPost, scopedToken); got != http.StatusNoContent {
			t.Fatalf("ping %d: %d: a ping must not be held to the client's slots or window", i, got)
		}
	}
	if srv.limits.inUse("acme") != 0 {
		t.Error("a ping took a slot")
	}
	_, rows := relayStats(t, ts.URL)
	if len(rows) != 0 {
		t.Errorf("pings left rows in the counters: %+v", rows)
	}
	// A token the gate refuses on the ping route is not counted either: the counters are of calls.
	if got := doPing(t, ts.URL, pingRoute, http.MethodPost, "made-up-token"); got != http.StatusUnauthorized {
		t.Errorf("an unknown token: %d", got)
	}
	if _, rows = relayStats(t, ts.URL); len(rows) != 0 {
		t.Errorf("a refused ping was counted: %+v", rows)
	}
	// The window is still whole: the client's one call goes through.
	if got := doPing(t, ts.URL, "/swarm/nodes/nas02/coddy/llm/completions", http.MethodPost, scopedToken); got == http.StatusTooManyRequests {
		t.Errorf("the pings spent the window: %d", got)
	}
}

// A client at its limit is still pinged: with its only slot held by a real call and its window spent by it, the ping passes and the
// next call is refused for the reason it would have been without the ping.
func TestAPingPassesAClientWhoseSlotAndWindowAreSpent(t *testing.T) {
	g := newGateNode(t)
	srv, ts := limitRelay(t, g, nil, limited("acme", scopedToken, 1, 1, 1))
	g.releaseOnCleanup(t)
	first := make(chan struct{})
	go func() { defer close(first); post(t, ts.URL+relayCompletions, scopedToken) }()
	<-g.entered
	if srv.limits.inUse("acme") != 1 {
		t.Fatal("the call holds no slot")
	}
	if res, _ := post(t, ts.URL+relayCompletions, scopedToken); res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("a second call: %d, want 429", res.StatusCode)
	}
	if got := doPing(t, ts.URL, pingRoute, http.MethodPost, scopedToken); got != http.StatusOK && got != http.StatusNoContent {
		t.Errorf("a ping of a client at its limit: %d", got)
	}
	close(g.hold)
	<-first
}
