//go:build http && swarm

package httpserver

// Godog harness for features/remote_model_provider_relay.feature: a node that
// shares a model, behind a real swarm relay, reached by a local Coddy at the
// relay's mount of the node. The relay dials the node (direct transport) or the
// node dialled the relay (tunnel transport), with the real join client of
// internal/swarm in both cases. The suite shares its steps with the direct one
// (bdd_remote_model_test.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	swarmserver "github.com/EvilFreelancer/coddy-agent/external/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

const (
	relayTestNodeName    = "nas02"
	relayTestPairing     = "relay-pair-token"
	relayTestNodeOwn     = "node-own-token"
	relayTestNodeShared  = "node-shared-token"
	relayTestClientToken = "relay-client-token"
	relayTestSecondToken = "relay-second-client-token"
)

// relayClauses names the client tokens of a relay by how a Given step says them.
var relayClauses = struct {
	tokens map[string][]string
}{tokens: map[string][]string{
	"a client token":    {relayTestClientToken},
	"two client tokens": {relayTestClientToken, relayTestSecondToken},
}}

// relayData is what the relay's Given step leaves for the steps that follow it.
type relayData struct {
	url       string
	mountURL  string
	tokens    []string
	nodeName  string
	nodeToken string
	// noLabel is a node of an older binary: it joins with a shared-model token
	// and says nothing about it.
	noLabel bool
}

// relayWithNode builds the relay and the node behind it, and waits until the
// relay can carry a request to the node.
func relayWithNode(s *remoteModelState, rd *relayData, clients string, tunnel bool, joined, selector, alias, subject, behaviour string) error {
	tokens, ok := relayClauses.tokens[clients]
	if !ok {
		return fmt.Errorf("unknown relay clients %q", clients)
	}
	s.spec = newRemoteSpec(selector, alias)
	s.spec.mainToken, s.spec.sharedToken = relayTestNodeOwn, relayTestNodeShared
	switch joined {
	case "requires its own token":
		s.spec.auth = authMainOnly
		rd.nodeToken = relayTestNodeOwn
	case "joined with a shared-model token", "joined with a shared-model token but says nothing about it":
		s.spec.auth = authSharedOnly
		rd.nodeToken = relayTestNodeShared
		rd.noLabel = joined != "joined with a shared-model token"
	default:
		return fmt.Errorf("unknown node credential %q", joined)
	}
	if err := parseRemoteTail(&s.spec, "whose "+subject+" "+behaviour); err != nil {
		return err
	}
	node, err := newRemoteStand(s.spec, s.remoteMuts, !tunnel)
	if err != nil {
		return err
	}
	s.remote = node

	relayCfg := &config.Config{}
	relayCfg.Swarm.Host = "127.0.0.1"
	relayCfg.Swarm.AuthToken = tokens[0]
	relayCfg.Swarm.PairingTokens = []string{relayTestPairing}
	relaySrv, err := swarmserver.New(relayCfg, remoteModelLogger())
	if err != nil {
		return err
	}
	if len(tokens) > 1 {
		relaySrv.SetExtraAuthTokens(tokens[1:])
	}
	relay := newConnContextServer(relaySrv.Handler())
	s.cleanups = append(s.cleanups, relay.Close)
	rd.url, rd.tokens, rd.nodeName = relay.URL, tokens, relayTestNodeName
	rd.mountURL = relay.URL + swarmdto.MountPath + relayTestNodeName

	advertise := ""
	if !tunnel {
		advertise = node.url()
	}
	// What the node's own process registers with: the label follows the token.
	nodeCfg := &config.Config{}
	nodeCfg.HTTPServer.SharedModels.Tokens = []string{relayTestNodeShared}
	var labels map[string]string
	if !rd.noLabel {
		labels = swarmdto.DerivedLabels(nodeCfg, config.SwarmJoin{Token: rd.nodeToken}, swarmdto.KindAgent)
	}
	client, err := swarmdto.NewClient(swarmdto.JoinOptions{
		RelayURL:     relay.URL,
		Labels:       labels,
		Name:         relayTestNodeName,
		Kind:         swarmdto.KindAgent,
		PairingToken: relayTestPairing,
		AdvertiseURL: advertise,
		NodeToken:    rd.nodeToken,
		Version:      "test",
		Handler:      node.handler,
		Log:          remoteModelLogger(),
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.Run(ctx)
	}()
	s.cleanups = append(s.cleanups, func() {
		cancel()
		<-done
	})
	return remoteModelWaitUntil("the relay to carry a request to the node", func() bool {
		res, err := doHTTP(http.MethodGet, rd.mountURL+"/coddy/llm/models", tokens[0], nil)
		return err == nil && res.status == http.StatusOK
	})
}

func (rd *relayData) localPointsAtMount(s *remoteModelState) error {
	if rd.mountURL == "" {
		return fmt.Errorf("the scenario has no relay mount yet")
	}
	s.localBase, s.localToken = rd.mountURL, rd.tokens[0]
	return nil
}

func (rd *relayData) nodeReceivedItsOwnCredential(s *remoteModelState) error {
	exchanges := s.remote.capt.snapshot()
	if len(exchanges) == 0 {
		return fmt.Errorf("the node served no request")
	}
	for _, e := range exchanges {
		if e.auth != "Bearer "+rd.nodeToken {
			return fmt.Errorf("the node received the credential %q on %s %s, want its own", e.auth, e.method, e.path)
		}
		for _, tok := range rd.tokens {
			if strings.Contains(e.auth, tok) || strings.Contains(e.query, tok) {
				return fmt.Errorf("the relay's client token reached the node on %s %s", e.method, e.path)
			}
		}
	}
	return nil
}

func (rd *relayData) clientAsksMountForSessions(s *remoteModelState) error {
	res, err := doHTTP(http.MethodGet, rd.mountURL+"/coddy/sessions", rd.tokens[0], nil)
	s.http = res
	return err
}

func (rd *relayData) listCarriesNoWarning() error {
	res, err := doHTTP(http.MethodGet, rd.url+"/swarm/sessions", rd.tokens[0], nil)
	if err != nil {
		return err
	}
	var body struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(res.body, &body); err != nil {
		return fmt.Errorf("the aggregated session list is not JSON: %v: %s", err, res.body)
	}
	for _, w := range body.Warnings {
		if strings.HasPrefix(w, rd.nodeName+":") {
			return fmt.Errorf("the aggregated session list warns about %q: %q", rd.nodeName, body.Warnings)
		}
	}
	return nil
}

func (rd *relayData) listCarriesWarning() error {
	res, err := doHTTP(http.MethodGet, rd.url+"/swarm/sessions", rd.tokens[0], nil)
	if err != nil {
		return err
	}
	var body struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(res.body, &body); err != nil {
		return fmt.Errorf("the aggregated session list is not JSON: %v: %s", err, res.body)
	}
	for _, w := range body.Warnings {
		if strings.HasPrefix(w, rd.nodeName+":") && strings.Contains(w, "401") {
			return nil
		}
	}
	return fmt.Errorf("the aggregated session list warns %q, want a warning for %q about a 401", body.Warnings, rd.nodeName)
}

func (rd *relayData) eachClientHoldsAStream(s *remoteModelState, alias string) error {
	for _, tok := range rd.tokens {
		resp, err := openStream(rd.mountURL, tok, alias)
		if err != nil {
			return err
		}
		s.held = append(s.held, resp)
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("a stream of the relay's client was refused: %d", resp.StatusCode)
		}
	}
	return remoteModelWaitUntil("the streams to run on the node", func() bool { return s.remote.gate.runningCount() == len(rd.tokens) })
}

