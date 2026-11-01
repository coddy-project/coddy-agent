//go:build http

package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"
)

func TestOpenAIBodyLimitAcceptance(t *testing.T) {
	state := &openAIPassthroughState{}
	var answer string
	const body = `{"model":"local/qwen3-1.7b","messages":[{"role":"user","content":"hello"}],"stream":false}`
	suite := godog.TestSuite{
		Name: "openai-body-limit-acceptance",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				return ctx, state.reset()
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				state.close()
				return ctx, nil
			})
			sc.Step(`^a direct model server with a 1024 byte request body limit$`, func() error {
				if err := state.startServer(); err != nil {
					return err
				}
				state.srv.activeCfg().HTTPServer.MaxBodyBytes = 1024
				return nil
			})
			sc.Step(`^an OpenAI client posts a chat completion below the limit$`, func() error {
				if len(body) >= 1024 {
					return fmt.Errorf("body is not below the limit: %d bytes", len(body))
				}
				res, raw, err := state.post(body)
				if err != nil {
					return err
				}
				if res.StatusCode != http.StatusOK {
					return fmt.Errorf("unexpected status %d", res.StatusCode)
				}
				answer = string(raw)
				return nil
			})
			sc.Step(`^the model receives the request and the client receives its answer$`, func() error {
				if got := gjson.Get(state.backend.last(), "messages.0.content").String(); got != "hello" {
					return fmt.Errorf("model received %q, want hello", got)
				}
				if got := gjson.Get(answer, "choices.0.message.content").String(); got != passthroughPlainAnswer {
					return fmt.Errorf("client received %q, want %q", got, passthroughPlainAnswer)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/openai_body_limit_acceptance.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("openai body limit acceptance failed")
	}
}
