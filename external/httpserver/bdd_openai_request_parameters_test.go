//go:build http

package httpserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"
)

func TestOpenAIRequestParametersFeature(t *testing.T) {
	s := &aggregatorReasoningState{}
	suite := godog.TestSuite{Name: "openai-request-parameters", ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) { return ctx, s.reset() })
		sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
			s.close()
			return ctx, nil
		})
		sc.Step(`^an OpenAI-compatible recording upstream and a direct model$`, func() error {
			if err := s.anAggregatorServing("test-model"); err != nil {
				return err
			}
			return s.aCoddyServerOnTheAggregator("test/test-model")
		})
		sc.Step(`^a client requests a completion with top_p and a developer instruction$`, func() error {
			body := `{"model":"test/test-model","messages":[{"role":"developer","content":"follow this"},{"role":"user","content":"hello"}],"top_p":0.7,"stream":true}`
			res, err := http.Post(s.ts.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(body))
			if err != nil {
				return err
			}
			defer func() {
				if err := res.Body.Close(); err != nil {
					t.Errorf("close response body: %v", err)
				}
			}()
			raw, err := io.ReadAll(res.Body)
			if err != nil {
				return err
			}
			if res.StatusCode != 200 {
				return fmt.Errorf("status %d: %s", res.StatusCode, raw)
			}
			return nil
		})
		sc.Step(`^the upstream receives top_p and the instruction as a system message$`, func() error {
			s.backend.mu.Lock()
			defer s.backend.mu.Unlock()
			if len(s.backend.chats) == 0 {
				return fmt.Errorf("no upstream request")
			}
			raw := s.backend.chats[0]
			if gjson.GetBytes(raw, "top_p").Float() != 0.7 {
				return fmt.Errorf("top_p missing: %s", raw)
			}
			for _, m := range gjson.GetBytes(raw, "messages").Array() {
				if m.Get("role").String() == "system" && m.Get("content").String() == "follow this" {
					return nil
				}
			}
			return fmt.Errorf("developer instruction missing: %s", raw)
		})
	}, Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/openai_request_parameters.feature"}, TestingT: t, Strict: true}}
	if suite.Run() != 0 {
		t.Fatal("request parameters feature failed")
	}
}
