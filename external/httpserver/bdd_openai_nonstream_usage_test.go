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

func (s *openAIStreamCompatState) nonStreamingCompletion(model string) error {
	request := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"stream":false}`, model)
	res, err := http.Post(s.ts.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(request))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("completion status %d: %s", res.StatusCode, data)
	}
	s.sseBody = string(data)
	return nil
}

func (s *openAIStreamCompatState) nonStreamingUsage(input, output int) error {
	usage := gjson.Get(s.sseBody, "usage")
	if !usage.Exists() || usage.Get("prompt_tokens").Int() != int64(input) || usage.Get("completion_tokens").Int() != int64(output) || usage.Get("total_tokens").Int() != int64(input+output) {
		return fmt.Errorf("usage = %s, want prompt %d completion %d in %s", usage.Raw, input, output, s.sseBody)
	}
	return nil
}

func (s *openAIStreamCompatState) cachedPromptTokens(cached int) error {
	got := gjson.Get(s.sseBody, "usage.prompt_tokens_details.cached_tokens")
	if !got.Exists() || got.Int() != int64(cached) {
		return fmt.Errorf("cached prompt tokens = %s, want %d in %s", got.Raw, cached, s.sseBody)
	}
	return nil
}

func TestNonStreamingUsageOmittedWhenProviderReportsNone(t *testing.T) {
	s := &openAIStreamCompatState{backendNoUsage: true}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"local/qwen3-1.7b", "agent"} {
		if err := s.nonStreamingCompletion(model); err != nil {
			t.Fatal(err)
		}
		if usage := gjson.Get(s.sseBody, "usage"); usage.Exists() {
			t.Fatalf("%s invented usage without provider counts: %s", model, s.sseBody)
		}
	}
}

func TestOpenAINonStreamingUsageFeature(t *testing.T) {
	s := &openAIStreamCompatState{}
	suite := godog.TestSuite{
		Name: "openai-nonstream-usage",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) { return ctx, s.reset() })
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				s.close()
				return ctx, nil
			})
			sc.Step(`^a coddy server with a model that reports token usage$`, s.startServer)
			sc.Step(`^an OpenAI client requests a non-streaming completion from "([^"]+)"$`, s.nonStreamingCompletion)
			sc.Step(`^the JSON completion reports (\d+) prompt and (\d+) completion tokens$`, s.nonStreamingUsage)
			sc.Step(`^its prompt token details report (\d+) cached tokens$`, s.cachedPromptTokens)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/openai_nonstream_usage.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("non-streaming usage feature failed")
	}
}
