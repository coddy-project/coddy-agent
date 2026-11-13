package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestToolSchemaLimitExplanationPreservesCauseWithoutPrintingSecrets(t *testing.T) {
	cause := errors.New("tools.function.parameters: schema exceeds maximum allowed size; token=fixture-secret https://user:password@example.invalid/?api_key=fixture-key")
	p := &failingStreamProvider{err: cause}
	ag := overflowRunAgent(t, p, 128000)
	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "hello"}})
	if err == nil || stop != string(acp.StopReasonRefused) || p.calls != 1 {
		t.Fatalf("stop=%q calls=%d err=%v", stop, p.calls, err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("provider cause lost")
	}
	for _, secret := range []string{"fixture-secret", "user:password", "example.invalid", "fixture-key"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("displayed error leaks %q", secret)
		}
	}
	if !llm.IsToolSchemaLimit(err) {
		t.Fatal("wrapped refusal lost its classification")
	}
}

func TestToolSchemaLimitExplanationUsesVerifiedOfferedMCPName(t *testing.T) {
	ag := newModeToolsAgent(t, session.ModeAgent)
	for _, tc := range []struct{ name, want, absent string }{
		{"srv__echo", `tool "srv__echo" from MCP server "srv"`, "unknown"},
		{"not_offered", "responsible tool/MCP server is unknown", `tool "not_offered"`},
	} {
		tools := ag.currentToolDefinitions(string(session.ModeAgent))
		cause := namedToolSchemaProviderRefusal(t, tc.name, tools)
		err := ag.toolSchemaLimitError(cause, tools)
		if !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), tc.absent) {
			t.Fatalf("unexpected identity: %v", err)
		}
		if !strings.Contains(err.Error(), "maximum schema depth 8") || !errors.Is(err, cause) {
			t.Fatalf("detail or cause lost: %v", err)
		}
	}
	// A name alone is not proof of a server. Match its actual declaration.
	st := &session.State{ID: "schema-name", CWD: t.TempDir(), SessionDir: t.TempDir()}
	st.AddSessionMCPClient(mcp.NewStaticClient("other", []mcp.ToolInfo{{Name: "echo"}}))
	ag.state = st
	tools := []llm.ToolDefinition{{Name: "srv__echo", InputSchema: map[string]any{"type": "object"}}}
	cause := namedToolSchemaProviderRefusal(t, "srv__echo", tools)
	err := ag.toolSchemaLimitError(cause, tools)
	if strings.Contains(err.Error(), `from MCP server`) {
		t.Fatalf("guessed a server: %v", err)
	}
}

func TestToolSchemaLimitMCPServerNamesAndAmbiguity(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprintf("ambiguous=%v", ambiguous), func(t *testing.T) {
			ag := newModeToolsAgent(t, session.ModeAgent)
			st := &session.State{ID: "schema-server", CWD: t.TempDir(), SessionDir: t.TempDir()}
			st.AddSessionMCPClient(mcp.NewStaticClient("group__srv", []mcp.ToolInfo{{Name: "echo"}}))
			if ambiguous {
				st.AddSessionMCPClient(mcp.NewStaticClient("group", []mcp.ToolInfo{{Name: "srv__echo"}}))
			}
			ag.state = st
			// One offered name can correspond to two declarations. Do not
			// invent a server identity even if the provider names that tool.
			tools := []llm.ToolDefinition{{Name: "group__srv__echo", InputSchema: map[string]any{"type": "object"}}}
			cause := namedToolSchemaProviderRefusal(t, "group__srv__echo", tools)
			err := ag.toolSchemaLimitError(cause, tools)
			if ambiguous {
				if strings.Contains(err.Error(), "from MCP server") || !strings.Contains(err.Error(), "its MCP server is unknown") {
					t.Fatalf("ambiguous declaration attributed: %v", err)
				}
			} else if !strings.Contains(err.Error(), `from MCP server "group__srv"`) {
				t.Fatalf("server name truncated: %v", err)
			}
		})
	}
}

// Exercise the real adapter boundary so attribution tests cannot manufacture
// the private outgoing-request metadata used by the LLM error classifier.
func namedToolSchemaProviderRefusal(t *testing.T, name string, tools []llm.ToolDefinition) error {
	t.Helper()
	requestNames := make(chan []string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools []struct{ Function struct{ Name string } }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		names := make([]string, len(request.Tools))
		for i, tool := range request.Tools {
			names[i] = tool.Function.Name
		}
		requestNames <- names
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": "invalid_request_error", "message": fmt.Sprintf("Invalid schema for function '%s': schema depth exceeds maximum limit of 8", name)}})
	}))
	t.Cleanup(server.Close)
	p, err := llm.NewProvider(llm.ProviderInput{Type: "openai", Model: "fixture", APIKey: "fixture-key", BaseURL: server.URL, ProxyURL: "none", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hello"}}, tools, nil)
	if err == nil || llm.UpstreamStatus(err) != 400 {
		t.Fatalf("expected fixture refusal, got %v", err)
	}
	names := <-requestNames
	if len(names) != len(tools) {
		t.Fatalf("sent %d tools, want %d", len(names), len(tools))
	}
	for i, tool := range tools {
		if names[i] != tool.Name {
			t.Fatalf("wire tool %d = %q, want %q", i, names[i], tool.Name)
		}
	}
	return err
}
