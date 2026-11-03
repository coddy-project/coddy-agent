//go:build http

package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"
)

// numberedTools is n OpenAI function tools named t0, t1, ...
func numberedTools(n int) string {
	tools := make([]string, n)
	for i := range tools {
		tools[i] = fmt.Sprintf(`{"type":"function","function":{"name":"t%d","parameters":{"type":"object","properties":{}}}}`, i)
	}
	return "[" + strings.Join(tools, ",") + "]"
}

func TestOpenAIClientToolLimitFeature(t *testing.T) {
	state := &openAIPassthroughState{}
	var answer string
	suite := godog.TestSuite{
		Name: "openai-client-tool-limit",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				return ctx, state.reset()
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				state.close()
				return ctx, nil
			})
			sc.Step(`^a direct model server that takes up to (\d+) client tools$`, func(limit int) error {
				if err := state.startServer(); err != nil {
					return err
				}
				state.srv.activeCfg().HTTPServer.MaxClientTools = limit
				return nil
			})
			sc.Step(`^an OpenAI client posts a chat completion offering (\d+) tools$`, func(n int) error {
				body := `{"model":"local/qwen3-1.7b","messages":[{"role":"user","content":"hello"}],"stream":false,"tools":` + numberedTools(n) + `}`
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
			sc.Step(`^the model receives all (\d+) tools and the client receives its answer$`, func(n int) error {
				if got := gjson.Get(state.backend.last(), "tools.#").Int(); got != int64(n) {
					return fmt.Errorf("model received %d tools, want %d", got, n)
				}
				if got := gjson.Get(answer, "choices.0.message.content").String(); got != passthroughPlainAnswer {
					return fmt.Errorf("client received %q, want %q", got, passthroughPlainAnswer)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/openai_client_tool_limit.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("openai client tool limit feature failed")
	}
}
