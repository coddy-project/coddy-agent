//go:build http

package httpserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"
)

func TestOpenAIModelListingFeature(t *testing.T) {
	state := &openAIPassthroughState{}
	var answer string
	suite := godog.TestSuite{
		Name: "openai-model-listing",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				return ctx, state.reset()
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				state.close()
				return ctx, nil
			})
			sc.Step(`^a direct model server$`, func() error {
				return state.startServer()
			})
			sc.Step(`^a direct model server capping output at (\d+) tokens$`, func(cap int) error {
				state.outputCap = cap
				return state.startServer()
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
			sc.Step(`^an OpenAI client retrieves the model "([^"]+)"$`, func(id string) error {
				res, err := http.Get(state.ts.URL + "/v1/models/" + id)
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
			sc.Step(`^the direct model row advertises an output cap of (\d+) tokens$`, func(cap int) error {
				row := gjson.Get(answer, `data.#(id=="local/qwen3-1.7b")`)
				if !row.Exists() {
					return fmt.Errorf("no row for the direct model: %s", answer)
				}
				if got := row.Get("max_output_tokens").Int(); got != int64(cap) {
					return fmt.Errorf("max_output_tokens = %d, want %d: %s", got, cap, row.Raw)
				}
				return nil
			})
			sc.Step(`^the direct model row names the parameters it accepts$`, func() error {
				row := gjson.Get(answer, `data.#(id=="local/qwen3-1.7b")`)
				if !row.Exists() {
					return fmt.Errorf("no row for the direct model: %s", answer)
				}
				params := map[string]bool{}
				for _, p := range row.Get("supported_parameters").Array() {
					params[p.String()] = true
				}
				for _, want := range []string{"max_tokens", "temperature", "tools", "reasoning_effort"} {
					if !params[want] {
						return fmt.Errorf("supported_parameters lacks %q: %s", want, row.Raw)
					}
				}
				return nil
			})
			sc.Step(`^the agent row names no generation parameters$`, func() error {
				row := gjson.Get(answer, `data.#(id=="agent")`)
				if !row.Exists() {
					return fmt.Errorf("no agent row: %s", answer)
				}
				params := map[string]bool{}
				for _, p := range row.Get("supported_parameters").Array() {
					params[p.String()] = true
				}
				for _, banned := range []string{"temperature", "tools", "max_tokens", "reasoning_effort"} {
					if params[banned] {
						return fmt.Errorf("the agent row claims %q which it refuses: %s", banned, row.Raw)
					}
				}
				return nil
			})
			sc.Step(`^the retrieved row is the direct model's listing entry$`, func() error {
				if got := gjson.Get(answer, "object").String(); got != "model" {
					return fmt.Errorf("object = %q, want model: %s", got, answer)
				}
				if got := gjson.Get(answer, "id").String(); got != "local/qwen3-1.7b" {
					return fmt.Errorf("id = %q, want local/qwen3-1.7b: %s", got, answer)
				}
				if got := gjson.Get(answer, "limits.client_tools"); !got.Exists() {
					return fmt.Errorf("the row lacks the limits of a listing entry: %s", answer)
				}
				return nil
			})
			sc.Step(`^an OpenAI client posts a chat completion asking for (\d+) output tokens$`, func(n int) error {
				body := fmt.Sprintf(`{"model":"local/qwen3-1.7b","messages":[{"role":"user","content":"hello"}],"stream":false,"max_tokens":%d}`, n)
				res, raw, err := state.post(body)
				if err != nil {
					return err
				}
				if res.StatusCode != http.StatusOK {
					return fmt.Errorf("unexpected status %d: %s", res.StatusCode, raw)
				}
				answer = string(raw)
				return nil
			})
			sc.Step(`^the model receives a cap of (\d+) tokens$`, func(cap int) error {
				if got := gjson.Get(state.backend.last(), "max_tokens").Int(); got != int64(cap) {
					return fmt.Errorf("model received max_tokens %d, want %d: %s", got, cap, state.backend.last())
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/openai_model_listing.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("openai model listing feature failed")
	}
}
