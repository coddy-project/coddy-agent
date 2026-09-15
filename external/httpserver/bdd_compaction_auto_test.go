//go:build http

package httpserver

// Godog harness for features/context_compaction_auto.feature: a model with a
// tiny context window - its own max_context_tokens, or the window its
// provider's model listing reports - makes any prompt exceed the
// auto-compaction threshold, so a regular /v1/responses turn compacts history
// first.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func (s *compactHTTPFeatureState) startServerTinyWindow() error {
	// 50 tokens: even a short conversation plus the system prompt exceeds 80%.
	return s.startServerWithContextWindow(50)
}

// providerReportedWindow is the context window the stand-in model listing
// reports for fake/model: small enough that any prompt crosses 80% of it,
// while the 128000 default would never be crossed by this conversation.
const providerReportedWindow = 50

func (s *compactHTTPFeatureState) startServerProviderReportsTinyWindow() error {
	s.listing = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"object":"list","data":[{"id":"model","limit":{"context":%d}}]}`, providerReportedWindow)
	}))
	return s.startServerWithProvider(config.ProviderConfig{
		Name: "fake", Type: "openai", APIKey: "test", APIBase: s.listing.URL,
	}, 0)
}

// modelListReportsProviderWindow checks the number the web UI draws its
// context ring against: GET /v1/models must carry the provider's window for
// the model, the same window the trigger measured against.
func (s *compactHTTPFeatureState) modelListReportsProviderWindow() error {
	return s.modelListReportsWindow(providerReportedWindow)
}

func (s *compactHTTPFeatureState) modelListReportsWindow(want int) error {
	res, err := http.Get(s.ts.URL + "/v1/models")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Data []struct {
			ID               string `json:"id"`
			MaxContextTokens int    `json:"max_context_tokens"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode /v1/models: %w", err)
	}
	for _, m := range body.Data {
		if m.ID == "fake/model" {
			if m.MaxContextTokens != want {
				return fmt.Errorf("GET /v1/models max_context_tokens for fake/model = %d, want %d", m.MaxContextTokens, want)
			}
			return nil
		}
	}
	return fmt.Errorf("GET /v1/models has no fake/model row: %+v", body.Data)
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
	var releaseListing func()
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if releaseListing != nil {
			releaseListing()
			_ = s.mgr.WaitContextWindowsIdle(5 * time.Second)
		}
		s.close()
		return ctx, nil
	})

	const lateWindow = 262144
	sc.Step(`^a running coddy HTTP server with a delayed provider window$`, func() error {
		if err := s.startServerWithProvider(config.ProviderConfig{
			Name: "fake", Type: "openai", APIKey: "test", APIBase: "https://listing.invalid/v1",
		}, 0); err != nil {
			return err
		}
		gate := make(chan struct{})
		releaseListing = func() {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
		s.mgr.SetContextWindowLister(func(ctx context.Context, _ llm.ProviderInput) ([]llm.ModelEntry, error) {
			select {
			case <-gate:
				return []llm.ModelEntry{{ID: "model", ContextWindow: lateWindow}}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}, nil)
		return nil
	})
	sc.Step(`^the model list returns the fallback window before the provider answers$`, func() error {
		return s.modelListReportsWindow(config.DefaultContextWindowTokens)
	})
	sc.Step(`^the provider finishes reporting its context window$`, func() error {
		releaseListing()
		return s.mgr.WaitContextWindowsIdle(5 * time.Second)
	})
	sc.Step(`^the user sends a streaming compaction command$`, s.sendCompactPrompt)
	sc.Step(`^the usage update reports the late provider window$`, func() error {
		if s.streamUsage == nil || s.streamUsage.Size != lateWindow {
			return fmt.Errorf("usage_update = %+v, want size %d", s.streamUsage, lateWindow)
		}
		return nil
	})

	sc.Step(`^a running coddy HTTP server with a summarizing agent and a tiny context window$`, s.startServerTinyWindow)
	sc.Step(`^a running coddy HTTP server whose model has no max_context_tokens and whose provider reports a tiny context window$`, s.startServerProviderReportsTinyWindow)
	sc.Step(`^the model list reports the provider's context window for the model$`, s.modelListReportsProviderWindow)
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
