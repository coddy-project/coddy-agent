//go:build swarm

package swarm

// Godog harness for features/swarm_client_scopes.feature. The relay is a real relay served by httptest, the nodes are stand-ins
// that record what reaches them.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

type scopesState struct {
	gate   *gateNode
	second struct {
		status int
		body   string
		header http.Header
	}
	t       *testing.T
	nodes   map[string]*recordingNode
	srv     *Server
	url     string
	last    map[string]int // status of the last answers by key
	bodies  map[string]string
	order   []string
	closers []func()
}

func (s *scopesState) reset() {
	for _, c := range s.closers {
		c()
	}
	s.closers, s.nodes, s.last, s.bodies, s.order = nil, map[string]*recordingNode{}, map[string]int{}, map[string]string{}, nil
}

func (s *scopesState) aRelay(full, name, token, node string) error {
	s.reset()
	rn := newRecordingNode(s.t)
	s.nodes[node] = rn
	srv, ts := scopedRelay(s.t, rn, acmeNamed(name, token, node))
	_ = full // scopedRelay holds the full token "full-secret"
	s.srv, s.url = srv, ts.URL
	return nil
}

func (s *scopesState) answersEveryRoute(node string) error { return nil } // every recording node does

func (s *scopesState) call(method, path, token, key string) {
	res, body := do(s.t, method, s.url+path, token)
	s.last[key], s.bodies[key] = res.StatusCode, body
	s.order = append(s.order, key)
}

func (s *scopesState) listModels(node string) error {
	s.call(http.MethodGet, "/swarm/nodes/"+node+"/coddy/llm/models", scopedToken, "models:"+node)
	return nil
}

func (s *scopesState) callCompletions(node string) error {
	s.call(http.MethodPost, "/swarm/nodes/"+node+"/coddy/llm/completions", scopedToken, "completions:"+node)
	return nil
}

func (s *scopesState) bothReached() error {
	for _, k := range []string{"models:nas02", "completions:nas02"} {
		if s.last[k] != http.StatusOK {
			return fmt.Errorf("%s answered %d", k, s.last[k])
		}
	}
	if n := len(s.nodes["nas02"].requests()); n != 2 {
		return fmt.Errorf("the node saw %d requests, want 2", n)
	}
	return nil
}

func (s *scopesState) nodeSawItsOwnCredential() error {
	for _, line := range s.nodes["nas02"].requests() {
		if strings.Contains(line, scopedToken) || strings.Contains(line, "full-secret") {
			return fmt.Errorf("a client credential reached the node: %s", line)
		}
		if !strings.Contains(line, "auth=Bearer node-secret") {
			return fmt.Errorf("the node saw %q", line)
		}
	}
	return nil
}

func (s *scopesState) tunnelNode(node string) error {
	stand := newTunnelStand(s.t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("tunnel node answer"))
	}))
	stand.srv.SetClients(append(stand.srv.scopedClients(), acmeNamed("acme", scopedToken, node)))
	s.srv, s.url = stand.srv, stand.relay.URL
	s.closers = append(s.closers, stand.stop)
	return nil
}

func (s *scopesState) reachedTunnelNode(node string) error {
	if s.last["models:"+node] != http.StatusOK || !strings.Contains(s.bodies["models:"+node], "tunnel node answer") {
		return fmt.Errorf("the tunnel node was not reached: %d %q", s.last["models:"+node], s.bodies["models:"+node])
	}
	return nil
}

func (s *scopesState) readNodeList() error {
	s.call(http.MethodGet, "/swarm/nodes", scopedToken, "nodes")
	return nil
}

func (s *scopesState) plain401() error {
	res, body := do(s.t, http.MethodGet, s.url+"/swarm/nodes", "made-up-token")
	if s.last["nodes"] != http.StatusUnauthorized || s.last["nodes"] != res.StatusCode || s.bodies["nodes"] != body {
		return fmt.Errorf("scoped %d %q, unknown token %d %q", s.last["nodes"], s.bodies["nodes"], res.StatusCode, body)
	}
	return nil
}

func (s *scopesState) otherRegistered(node string) error {
	rn := newRecordingNode(s.t)
	s.nodes[node] = rn
	registerRecording(s.t, s.srv, node, rn)
	return nil
}

