//go:build swarm

package swarm

// Godog harness for features/swarm_registry.feature. Every step goes over the
// real HTTP surface of a relay served by httptest, through the same auth gate a
// deployed relay uses, so the spec exercises the contract rather than the
// internals.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

type registryFeatureState struct {
	ts     *httptest.Server
	srv    *Server
	client string
	pair   string

	status int
	body   []byte

	secrets     map[string]string
	generations map[string]uint64
}

func (s *registryFeatureState) reset() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	s.status = 0
	s.body = nil
	s.secrets = map[string]string{}
	s.generations = map[string]uint64{}
}

func (s *registryFeatureState) relayWithTokens(pair, client string) error {
	s.reset()
	cfg := &config.Config{}
	cfg.Swarm.Name = "test-relay"
	cfg.Swarm.PairingTokens = []string{pair}
	cfg.Swarm.AuthToken = client
	s.pair, s.client = pair, client

	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	s.srv = srv
	s.ts = httptest.NewServer(srv.Handler())
	return nil
}

func (s *registryFeatureState) do(method, path, bearer string, body interface{}) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, s.ts.URL+path, rd)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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

func (s *registryFeatureState) registration(name, secret string) swarmdto.RegisterRequest {
	return swarmdto.RegisterRequest{
		Name:         name,
		Kind:         swarmdto.KindAgent,
		Transport:    swarmdto.TransportDirect,
		AdvertiseURL: "http://" + name + ".example:12345",
		InstanceUUID: "uuid-" + name,
		Token:        "node-token-" + name,
		LeaseSecret:  secret,
		Version:      "test",
	}
}

// ---- steps ----

func (s *registryFeatureState) aRelay(pair, client string) error {
	return s.relayWithTokens(pair, client)
}

func (s *registryFeatureState) readInfoAnonymously() error {
	return s.do(http.MethodGet, "/swarm/info", "", nil)
}

func (s *registryFeatureState) responseSaysSwarm() error {
	var info swarmdto.Info
	if err := json.Unmarshal(s.body, &info); err != nil {
		return fmt.Errorf("decode info: %w (body %s)", err, s.body)
	}
	if !info.Swarm {
		return fmt.Errorf("info does not identify a swarm: %s", s.body)
	}
	return nil
}

func (s *registryFeatureState) responseCarriesUUID() error {
	var info swarmdto.Info
	if err := json.Unmarshal(s.body, &info); err != nil {
		return err
	}
	if info.UUID == "" {
		return fmt.Errorf("info carries no relay uuid: %s", s.body)
	}
	return nil
}

func (s *registryFeatureState) nodeRegisters(name string) error {
	return s.nodeRegistersWithToken(name, s.pair)
}

func (s *registryFeatureState) nodeRegistersWithToken(name, token string) error {
	if err := s.do(http.MethodPost, "/swarm/register", token, s.registration(name, "")); err != nil {
		return err
	}
	s.rememberLease(name)
	return nil
}

func (s *registryFeatureState) nodeHasRegistered(name string) error {
	if err := s.nodeRegisters(name); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("setup registration failed: status %d body %s", s.status, s.body)
	}
	return nil
}

func (s *registryFeatureState) nodeRegistersAgainWithSecret(name string) error {
	if err := s.do(http.MethodPost, "/swarm/register", s.pair, s.registration(name, s.secrets[name])); err != nil {
		return err
	}
	s.rememberLease(name)
	return nil
}

func (s *registryFeatureState) nodeRegistersAgainWithoutSecret(name string) error {
	return s.do(http.MethodPost, "/swarm/register", s.pair, s.registration(name, ""))
}

func (s *registryFeatureState) rememberLease(name string) {
	var res swarmdto.RegisterResponse
	if json.Unmarshal(s.body, &res) == nil && res.LeaseSecret != "" {
		if _, seen := s.secrets[name]; !seen {
			s.secrets[name] = res.LeaseSecret
		}
		s.generations[name] = res.Generation
	}
}

func (s *registryFeatureState) registrationAccepted() error {
	if s.status != http.StatusOK {
		return fmt.Errorf("status %d, body %s", s.status, s.body)
	}
	return nil
}

func (s *registryFeatureState) registrationReturnsLeaseSecret() error {
	var res swarmdto.RegisterResponse
	if err := json.Unmarshal(s.body, &res); err != nil {
		return err
	}
	if len(res.LeaseSecret) < 32 {
		return fmt.Errorf("lease secret is missing or too short: %q", res.LeaseSecret)
	}
	return nil
}

func (s *registryFeatureState) registrationRejectedUnauthorized() error {
	if s.status != http.StatusUnauthorized {
		return fmt.Errorf("status %d, want 401, body %s", s.status, s.body)
	}
	return nil
}

