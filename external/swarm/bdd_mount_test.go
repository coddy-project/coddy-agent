//go:build swarm

package swarm

// Godog harness for features/swarm_mount.feature. A real httptest node stands
// in for a `coddy serve` node, and every step goes over the relay's real HTTP surface,
// so the spec covers the proxy contract rather than its internals.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// nodeRecord is what the stand-in node saw.
type nodeRecord struct {
	Path  string
	Auth  string
	Query string
}

type mountFeatureState struct {
	relay    *httptest.Server
	srv      *Server
	node     *httptest.Server
	child    *httptest.Server
	childSrv *Server
	client   string
	pair     string

	mu        sync.Mutex
	seen      []nodeRecord
	childSeen []nodeRecord

	status         int
	body           []byte
	header         http.Header
	chunks         []string
	childResponses map[string][]byte
	// nodeCORS makes the node answer with CORS headers of its own.
	nodeCORS bool
	// delivered carries the client's word that the chunk the node just wrote
	// has arrived. The node waits on it before producing the next one, which
	// is what turns "the relay streams" into something the scenario can prove
	// rather than time.
	delivered chan struct{}
	streamErr error
	stalled   bool
}

func (s *mountFeatureState) reset() {
	if s.relay != nil {
		s.relay.Close()
		s.relay = nil
	}
	if s.node != nil {
		s.node.Close()
		s.node = nil
	}
	if s.child != nil {
		s.child.Close()
		s.child = nil
	}
	if s.childSrv != nil {
		s.childSrv.Close()
		s.childSrv = nil
	}
	s.mu.Lock()
	s.seen = nil
	s.childSeen = nil
	s.stalled = false
	s.mu.Unlock()
	s.status, s.body, s.chunks, s.streamErr = 0, nil, nil, nil
	s.header, s.nodeCORS = nil, false
	s.childResponses = nil
}

// awaitDelivery blocks the node until the client reports the chunk it just
// wrote, and reports whether that happened.
func (s *mountFeatureState) awaitDelivery(ctx context.Context, delivered <-chan struct{}) bool {
	select {
	case <-delivered:
		return true
	case <-ctx.Done():
		return false
	case <-time.After(streamAck):
		s.mu.Lock()
		s.stalled = true
		s.mu.Unlock()
		return false
	}
}

// noteDelivery is the other half. The channel holds the whole stream, so the
// client never blocks here even once the node has stopped listening.
func (s *mountFeatureState) noteDelivery() {
	select {
	case s.delivered <- struct{}{}:
	default:
	}
}

func (s *mountFeatureState) record(r *http.Request) {
	s.mu.Lock()
	s.seen = append(s.seen, nodeRecord{
		Path:  r.URL.Path,
		Auth:  r.Header.Get("Authorization"),
		Query: r.URL.RawQuery,
	})
	s.mu.Unlock()
}

func (s *mountFeatureState) lastSeen() (nodeRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) == 0 {
		return nodeRecord{}, false
	}
	return s.seen[len(s.seen)-1], true
}

// ---- steps ----

func (s *mountFeatureState) aRelay(pair, client string) error {
	s.reset()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
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

func (s *mountFeatureState) anAgentNode(name string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/coddy/sessions", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if s.nodeCORS {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization")
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"origin": "node:" + name, "sessions": []interface{}{}})
	})
	// delivered belongs to this node: the handler closes over it, so a request
	// left over from an earlier scenario cannot reach the next one's gate.
	delivered := make(chan struct{}, 8)
	s.delivered = delivered
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		// Read the request out before answering, the way a node that parses a
		// JSON body does. A node that answers first leaves the relay's
		// transport still writing that body while this server drains and
		// closes it - the server does that the moment a response header goes
		// out - and the transport answers the failed write by tearing down the
		// connection, which cuts the very stream this scenario is about.
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		for i := 0; i < 3; i++ {
			_, _ = fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			fl.Flush()
			if !s.awaitDelivery(r.Context(), delivered) {
				return
			}
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		writeJSON(w, http.StatusOK, map[string]interface{}{"origin": "node:" + name})
	})
	s.node = httptest.NewServer(mux)

	_, err := s.srv.registry.Register(swarmdto.RegisterRequest{
		Name:         name,
		Kind:         swarmdto.KindAgent,
		Transport:    swarmdto.TransportDirect,
		AdvertiseURL: s.node.URL,
		InstanceUUID: "uuid-" + name,
		Token:        "node-secret",
		Version:      "test",
	})
	return err
}

