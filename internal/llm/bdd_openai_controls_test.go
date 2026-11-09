package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cucumber/godog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIControlsFeature(t *testing.T) {
	var srv *httptest.Server
	var path, tier string
	var p Provider
	suite := godog.TestSuite{Name: "openai-controls", Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/openai_service_tier.feature"}, TestingT: t}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^an API-key Astra model with priority service$`, func() error {
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				path = r.URL.Path
				tier, _ = body["service_tier"].(string)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"service_tier\":\"priority\",\"output\":[]}}\n\n")
			}))
			var err error
			p, err = NewProvider(ProviderInput{Type: "openai", Model: "gpt-6-astra", BaseURL: srv.URL, ServiceTier: "priority", RetryDisabled: true})
			return err
		})
		sc.Step(`^the model answers a prompt$`, func() error {
			_, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, nil, func(StreamChunk) {})
			return err
		})
		sc.Step(`^the request uses Responses with priority service$`, func() error {
			if path != "/responses" || tier != "priority" {
				return fmt.Errorf("request=%s tier=%s", path, tier)
			}
			return nil
		})
		sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
			if srv != nil {
				srv.Close()
			}
			return ctx, nil
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("feature failed")
	}
}
