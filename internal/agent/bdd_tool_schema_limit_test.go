package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cucumber/godog"
	"github.com/openai/openai-go"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestToolSchemaLimitFeature(t *testing.T) {
	var calls atomic.Int32
	var request struct {
		Tools    []json.RawMessage `json:"tools"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	var turnErr error
	var stop string
	var ag *Agent
	var server *httptest.Server
	initialize := func(sc *godog.ScenarioContext) {
		sc.Step(`^a provider that rejects the offered tool parameter schemas as too large$`, func() error {
			body, err := os.ReadFile("../llm/testdata/tool_schema_limit/kimi_size.json")
			if err != nil {
				return err
			}
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write(body)
			}))
			t.Cleanup(server.Close)
			p, err := llm.NewProvider(llm.ProviderInput{Type: "openai", Model: "fixture", APIKey: "fixture-key", BaseURL: server.URL, ProxyURL: "none", MaxTokens: 100})
			if err != nil {
				return err
			}
			ag = overflowRunAgent(t, p, 128000)
			return nil
		})
		sc.Step(`^the user sends a simple greeting with tools available$`, func() error {
			stop, turnErr = ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "hello"}})
			return nil
		})
		sc.Step(`^the turn explains how to reduce the offered tool schemas$`, func() error {
			if turnErr == nil || stop != string(acp.StopReasonRefused) {
				return fmt.Errorf("expected a refused turn, got %q: %v", stop, turnErr)
			}
			for _, want := range []string{"tool schemas", "simple message", "before any tool runs", "fewer tools", "MCP server", "unknown"} {
				if !strings.Contains(turnErr.Error(), want) {
					return fmt.Errorf("explanation lacks %q: %v", want, turnErr)
				}
			}
			var upstream *openai.Error
			if !errors.As(turnErr, &upstream) || llm.UpstreamStatus(turnErr) != 400 {
				return fmt.Errorf("provider cause was lost")
			}
			return nil
		})
		sc.Step(`^the rejected request is sent only once and no tool runs$`, func() error {
			if calls.Load() != 1 || len(request.Tools) == 0 {
				return fmt.Errorf("requests=%d, offered tools=%d", calls.Load(), len(request.Tools))
			}
			for _, m := range ag.state.GetMessages() {
				if m.Role == llm.RoleTool {
					return fmt.Errorf("a tool ran")
				}
			}
			for _, m := range request.Messages {
				if m.Role == "user" && m.Content == "hello" {
					return nil
				}
			}
			return fmt.Errorf("greeting missing from request")
		})
	}
	suite := godog.TestSuite{Name: "tool-schema-limit", ScenarioInitializer: initialize, Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/tool_schema_limit.feature"}, TestingT: t, Strict: true}}
	if suite.Run() != 0 {
		t.Fatal("tool schema limit feature failed")
	}
}