func (s *mountFeatureState) aChildRelay(name, clientToken string) error {
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.Name = name
	cfg.Swarm.AuthToken = clientToken
	childSrv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	s.childSrv = childSrv
	s.child = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.childSeen = append(s.childSeen, nodeRecord{Path: r.URL.Path, Auth: r.Header.Get("Authorization")})
		s.mu.Unlock()
		childSrv.Handler().ServeHTTP(w, r)
	}))

	_, err = s.srv.registry.Register(swarmdto.RegisterRequest{
		Name:         name,
		Kind:         swarmdto.KindRelay,
		Transport:    swarmdto.TransportDirect,
		AdvertiseURL: s.child.URL,
		InstanceUUID: "uuid-" + name,
		Token:        clientToken,
		Version:      "test",
	})
	return err
}

func (s *mountFeatureState) browserReadsChildRelayEndpoints() error {
	s.childResponses = make(map[string][]byte, 2)
	for _, path := range []string{"/swarm/info", "/swarm/topology"} {
		if err := s.callWithClientToken(path, "inner"); err != nil {
			return err
		}
		if s.status != http.StatusOK {
			return fmt.Errorf("GET %s returned %d: %s", path, s.status, s.body)
		}
		s.childResponses[path] = append([]byte(nil), s.body...)
	}
	return nil
}

func (s *mountFeatureState) mountedChildIdentifiesItself(name string) error {
	var info swarmdto.Info
	if err := json.Unmarshal(s.childResponses["/swarm/info"], &info); err != nil {
		return fmt.Errorf("decode child info: %w", err)
	}
	if info.Name != name {
		return fmt.Errorf("child info name = %q, want %q", info.Name, name)
	}
	var topology Topology
	if err := json.Unmarshal(s.childResponses["/swarm/topology"], &topology); err != nil {
		return fmt.Errorf("decode child topology: %w", err)
	}
	if topology.Root.Name != name {
		return fmt.Errorf("child topology root = %q, want %q", topology.Root.Name, name)
	}
	return nil
}

func (s *mountFeatureState) childSawCredential(want string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.childSeen) != 2 {
		return fmt.Errorf("child saw %d requests, want 2", len(s.childSeen))
	}
	for _, rec := range s.childSeen {
		if rec.Auth != want {
			return fmt.Errorf("child saw authorization %q on %s, want %q", rec.Auth, rec.Path, want)
		}
		if rec.Auth == "Bearer "+s.client {
			return fmt.Errorf("outer client token crossed the child boundary on %s", rec.Path)
		}
	}
	return nil
}

func (s *mountFeatureState) callOnNode(path, node, bearer string) error {
	req, err := http.NewRequest(http.MethodGet, s.relay.URL+swarmdto.MountPath+node+path, nil)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
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

// relayAllowsOrigin turns the relay's own CORS on. The server keeps the pointer
// it was built with, so the policy is read on the next request.
func (s *mountFeatureState) relayAllowsOrigin(origin string) error {
	s.srv.cfg.Swarm.CORS.Enabled = true
	s.srv.cfg.Swarm.CORS.AllowedOrigins = []string{origin}
	return nil
}

// relayAllowsLoopbackOrigins is the laptop case: the page comes from the
// browser's own machine, on whatever port its coddy serve took.
func (s *mountFeatureState) relayAllowsLoopbackOrigins() error {
	s.srv.cfg.Swarm.CORS.Enabled = true
	s.srv.cfg.Swarm.CORS.AllowLoopback = true
	return nil
}

// nodeAnswersWithCORS makes the node behave like a coddy serve whose own
// httpserver.cors is on - a node that browsers also reach directly.
func (s *mountFeatureState) nodeAnswersWithCORS() error {
	s.nodeCORS = true
	return nil
}

func (s *mountFeatureState) browserCallsOnNode(origin, path, node string) error {
	req, err := http.NewRequest(http.MethodGet, s.relay.URL+swarmdto.MountPath+node+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.client)
	req.Header.Set("Origin", origin)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.status = res.StatusCode
	s.body, _ = io.ReadAll(res.Body)
	s.header = res.Header
	return nil
}

// A browser refuses a response that names the allowed origin more than once,
// "*, *" included, so the count is what matters here.
func (s *mountFeatureState) responseAllowsOriginOnce(origin string) error {
	got := s.header.Values("Access-Control-Allow-Origin")
	if len(got) != 1 || got[0] != origin {
		return fmt.Errorf("Access-Control-Allow-Origin = %q, want exactly [%q]", got, origin)
	}
	for _, name := range []string{"Access-Control-Allow-Headers", "Access-Control-Allow-Methods"} {
		if n := len(s.header.Values(name)); n != 1 {
			return fmt.Errorf("%s is sent %d times, want once", name, n)
		}
	}
	return nil
}

// The Files window reads these back from a workspace file; across origins a
// browser hides every response header the answer does not expose.
func (s *mountFeatureState) responseExposes(list string) error {
	exposed := strings.ToLower(s.header.Get("Access-Control-Expose-Headers"))
	for _, name := range strings.Split(list, ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); !strings.Contains(exposed, name) {
			return fmt.Errorf("Access-Control-Expose-Headers = %q, missing %s", exposed, name)
		}
	}
	return nil
}

