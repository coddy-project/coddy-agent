//go:build http

package httpserver

import (
	"context"
	"fmt"
	"io"
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
			sc.Step(`^an OpenAI client asks for the model listing$`, func() error {
				res, err := http.Get(state.ts.URL + "/v1/models")
				if err != nil {
					return err
				}
				defer func() { _ = res.Body.Close() }()
				raw, err := io.ReadAll(res.Body)
				if err != nil {
					return err
				}
				if res.StatusCode != http.StatusOK {
					return fmt.Errorf("unexpected status %d: %s", res.StatusCode, raw)
				}
				answer = string(raw)
				return nil
			})
			sc.Step(`^the direct model row says it takes (\d+) client tools$`, func(limit int) error {
				row := gjson.Get(answer, `data.#(id=="local/qwen3-1.7b")`)
				if !row.Exists() {
					return fmt.Errorf("no row for the direct model: %s", answer)
				}
				if got := row.Get("limits.client_tools").Bool(); !got {
					return fmt.Errorf("limits.client_tools = false on a direct model row: %s", row.Raw)
				}
				if got := row.Get("limits.tools").Int(); got != int64(limit) {
					return fmt.Errorf("limits.tools = %d, want %d: %s", got, limit, row.Raw)
				}
				return nil
			})
			sc.Step(`^the agent row says it takes none of the client's tools$`, func() error {
				row := gjson.Get(answer, `data.#(id=="agent")`)
				if !row.Exists() {
					return fmt.Errorf("no agent row: %s", answer)
				}
				if got := row.Get("limits.client_tools").Bool(); got {
					return fmt.Errorf("limits.client_tools = true on the agent row: %s", row.Raw)
				}
				if got := row.Get("limits.tools"); got.Exists() {
					return fmt.Errorf("the agent row advertises a tool bound it never applies: %s", row.Raw)
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
