//go:build swarm

package swarm

// Godog harness for features/swarm_mount.feature. A real httptest node stands
// in for `coddy http`, and every step goes over the relay's real HTTP surface,
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
	relay  *httptest.Server
	srv    *Server
	node   *httptest.Server
	client string
	pair   string

	mu   sync.Mutex
	seen []nodeRecord

	status int
	body   []byte
	chunks []string
	gaps   []time.Duration
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
	s.mu.Lock()
	s.seen = nil
	s.mu.Unlock()
	s.status, s.body, s.chunks, s.gaps = 0, nil, nil, nil
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
		writeJSON(w, http.StatusOK, map[string]interface{}{"origin": "node:" + name, "sessions": []interface{}{}})
	})
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		for i := 0; i < 3; i++ {
			_, _ = fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			fl.Flush()
			time.Sleep(80 * time.Millisecond)
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

func (s *mountFeatureState) callWithClientToken(path, node string) error {
	return s.callOnNode(path, node, s.client)
}

func (s *mountFeatureState) callWithoutCredential(path, node string) error {
	return s.callOnNode(path, node, "")
}

func (s *mountFeatureState) streamFromNode(path, node string) error {
	req, err := http.NewRequest(http.MethodPost, s.relay.URL+swarmdto.MountPath+node+path, strings.NewReader(`{"stream":true}`))
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
	last := time.Now()
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s.chunks = append(s.chunks, line)
		s.gaps = append(s.gaps, time.Since(last))
		last = time.Now()
		if strings.Contains(line, "[DONE]") {
			break
		}
	}
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
	if len(s.chunks) != 4 {
		return fmt.Errorf("received %d chunks, want 4: %v", len(s.chunks), s.chunks)
	}
	// If the relay buffered the response, every chunk would land at once at the
	// end. Seeing the gaps proves each was forwarded as the node produced it.
	var spread int
	for _, gap := range s.gaps[1:] {
		if gap > 40*time.Millisecond {
			spread++
		}
	}
	if spread < 2 {
		return fmt.Errorf("chunks arrived together, so the relay buffered them: %v", s.gaps)
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
			ctx.Step(`^I call "([^"]*)" on node "([^"]*)" with the client token$`, st.callWithClientToken)
			ctx.Step(`^I call "([^"]*)" on node "([^"]*)" without any credential$`, st.callWithoutCredential)
			ctx.Step(`^I stream "([^"]*)" from node "([^"]*)" with the client token$`, st.streamFromNode)
			ctx.Step(`^the node received the path "([^"]*)"$`, st.nodeReceivedPath)
			ctx.Step(`^the response comes from the node$`, st.responseComesFromTheNode)
			ctx.Step(`^the node saw the authorization "([^"]*)"$`, st.nodeSawAuthorization)
			ctx.Step(`^I receive the streamed chunks as they are produced$`, st.receivedStreamedChunks)
			ctx.Step(`^the request is refused as not carried$`, st.refusedAsNotCarried)
			ctx.Step(`^the error names the node "([^"]*)"$`, st.errorNamesNode)
			ctx.Step(`^the request is rejected as unauthorized$`, st.rejectedUnauthorized)
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