// relayPreflightAllows sends the preflight a browser sends before a request
// with those headers, the way the Files window revalidates a file.
func (s *mountFeatureState) relayPreflightAllows(list, method string) error {
	req, err := http.NewRequest(http.MethodOptions, s.relay.URL+swarmdto.MountPath+"nas02/coddy/sessions/s1/workspace/raw", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Origin", s.header.Get("Access-Control-Allow-Origin"))
	req.Header.Set("Access-Control-Request-Method", method)
	req.Header.Set("Access-Control-Request-Headers", strings.ToLower(strings.ReplaceAll(list, " ", "")))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	allowed := strings.ToLower(res.Header.Get("Access-Control-Allow-Headers"))
	for _, name := range strings.Split(list, ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); !strings.Contains(allowed, name) {
			return fmt.Errorf("preflight allows headers %q, missing %s", allowed, name)
		}
	}
	if !strings.Contains(res.Header.Get("Access-Control-Allow-Methods"), method) {
		return fmt.Errorf("preflight allows methods %q, missing %s", res.Header.Get("Access-Control-Allow-Methods"), method)
	}
	return nil
}

func (s *mountFeatureState) callWithClientToken(path, node string) error {
	return s.callOnNode(path, node, s.client)
}

func (s *mountFeatureState) callWithoutCredential(path, node string) error {
	return s.callOnNode(path, node, "")
}

func (s *mountFeatureState) streamFromNode(path, node string) error {
	// The deadline is what a wedged stream runs into instead of the package's
	// own ten-minute timeout, which would say nothing about which step hung.
	ctx, cancel := context.WithTimeout(context.Background(), 2*streamAck)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.relay.URL+swarmdto.MountPath+node+path, strings.NewReader(`{"stream":true}`))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.client)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.status = res.StatusCode

	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s.chunks = append(s.chunks, line)
		s.noteDelivery()
	}
	// Reading to the end rather than stopping at [DONE] leaves the relay's
	// copy finished before the next step tears the servers down. Closing the
	// body early would cancel that copy in flight, and the scenario would be
	// asserting on a stream it had just cut itself.
	s.streamErr = sc.Err()
	return nil
}

func (s *mountFeatureState) nodeReceivedPath(want string) error {
	rec, ok := s.lastSeen()
	if !ok {
		return fmt.Errorf("the node received nothing")
	}
	if rec.Path != want {
		return fmt.Errorf("node received %q, want %q", rec.Path, want)
	}
	return nil
}

// nodeReceivedQuery compares the query the node saw, parameters in the order
// url.Values.Encode writes them.
func (s *mountFeatureState) nodeReceivedQuery(want string) error {
	rec, ok := s.lastSeen()
	if !ok {
		return fmt.Errorf("the node received nothing")
	}
	if rec.Query != want {
		return fmt.Errorf("node received query %q, want %q", rec.Query, want)
	}
	return nil
}