func (rd *relayData) firstClientRequestsAnother(s *remoteModelState) error {
	resp, err := openStream(rd.mountURL, rd.tokens[0], s.spec.alias)
	if err != nil {
		return err
	}
	s.sixth = resp
	s.http = httpResult{status: resp.StatusCode, header: resp.Header}
	if resp.StatusCode != http.StatusOK {
		s.http.body, _ = io.ReadAll(resp.Body)
	}
	return nil
}

func registerRelaySteps(sc *godog.ScenarioContext, s *remoteModelState) {
	rd := &relayData{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*rd = relayData{}
		return ctx, nil
	})
	sc.Step(`^a swarm relay with (a client token|two client tokens) mounting a remote coddy node( over its tunnel)? that (requires its own token|joined with a shared-model token|joined with a shared-model token but says nothing about it) and shares "([^"]*)" as "([^"]*)" whose (model|provider) (.+)$`,
		func(clients, tunnel, joined, selector, alias, subject, behaviour string) error {
			return relayWithNode(s, rd, clients, tunnel != "", joined, selector, alias, subject, behaviour)
		})
	sc.Step(`^a local coddy with a provider "([^"]*)" of type coddy pointing at the relay mount of that node with the relay's client token$`,
		func(string) error { return rd.localPointsAtMount(s) })
	sc.Step(`^the node received its own credential and not the relay's client token$`,
		func() error { return rd.nodeReceivedItsOwnCredential(s) })
	sc.Step(`^the node holds no session$`, func() error { return s.remote.assertNoSession() })
	sc.Step(`^a client of the relay asks the mount of that node for its sessions$`,
		func() error { return rd.clientAsksMountForSessions(s) })
	sc.Step(`^the node refuses it as unauthorized$`, s.refusedAsUnauthorized)
	sc.Step(`^the relay's aggregated session list carries a warning for that node$`, rd.listCarriesWarning)
	sc.Step(`^the relay's aggregated session list carries no warning for that node$`, rd.listCarriesNoWarning)
	sc.Step(`^the node allows (\d+) shared-model streams at once$`, s.remoteAllows)
	sc.Step(`^each client of the relay holds a stream from "([^"]*)" open through the mount$`,
		func(alias string) error { return rd.eachClientHoldsAStream(s, alias) })
	sc.Step(`^the first client requests a third stream$`, func() error { return rd.firstClientRequestsAnother(s) })
	sc.Step(`^the node answers 429 with the kind "([^"]*)"$`,
		func(kind string) error { return s.answerKind(http.StatusTooManyRequests, kind) })
}

func TestRemoteModelRelayFeature(t *testing.T) {
	runRemoteModelSuite(t, "remote-model-relay", "../../features/remote_model_provider_relay.feature", registerRelaySteps)
}
