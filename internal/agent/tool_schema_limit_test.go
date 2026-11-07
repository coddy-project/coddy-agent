package agent

import (
	"context"
	"errors"
	"fmt"
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
		cause := errors.New("Invalid schema for function '" + tc.name + "': schema depth exceeds maximum limit of 8")
		err := ag.toolSchemaLimitError(cause, ag.currentToolDefinitions(string(session.ModeAgent)))
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
	cause := errors.New("Invalid schema for function 'srv__echo': schema exceeds maximum allowed size")
	err := ag.toolSchemaLimitError(cause, []llm.ToolDefinition{{Name: "srv__echo"}})
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
			cause := errors.New("Invalid schema for function 'group__srv__echo': schema depth exceeds maximum limit of 8")
			// One offered name can correspond to two declarations. Do not
			// invent a server identity even if the provider names that tool.
			err := ag.toolSchemaLimitError(cause, []llm.ToolDefinition{{Name: "group__srv__echo"}})
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