func (s *mountFeatureState) responseComesFromTheNode() error {
	var out map[string]interface{}
	if err := json.Unmarshal(s.body, &out); err != nil {
		return fmt.Errorf("decode body: %w (%s)", err, s.body)
	}
	origin, _ := out["origin"].(string)
	if !strings.HasPrefix(origin, "node:") {
		return fmt.Errorf("body did not come from a node: %s", s.body)
	}
	return nil
}

func (s *mountFeatureState) nodeSawAuthorization(want string) error {
	rec, ok := s.lastSeen()
	if !ok {
		return fmt.Errorf("the node received nothing")
	}
	if rec.Auth != want {
		return fmt.Errorf("node saw authorization %q, want %q", rec.Auth, want)
	}
	return nil
}

func (s *mountFeatureState) receivedStreamedChunks() error {
	s.mu.Lock()
	stalled := s.stalled
	s.mu.Unlock()
	// The node writes a chunk and then waits to hear that it arrived, so a
	// relay that buffered the response leaves it waiting: the whole answer
	// would only be handed over once the handler had returned, and the handler
	// cannot return until the client has seen the chunk before it.
	if stalled {
		return fmt.Errorf("the node waited %s for a chunk to reach the client, so the relay buffered the response: %v", streamAck, s.chunks)
	}
	if s.streamErr != nil {
		return fmt.Errorf("the stream ended early: %v (chunks %v)", s.streamErr, s.chunks)
	}
	want := []string{"data: chunk-0", "data: chunk-1", "data: chunk-2", "data: [DONE]"}
	if !slices.Equal(s.chunks, want) {
		return fmt.Errorf("received %v, want %v", s.chunks, want)
	}
	return nil
}

func (s *mountFeatureState) refusedAsNotCarried() error {
	if s.status != http.StatusNotFound {
		return fmt.Errorf("status %d, want 404, body %s", s.status, s.body)
	}
	if !strings.Contains(string(s.body), "not carried") {
		return fmt.Errorf("the refusal should say the route is not carried: %s", s.body)
	}
	return nil
}

func (s *mountFeatureState) errorNamesNode(name string) error {
	var wrap struct {
		Error struct {
			Node    string `json:"node"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(s.body, &wrap); err != nil {
		return fmt.Errorf("decode error body: %w (%s)", err, s.body)
	}
	if wrap.Error.Node != name {
		return fmt.Errorf("error names node %q, want %q (body %s)", wrap.Error.Node, name, s.body)
	}
	return nil
}

func (s *mountFeatureState) rejectedUnauthorized() error {
	if s.status != http.StatusUnauthorized {
		return fmt.Errorf("status %d, want 401, body %s", s.status, s.body)
	}
	return nil
}

func TestSwarmMountFeature(t *testing.T) {
	st := &mountFeatureState{}
	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Step(`^a swarm relay with pairing token "([^"]*)" and client token "([^"]*)"$`, st.aRelay)
			ctx.Step(`^an agent node "([^"]*)" that reports what it receives$`, st.anAgentNode)
			ctx.Step(`^a child relay "([^"]*)" with client token "([^"]*)" registered under the outer relay$`, st.aChildRelay)
			ctx.Step(`^I call "([^"]*)" on node "([^"]*)" with the client token$`, st.callWithClientToken)
			ctx.Step(`^I call "([^"]*)" on node "([^"]*)" without any credential$`, st.callWithoutCredential)
			ctx.Step(`^I stream "([^"]*)" from node "([^"]*)" with the client token$`, st.streamFromNode)
			ctx.Step(`^the node received the path "([^"]*)"$`, st.nodeReceivedPath)
			ctx.Step(`^the node received the query "([^"]*)"$`, st.nodeReceivedQuery)
			ctx.Step(`^the response comes from the node$`, st.responseComesFromTheNode)
			ctx.Step(`^the node saw the authorization "([^"]*)"$`, st.nodeSawAuthorization)
			ctx.Step(`^I receive the streamed chunks as they are produced$`, st.receivedStreamedChunks)
			ctx.Step(`^the request is refused as not carried$`, st.refusedAsNotCarried)
			ctx.Step(`^the error names the node "([^"]*)"$`, st.errorNamesNode)
			ctx.Step(`^the request is rejected as unauthorized$`, st.rejectedUnauthorized)
			ctx.Step(`^the relay allows the browser origin "([^"]*)"$`, st.relayAllowsOrigin)
			ctx.Step(`^the relay allows loopback origins$`, st.relayAllowsLoopbackOrigins)
			ctx.Step(`^the node answers with CORS headers of its own$`, st.nodeAnswersWithCORS)
			ctx.Step(`^a browser at "([^"]*)" calls "([^"]*)" on node "([^"]*)" with the client token$`, st.browserCallsOnNode)
			ctx.Step(`^the response allows the origin "([^"]*)" exactly once$`, st.responseAllowsOriginOnce)
			ctx.Step(`^the response lets the browser read "([^"]*)"$`, st.responseExposes)
			ctx.Step(`^the relay lets a browser send "([^"]*)" on a "([^"]*)"$`, st.relayPreflightAllows)
			ctx.Step(`^a browser using only the outer client token reads the mounted child relay endpoints$`, st.browserReadsChildRelayEndpoints)
			ctx.Step(`^the mounted child relay identifies itself as "([^"]*)"$`, st.mountedChildIdentifiesItself)
			ctx.Step(`^the child relay saw "([^"]*)", not the outer client token$`, st.childSawCredential)
			ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
				st.reset()
				return ctx, nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/swarm_mount.feature"},
			TestingT: t,
			Output:   os.Stdout,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("swarm mount feature failed")
	}
}

