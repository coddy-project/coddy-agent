//go:build http && swarm

package httpserver

// Godog harness for features/swarm_statistics.feature. The nodes are real
// coddy HTTP servers over real statistics stores and the relay is a real
// relay, so what this exercises is the wire between them, not a stand-in for
// it.

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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	swarmserver "github.com/EvilFreelancer/coddy-agent/external/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

type swarmStatisticsNode struct {
	name    string
	mgr     *session.Manager
	server  *httptest.Server
	token   string
	mu      sync.Mutex
	asked   []string
	records int
}

// paths the relay asked this node for, under /coddy/statistics.
func (n *swarmStatisticsNode) statisticsPaths() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.asked...)
}

type swarmStatisticsState struct {
	t          *testing.T
	root       string
	relay      *httptest.Server
	relayToken string
	pairToken  string
	nodes      map[string]*swarmStatisticsNode
	answer     map[string]any
	raw        string
}

func (s *swarmStatisticsState) close() {
	for _, n := range s.nodes {
		if n.server != nil {
			n.server.Close()
		}
	}
	if s.relay != nil {
		s.relay.Close()
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
	}
	*s = swarmStatisticsState{}
}

// newNode is a coddy HTTP server with a statistics store of its own.
func (s *swarmStatisticsState) newNode(name string) (*swarmStatisticsNode, error) {
	home := filepath.Join(s.root, name, "home")
	workspace := filepath.Join(s.root, name, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, err
	}
	cfg := &config.Config{
		Paths:      config.Paths{Home: home, CWD: workspace},
		Models:     []config.ModelEntry{{Model: "fake/model"}},
		Agent:      config.Agent{Model: "fake/model"},
		HTTPServer: config.HTTPServerConfig{AuthToken: "node-token-" + name},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &session.FileStore{Root: filepath.Join(s.root, name, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}, log, workspace, store)
	node := &swarmStatisticsNode{name: name, mgr: mgr, token: cfg.HTTPServer.AuthToken}
	inner := New(cfg, mgr, log, workspace)
	node.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/coddy/statistics") {
			node.mu.Lock()
			node.asked = append(node.asked, r.URL.Path)
			node.mu.Unlock()
		}
		inner.Handler().ServeHTTP(w, r)
	}))
	return node, nil
}

func (s *swarmStatisticsState) register(name, advertise, token string) error {
	body, _ := json.Marshal(swarmdto.RegisterRequest{
		Name: name, Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: advertise, InstanceUUID: "swarm-statistics-" + name, Token: token, Version: "test",
	})
	req, _ := http.NewRequest(http.MethodPost, s.relay.URL+"/swarm/register", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.pairToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("register %s: %s %s", name, res.Status, raw)
	}
	return nil
}

func (s *swarmStatisticsState) relayWithNodes(first, second string) error {
	root, err := os.MkdirTemp("", "coddy-swarm-statistics-*")
	if err != nil {
		return err
	}
	s.root = root
	s.relayToken, s.pairToken = "relay-client-token", "relay-pair-token"
	s.nodes = map[string]*swarmStatisticsNode{}
	relayCfg := &config.Config{}
	relayCfg.Swarm.Host = "127.0.0.1"
	relayCfg.Swarm.AuthToken = s.relayToken
	relayCfg.Swarm.PairingTokens = []string{s.pairToken}
	relayServer, err := swarmserver.New(relayCfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	s.relay = httptest.NewServer(relayServer.Handler())
	for _, name := range []string{first, second} {
		node, err := s.newNode(name)
		if err != nil {
			return err
		}
		s.nodes[name] = node
		if err := s.register(name, node.server.URL, node.token); err != nil {
			return err
		}
	}
	return nil
}

func (s *swarmStatisticsState) recorded(name string, tokens int64, model string) error {
	return s.recordedAt(name, tokens, model, time.Now())
}

func (s *swarmStatisticsState) recordedAt(name string, tokens int64, model string, at time.Time) error {
	node, ok := s.nodes[name]
	if !ok {
		return fmt.Errorf("no node %q", name)
	}
	provider, apiModel, _ := strings.Cut(model, "/")
	node.records++
	// A turn and the call it made, the shape a real session journals.
	turn := statistics.Event{
		ID: fmt.Sprintf("%s-turn-%d", name, node.records), Kind: "turn", At: at,
		Session: fmt.Sprintf("sess_%s_%d", name, node.records), Project: filepath.Join(s.root, name, "workspace"),
		Source: "webui", Provider: provider, Model: apiModel, Requests: 1, ActiveMS: statistics.Int(1000),
	}
	if err := node.mgr.Statistics().Append(turn); err != nil {
		return err
	}
	call := turn
	call.ID += ":call:1"
	call.Kind = "call"
	call.Requests = 0
	call.Input, call.Output = statistics.Int(tokens-tokens/6), statistics.Int(tokens/6)
	return node.mgr.Statistics().Append(call)
}

func (s *swarmStatisticsState) get(target string) error {
	req, _ := http.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+s.relayToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	s.raw = string(raw)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s %s", target, res.Status, raw)
	}
	s.answer = map[string]any{}
	return json.Unmarshal(raw, &s.answer)
}

