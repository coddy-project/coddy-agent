//go:build http

package httpserver

// Godog harness for features/context_compaction_auto.feature: a model with a
// tiny max_context_tokens makes any prompt exceed the auto-compaction
// threshold, so a regular /v1/responses turn compacts history first.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

func (s *compactHTTPFeatureState) startServerTinyWindow() error {
	// 50 tokens: even a short conversation plus the system prompt exceeds 80%.
	return s.startServerWithContextWindow(50)
}

func (s *compactHTTPFeatureState) sendRegularPrompt() error {
	payload := map[string]interface{}{
		"model":  "agent",
		"input":  "please continue with the work",
		"stream": false,
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	s.status = res.StatusCode
	var parsed struct {
		Output []struct {
			Text string `json:"text"`
		} `json:"output"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode /v1/responses body: %w", err)
	}
	s.respText = ""
	for _, o := range parsed.Output {
		s.respText += o.Text
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d", s.status)
	}
	return nil
}

func (s *compactHTTPFeatureState) agentReplyArrives() error {
	if !strings.Contains(s.respText, "canned answer") {
		return fmt.Errorf("agent reply missing, got %q", s.respText)
	}
	return nil
}

func initializeCompactionAutoScenario(sc *godog.ScenarioContext) {
	s := &compactHTTPFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy HTTP server with a summarizing agent and a tiny context window$`, s.startServerTinyWindow)
	sc.Step(`^an HTTP session with (\d+) completed exchanges$`, s.sessionWithExchanges)
	sc.Step(`^the user sends a regular prompt$`, s.sendRegularPrompt)
	sc.Step(`^the agent reply arrives over HTTP$`, s.agentReplyArrives)
	sc.Step(`^the session transcript contains a compaction summary row$`, s.transcriptHasSummaryRow)
	sc.Step(`^the session transcript still contains all (\d+) original exchanges$`, func(int) error { return s.transcriptKeepsAllExchanges() })
	sc.Step(`^HTTP session stats match the compacted LLM context$`, s.statsMatchCompactedContext)
}

func TestContextCompactionAutoFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "context-compaction-auto",
		ScenarioInitializer: initializeCompactionAutoScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/context_compaction_auto.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("context compaction auto feature suite failed")
	}
}