func (s *scopesState) bothAnswersSame() error {
	a, b := s.bodies["models:other"], s.bodies["models:ghost"]
	if s.last["models:other"] != s.last["models:ghost"] || strings.ReplaceAll(a, "other", "X") != strings.ReplaceAll(b, "ghost", "X") {
		return fmt.Errorf("an unlisted node answers %d %q, an unknown one %d %q", s.last["models:other"], a, s.last["models:ghost"], b)
	}
	return nil
}

func (s *scopesState) neverReached(node string) error {
	if n := len(s.nodes[node].requests()); n != 0 {
		return fmt.Errorf("the node %q was reached %d times", node, n)
	}
	return nil
}

func (s *scopesState) readSessions(node string) error {
	s.call(http.MethodGet, "/swarm/nodes/"+node+"/coddy/sessions", scopedToken, "sessions:"+node)
	return nil
}

func (s *scopesState) refusedNotCarried() error {
	if s.last["sessions:nas02"] != http.StatusNotFound || !strings.Contains(s.bodies["sessions:nas02"], "not carried") {
		return fmt.Errorf("answer %d %q", s.last["sessions:nas02"], s.bodies["sessions:nas02"])
	}
	return nil
}

func (s *scopesState) noRequestReached() error {
	if n := len(s.nodes["nas02"].requests()); n != 0 {
		return fmt.Errorf("the node saw %d requests", n)
	}
	return nil
}

func (s *scopesState) fullReadsNodeList() error {
	s.call(http.MethodGet, "/swarm/nodes", "full-secret", "full-nodes")
	return nil
}

func (s *scopesState) relayAnswers(code int) error {
	got := s.last["full-nodes"]
	if got == 0 {
		got = s.last["nodes"]
	}
	if got != code {
		return fmt.Errorf("answered %d, want %d", got, code)
	}
	return nil
}

func (s *scopesState) limitedRelay(name string, streams, perMinute, burst int, holds bool) error {
	s.reset()
	g := newGateNode(s.t)
	if !holds {
		close(g.hold)
	}
	s.gate = g
	srv, ts := limitRelay(s.t, g, nil, limited(name, scopedToken, streams, perMinute, burst))
	s.srv, s.url = srv, ts.URL
	s.t.Cleanup(func() {
		select {
		case <-g.hold:
		default:
			close(g.hold)
		}
	})
	return nil
}

func (s *scopesState) slotRelay(name string, streams int) error {
	return s.limitedRelay(name, streams, 0, 0, true)
}

func (s *scopesState) windowRelay(name string, perMinute, burst int) error {
	return s.limitedRelay(name, 0, perMinute, burst, false)
}

func (s *scopesState) startCall(name string) error {
	select {
	case <-s.gate.hold:
		// The node answers at once: the call is made and finished here.
		res, _ := do(s.t, http.MethodPost, s.url+relayCompletions, scopedToken)
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("the first call: %d", res.StatusCode)
		}
	default:
		go func() { do(s.t, http.MethodPost, s.url+relayCompletions, scopedToken) }()
		select {
		case <-s.gate.entered:
		case <-time.After(5 * time.Second):
			return fmt.Errorf("the first call never reached the node")
		}
	}
	return nil
}

func (s *scopesState) startSecond(name string) error {
	res, body := do(s.t, http.MethodPost, s.url+relayCompletions, scopedToken)
	s.second.status, s.second.body, s.second.header = res.StatusCode, body, res.Header
	return nil
}

