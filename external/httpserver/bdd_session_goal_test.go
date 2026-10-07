//go:build http

package httpserver

// Godog harness for features/session_goal_http.feature: the session goal
// through POST /v1/responses (the /goal command and the supervisor's turns in
// the stream) and the goal route, with the real agent runner over a stub
// model and a scripted supervisor check.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type goalHTTPState struct {
	root   string
	ts     *httptest.Server
	srv    *Server
	mgr    *session.Manager
	sid    string
	stream string
	checks int
	mu     sync.Mutex
}

func (s *goalHTTPState) close() {
	if s.ts != nil {
		s.ts.Close()
	}
	if s.srv != nil {
		s.srv.Drain()
	}
}

func (s *goalHTTPState) server() error {
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.root, CWD: s.root},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, ReasoningLevels: &[]string{"low", "medium", "high"}, ReasoningDefault: "medium"}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	factory := func(llm.ProviderInput) (llm.Provider, error) { return cannedSummaryProvider{}, nil }
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		ag := agent.NewAgent(cfg, st, snd, slog.Default())
		ag.SetProviderFactory(factory)
		return ag.Run(ctx, prompt)
	}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.root, &session.FileStore{Root: s.root + "/sessions"})
	s.mgr.SetGoalJudge(func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.checks++
		if s.checks == 1 {
			return session.GoalCheckResult{Verdict: session.GoalVerdictNotMet, Reason: "no fix yet", Remaining: []string{"ship the fix"}}, nil
		}
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet, Reason: "shipped"}, nil
	})
	s.srv = New(cfg, s.mgr, slog.Default(), s.root)
	s.srv.agentProviderFactory = factory
	s.ts = httptest.NewServer(s.srv.Handler())
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
	if err != nil {
		return err
	}
	s.sid = res.SessionID
	return nil
}

func (s *goalHTTPState) sendStreamed(prompt string) error {
	buf, _ := json.Marshal(map[string]interface{}{"model": "agent", "input": prompt, "stream": true})
	req, _ := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d: %s", res.StatusCode, body)
	}
	s.stream = string(body)
	return nil
}

func (s *goalHTTPState) streamCarriesGoal() error {
	for _, want := range []string{"event: goal_turn", `"kind":"kickoff"`, `"kind":"continue"`, "event: session_goal", `"status":"complete"`} {
		if !strings.Contains(s.stream, want) {
			return fmt.Errorf("the stream lacks %s:\n%s", want, s.stream)
		}
	}
	return nil
}

func (s *goalHTTPState) goalRoute(method string, body interface{}) (map[string]interface{}, error) {
	var rd io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rd = bytes.NewReader(buf)
	}
	req, _ := http.NewRequest(method, s.ts.URL+"/coddy/sessions/"+s.sid+"/goal", rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("%s goal: status %d: %s", method, res.StatusCode, raw)
	}
	var out map[string]interface{}
	return out, json.NewDecoder(res.Body).Decode(&out)
}

func (s *goalHTTPState) completeAfterOne() error {
	out, err := s.goalRoute(http.MethodGet, nil)
	if err != nil {
		return err
	}
	goal, _ := out["goal"].(map[string]interface{})
	if out["object"] != "coddy.session_goal" || goal == nil || goal["status"] != "complete" || goal["continuations"] != float64(1) || goal["objective"] != "ship the fix" {
		return fmt.Errorf("GET goal = %v", out)
	}
	return nil
}

// checkerAtDefault reads the checker the goal route names: no /goal option
// chose one, so the session's model checks at its default level.
func (s *goalHTTPState) checkerAtDefault(model, level string) error {
	out, err := s.goalRoute(http.MethodGet, nil)
	if err != nil {
		return err
	}
	goal, _ := out["goal"].(map[string]interface{})
	if goal == nil || goal["checkModel"] != model || goal["checkReasoning"] != level || goal["reasoning"] != nil {
		return fmt.Errorf("GET goal checker = %v", goal)
	}
	return nil
}

func (s *goalHTTPState) messagesCarryGoal() error {
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + s.sid + "/messages")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		Goal     map[string]interface{}   `json:"goal"`
		Messages []map[string]interface{} `json:"messages"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return err
	}
	turns := 0
	for _, m := range out.Messages {
		if _, ok := m["goal_turn"]; ok {
			turns++
		}
	}
	if out.Goal == nil || out.Goal["goal"] == nil || turns != 2 {
		return fmt.Errorf("messages: goal=%v goal_turn rows=%d", out.Goal, turns)
	}
	return nil
}

func (s *goalHTTPState) setObjective(objective string) error {
	_, err := s.goalRoute(http.MethodPatch, map[string]string{"objective": objective})
	return err
}

func (s *goalHTTPState) pause() error {
	_, err := s.goalRoute(http.MethodPatch, map[string]string{"status": "paused"})
	return err
}

func (s *goalHTTPState) pausedWith(objective string) error {
	out, err := s.goalRoute(http.MethodGet, nil)
	if err != nil {
		return err
	}
	goal, _ := out["goal"].(map[string]interface{})
	if goal == nil || goal["objective"] != objective || goal["status"] != "paused" {
		return fmt.Errorf("GET goal = %v", out)
	}
	return nil
}

func (s *goalHTTPState) clear() error {
	_, err := s.goalRoute(http.MethodDelete, nil)
	return err
}

func (s *goalHTTPState) noGoal() error {
	out, err := s.goalRoute(http.MethodGet, nil)
	if err != nil {
		return err
	}
	if out["goal"] != nil {
		return fmt.Errorf("GET goal = %v, want none", out)
	}
	return nil
}

func TestSessionGoalHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "session-goal-http",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &goalHTTPState{root: t.TempDir()}
			sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
				s.close()
				return ctx, err
			})
			sc.Step(`^a Coddy server whose supervisor finds the goal met on its second check$`, s.server)
			sc.Step(`^the browser sends "([^"]*)" as a streamed prompt$`, s.sendStreamed)
			sc.Step(`^the stream carries the goal kickoff and the goal updates$`, s.streamCarriesGoal)
			sc.Step(`^the goal route reports the goal complete after one continuation$`, s.completeAfterOne)
			sc.Step(`^the session messages carry the goal and both goal turns$`, s.messagesCarryGoal)
			sc.Step(`^the goal route names the checker "([^"]*)" at its default level "([^"]*)"$`, s.checkerAtDefault)
			sc.Step(`^the browser sets the objective "([^"]*)" through the goal route$`, s.setObjective)
			sc.Step(`^the browser pauses the goal through the goal route$`, s.pause)
			sc.Step(`^the goal route reports the goal "([^"]*)" as paused$`, s.pausedWith)
			sc.Step(`^the browser clears the goal through the goal route$`, s.clear)
			sc.Step(`^the goal route reports no goal$`, s.noGoal)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/session_goal_http.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("session goal HTTP feature failed")
	}
}