// With the relay's own CORS off, a node's answer must not open the relay to a browser:
// the node's policy is about the node's address, not the relay's.
func TestMountDoesNotLendANodesCORSPolicyToTheRelay(t *testing.T) {
	st := &mountFeatureState{}
	defer st.reset()
	if err := st.aRelay("pair-secret", "client-secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.anAgentNode("nas02"); err != nil {
		t.Fatal(err)
	}
	st.nodeCORS = true
	if err := st.browserCallsOnNode("https://app.example", "/coddy/sessions", "nas02"); err != nil {
		t.Fatal(err)
	}
	if st.status != http.StatusOK {
		t.Fatalf("status = %d", st.status)
	}
	for name := range st.header {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Fatalf("the relay passed the node's %s through with its own CORS switched off", name)
		}
	}
}

// A relay chained under this one is reached through a mount like any node, and
// the mount carries /coddy/* - but the settings of that relay decide who joins
// it, and a client of this relay is not its operator (issue #401). Reading them
// goes through; changing them is refused here.
func TestMountRefusesToChangeTheSettingsOfARelayBehindIt(t *testing.T) {
	st := &mountFeatureState{}
	defer st.reset()
	if err := st.aRelay("pair-secret", "client-secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.anAgentNode("worker"); err != nil {
		t.Fatal(err)
	}
	// A relay registered under this one; the fake node answers for it too.
	if _, err := st.srv.registry.Register(swarmdto.RegisterRequest{
		Name: "inner", Kind: swarmdto.KindRelay, Transport: swarmdto.TransportDirect,
		AdvertiseURL: st.node.URL, InstanceUUID: "uuid-inner", Token: "inner-secret", Version: "test",
	}); err != nil {
		t.Fatal(err)
	}
	send := func(method, node, path string) int {
		req, err := http.NewRequest(method, st.relay.URL+swarmdto.MountPath+node+path, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+st.client)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	for _, c := range []struct {
		method, node, path string
		want               int
	}{
		{http.MethodGet, "inner", "/coddy/config", http.StatusOK},
		{http.MethodGet, "inner", "/coddy/config/schema", http.StatusOK},
		{http.MethodPut, "inner", "/coddy/config", http.StatusForbidden},
		{http.MethodPost, "inner", "/coddy/config/validate", http.StatusForbidden},
		// The node decodes the path it routes on, so an escaped letter names
		// the same route and is refused the same way.
		{http.MethodPut, "inner", "/coddy/%63onfig", http.StatusForbidden},
		{http.MethodPost, "inner", "/coddy/config/%76alidate", http.StatusForbidden},
		// An escaped letter in the prefix is no route a mount carries at all.
		{http.MethodPut, "inner", "/%63oddy/config", http.StatusNotFound},
		// An agent's settings are its API like the rest of it.
		{http.MethodPut, "worker", "/coddy/config", http.StatusOK},
	} {
		if got := send(c.method, c.node, c.path); got != c.want {
			t.Errorf("%s %s%s through the mount = %d, want %d", c.method, c.node, c.path, got, c.want)
		}
	}
}