func (s *scopesState) secondRefused(code string) error {
	var e struct {
		Kind string `json:"kind"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(s.second.body), &e); err != nil {
		return fmt.Errorf("not the error object: %v: %s", err, s.second.body)
	}
	if s.second.status != http.StatusTooManyRequests || e.Kind != "busy" || e.Code != code {
		return fmt.Errorf("the second call: %d %+v", s.second.status, e)
	}
	return nil
}

func (s *scopesState) retryHint(sec int) error {
	if got := s.second.header.Get("Retry-After"); got != fmt.Sprint(sec) {
		return fmt.Errorf("Retry-After %q, want %d", got, sec)
	}
	return nil
}

func (s *scopesState) nodeSawOnlyTheFirst() error {
	if n := s.gate.calls.Load(); n != 1 {
		return fmt.Errorf("the node saw %d calls, want 1", n)
	}
	return nil
}

func (s *scopesState) relayCounted(ok, lim int, client string) error {
	_, rows := relayStats(s.t, s.url)
	for _, w := range []struct {
		outcome string
		n       int
	}{{"ok", ok}, {"limit", lim}} {
		r, found := rowOf(rows, client, "nas02", w.outcome)
		if !found || r.Calls != int64(w.n) {
			return fmt.Errorf("row %s/%s: %+v (found %v), want %d", client, w.outcome, r, found, w.n)
		}
	}
	return nil
}

func (s *scopesState) cannotReadCounters(string) error {
	if res, _ := do(s.t, http.MethodGet, s.url+"/swarm/stats", scopedToken); res.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("a scoped client read the counters: %d", res.StatusCode)
	}
	return nil
}

func TestSwarmClientScopesFeature(t *testing.T) {
	st := &scopesState{t: t}
	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Step(`^a swarm relay with the full token "([^"]*)" and a scoped client "([^"]*)" with the token "([^"]*)" that may reach the node "([^"]*)"$`, st.aRelay)
			ctx.Step(`^the node "([^"]*)" answers every route it is asked$`, st.answersEveryRoute)
			ctx.Step(`^the scoped client lists the models of the node "([^"]*)"$`, st.listModels)
			ctx.Step(`^the scoped client calls the completions of the node "([^"]*)"$`, st.callCompletions)
			ctx.Step(`^both requests reached the node$`, st.bothReached)
			ctx.Step(`^the node saw its own credential and never the client's$`, st.nodeSawItsOwnCredential)
			ctx.Step(`^the node "([^"]*)" is online through a tunnel and the scoped client may reach it$`, st.tunnelNode)
			ctx.Step(`^the request reached the node "([^"]*)"$`, st.reachedTunnelNode)
			ctx.Step(`^the scoped client reads the relay's node list$`, st.readNodeList)
			ctx.Step(`^the relay answers 401 as it would to an unknown token$`, st.plain401)
			ctx.Step(`^the node "([^"]*)" is registered but not in the client's list$`, st.otherRegistered)
			ctx.Step(`^both answers are the same, up to the node's name$`, st.bothAnswersSame)
			ctx.Step(`^the node "([^"]*)" was never reached$`, st.neverReached)
			ctx.Step(`^the scoped client reads the sessions of the node "([^"]*)"$`, st.readSessions)
			ctx.Step(`^the request is refused as not carried$`, st.refusedNotCarried)
			ctx.Step(`^no request reached the node$`, st.noRequestReached)
			ctx.Step(`^the full client reads the relay's node list$`, st.fullReadsNodeList)
			ctx.Step(`^the relay answers (\d+)$`, st.relayAnswers)
			ctx.Step(`^a relay whose client "([^"]*)" may hold (\d+) shared-model call at a time, and a node that holds every call open$`, st.slotRelay)
			ctx.Step(`^a relay whose client "([^"]*)" may start (\d+) shared-model calls a minute with a burst of (\d+), and a node that answers at once$`, st.windowRelay)
			ctx.Step(`^the client "([^"]*)" starts a shared-model call$`, st.startCall)
			ctx.Step(`^the client "([^"]*)" starts a second shared-model call$`, st.startSecond)
			ctx.Step(`^the second call is refused with 429 busy and the code "([^"]*)"$`, st.secondRefused)
			ctx.Step(`^the retry hint of the refusal is (\d+) seconds?$`, st.retryHint)
			ctx.Step(`^the node saw only the first call$`, st.nodeSawOnlyTheFirst)
			ctx.Step(`^the relay counted (\d+) "ok" and (\d+) "limit" calls? for the client "([^"]*)"$`, st.relayCounted)
			ctx.Step(`^the scoped client "([^"]*)" cannot read the counters$`, st.cannotReadCounters)
			ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
				st.reset()
				return ctx, nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/swarm_client_scopes.feature"},
			TestingT: t,
			Output:   os.Stdout,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("swarm client scopes feature failed")
	}
}