func (s *registryFeatureState) registrationRejectedConflict() error {
	if s.status != http.StatusConflict {
		return fmt.Errorf("status %d, want 409, body %s", s.status, s.body)
	}
	return nil
}

func (s *registryFeatureState) requestRejectedUnauthorized() error {
	return s.registrationRejectedUnauthorized()
}

func (s *registryFeatureState) generationMoved() error {
	var res swarmdto.RegisterResponse
	if err := json.Unmarshal(s.body, &res); err != nil {
		return err
	}
	if res.Generation < 2 {
		return fmt.Errorf("generation = %d, want it past the first registration", res.Generation)
	}
	return nil
}

func (s *registryFeatureState) listNodes(bearer string) error {
	return s.do(http.MethodGet, "/swarm/nodes", bearer, nil)
}

func (s *registryFeatureState) listNodesWithClientToken() error {
	return s.listNodes(s.client)
}

func (s *registryFeatureState) listNodesAnonymously() error {
	return s.listNodes("")
}

func (s *registryFeatureState) nodeListedOnline(name string) error {
	nodes, err := s.decodeNodes()
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.Name == name {
			if !n.Online {
				return fmt.Errorf("node %q is listed but not online", name)
			}
			return nil
		}
	}
	return fmt.Errorf("node %q is not listed: %s", name, s.body)
}

func (s *registryFeatureState) nodeNotListed(name string) error {
	nodes, err := s.decodeNodes()
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.Name == name {
			return fmt.Errorf("node %q is still listed", name)
		}
	}
	return nil
}

func (s *registryFeatureState) decodeNodes() ([]swarmdto.NodeInfo, error) {
	var wrap struct {
		Nodes []swarmdto.NodeInfo `json:"nodes"`
	}
	if err := json.Unmarshal(s.body, &wrap); err != nil {
		return nil, fmt.Errorf("decode nodes: %w (body %s)", err, s.body)
	}
	return wrap.Nodes, nil
}

func (s *registryFeatureState) removeNode(name string) error {
	if err := s.do(http.MethodDelete, "/swarm/nodes/"+name, s.client, nil); err != nil {
		return err
	}
	if s.status != http.StatusNoContent {
		return fmt.Errorf("remove status %d, body %s", s.status, s.body)
	}
	return s.listNodesWithClientToken()
}

func TestSwarmRegistryFeature(t *testing.T) {
	st := &registryFeatureState{}
	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Step(`^a swarm relay with pairing token "([^"]*)" and client token "([^"]*)"$`, st.aRelay)
			ctx.Step(`^I read the relay info without any credential$`, st.readInfoAnonymously)
			ctx.Step(`^the response says it is a swarm$`, st.responseSaysSwarm)
			ctx.Step(`^the response carries a relay uuid$`, st.responseCarriesUUID)
			ctx.Step(`^the node "([^"]*)" registers with the pairing token$`, st.nodeRegisters)
			ctx.Step(`^the node "([^"]*)" registers with the pairing token "([^"]*)"$`, st.nodeRegistersWithToken)
			ctx.Step(`^the node "([^"]*)" has registered with the pairing token$`, st.nodeHasRegistered)
			ctx.Step(`^the node "([^"]*)" registers again with its lease secret$`, st.nodeRegistersAgainWithSecret)
			ctx.Step(`^the node "([^"]*)" registers again without a lease secret$`, st.nodeRegistersAgainWithoutSecret)
			ctx.Step(`^the registration is accepted$`, st.registrationAccepted)
			ctx.Step(`^the registration returns a lease secret$`, st.registrationReturnsLeaseSecret)
			ctx.Step(`^the registration is rejected as unauthorized$`, st.registrationRejectedUnauthorized)
			ctx.Step(`^the registration is rejected as a conflict$`, st.registrationRejectedConflict)
			ctx.Step(`^the request is rejected as unauthorized$`, st.requestRejectedUnauthorized)
			ctx.Step(`^the lease generation has moved$`, st.generationMoved)
			ctx.Step(`^I list the relay nodes with the client token$`, st.listNodesWithClientToken)
			ctx.Step(`^I list the relay nodes without any credential$`, st.listNodesAnonymously)
			ctx.Step(`^the node "([^"]*)" is listed as online$`, st.nodeListedOnline)
			ctx.Step(`^the node "([^"]*)" is no longer listed$`, st.nodeNotListed)
			ctx.Step(`^I remove the node "([^"]*)" with the client token$`, st.removeNode)
			ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
				st.reset()
				return ctx, nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/swarm_registry.feature"},
			TestingT: t,
			Output:   os.Stdout,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("swarm registry feature failed")
	}
}