func (s *swarmStatisticsState) readSwarm(query string) error {
	return s.get(s.relay.URL + "/swarm/statistics?timezone=UTC" + query)
}

func (s *swarmStatisticsState) report() (statistics.Report, error) {
	var out statistics.Report
	raw, err := json.Marshal(s.answer["report"])
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *swarmStatisticsState) holdsTokensFromNodes(tokens int64, nodes int) error {
	report, err := s.report()
	if err != nil {
		return err
	}
	if report.Totals.Total == nil || *report.Totals.Total != tokens {
		return fmt.Errorf("tokens: %v in %s", report.Totals.Total, s.raw)
	}
	list, _ := s.answer["nodes"].([]any)
	if len(list) != nodes {
		return fmt.Errorf("nodes: %d, want %d, in %s", len(list), nodes, s.raw)
	}
	if strings.Contains(s.raw, "node-token-") || strings.Contains(s.raw, s.pairToken) {
		return fmt.Errorf("a credential travelled with the statistics: %s", s.raw)
	}
	return nil
}

func TestSwarmStatisticsFeatures(t *testing.T) {
	state := &swarmStatisticsState{t: t}
	setup := func(sc *godog.ScenarioContext) {
		sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
			state.close()
			return ctx, nil
		})
		sc.Step(`^a relay with the coddy nodes "([^"]*)" and "([^"]*)"$`, func(first, second string) error {
			state.t = t
			return state.relayWithNodes(first, second)
		})
		sc.Step(`^the node "([^"]*)" recorded (\d+) tokens of "([^"]*)" today$`, func(name string, tokens int64, model string) error {
			return state.recorded(name, tokens, model)
		})
		sc.Step(`^the node "([^"]*)" is registered a second time as "([^"]*)"$`, func(name, alias string) error {
			node, ok := state.nodes[name]
			if !ok {
				return fmt.Errorf("no node %q", name)
			}
			return state.register(alias, node.server.URL, node.token)
		})
		sc.Step(`^a node "([^"]*)" is registered but unreachable$`, func(name string) error {
			dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			url := dead.URL
			dead.Close()
			return state.register(name, url, "node-token-gone")
		})
		sc.Step(`^I read the swarm statistics with the client token$`, func() error {
			return state.readSwarm("")
		})
		sc.Step(`^I read the swarm statistics filtered by the model "([^"]*)"$`, func(model string) error {
			_, apiModel, _ := strings.Cut(model, "/")
			return state.readSwarm("&model=" + apiModel)
		})
		sc.Step(`^I read the statistics of the node "([^"]*)" through its mount$`, func(name string) error {
			return state.get(state.relay.URL + swarmdto.MountPath + name + "/coddy/statistics?timezone=UTC")
		})
		sc.Step(`^the report holds (\d+) tokens from (\d+) nodes$`, state.holdsTokensFromNodes)
		sc.Step(`^that answer holds (\d+) tokens$`, func(tokens int64) error {
			report, err := state.report()
			if err != nil {
				return err
			}
			if report.Totals.Total == nil || *report.Totals.Total != tokens {
				return fmt.Errorf("tokens: %v in %s", report.Totals.Total, state.raw)
			}
			return nil
		})
		sc.Step(`^the models of the report are "([^"]*)" and "([^"]*)"$`, func(first, second string) error {
			report, err := state.report()
			if err != nil {
				return err
			}
			var names []string
			for _, row := range report.Models {
				names = append(names, row.Name)
			}
			sort.Strings(names)
			_, a, _ := strings.Cut(first, "/")
			_, b, _ := strings.Cut(second, "/")
			want := []string{a, b}
			sort.Strings(want)
			if strings.Join(names, ",") != strings.Join(want, ",") {
				return fmt.Errorf("models %v, want %v", names, want)
			}
			return nil
		})
		sc.Step(`^each node was asked for its own summary, never for its whole history$`, func() error {
			for name, node := range state.nodes {
				paths := node.statisticsPaths()
				if len(paths) == 0 {
					return fmt.Errorf("the relay asked %s for nothing", name)
				}
				for _, path := range paths {
					if strings.HasSuffix(path, "/events") {
						return fmt.Errorf("the relay pulled the whole history of %s: %v", name, paths)
					}
				}
			}
			return nil
		})
		sc.Step(`^the node "([^"]*)" keeps its own time zone "([^"]*)"$`, func(name, zone string) error {
			node, ok := state.nodes[name]
			if !ok {
				return fmt.Errorf("no node %q", name)
			}
			return node.mgr.Statistics().UpdateSettings(func(st *statistics.Settings) error {
				st.Timezone = zone
				return nil
			})
		})
		sc.Step(`^the report is laid out in "([^"]*)"$`, func(zone string) error {
			report, err := state.report()
			if err != nil {
				return err
			}
			if report.Timezone != zone {
				return fmt.Errorf("the fleet report is laid out in %q, not %q", report.Timezone, zone)
			}
			return nil
		})
		// 22:00 UTC yesterday is the morning of today in Sydney, so the two
		// calendars never agree on it, whenever the suite runs.
		sc.Step(`^the node "([^"]*)" recorded (\d+) tokens of "([^"]*)" at 22:00 UTC yesterday$`, func(name string, tokens int64, model string) error {
			yesterday := time.Now().UTC().AddDate(0, 0, -1)
			at := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 22, 0, 0, 0, time.UTC)
			return state.recordedAt(name, tokens, model, at)
		})
		sc.Step(`^the one day of the report is yesterday in "([^"]*)"$`, func(zone string) error {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				return err
			}
			report, err := state.report()
			if err != nil {
				return err
			}
			want := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
			if len(report.Days) != 1 || report.Days[0].Date != want {
				return fmt.Errorf("days %+v, want one dated %s", report.Days, want)
			}
			return nil
		})
		sc.Step(`^the node "([^"]*)" is listed once$`, func(name string) error {
			list, _ := state.answer["nodes"].([]any)
			seen := 0
			for _, row := range list {
				node, _ := row.(map[string]any)
				if text, _ := node["name"].(string); text == name {
					seen++
				}
			}
			if seen != 1 {
				return fmt.Errorf("%q listed %d times in %s", name, seen, state.raw)
			}
			return nil
		})
		sc.Step(`^a warning names the node "([^"]*)"$`, func(name string) error {
			warnings, _ := state.answer["warnings"].([]any)
			for _, warning := range warnings {
				if text, _ := warning.(string); strings.Contains(text, name) {
					return nil
				}
			}
			return fmt.Errorf("no warning about %q in %s", name, state.raw)
		})
	}
	suite := godog.TestSuite{
		Name:                "swarm-statistics",
		ScenarioInitializer: setup,
		Options:             &godog.Options{Format: "progress", Paths: []string{"../../features/swarm_statistics.feature"}, TestingT: t},
	}
	if suite.Run() != 0 {
		t.Fatal("swarm statistics specifications failed")
	}
}
