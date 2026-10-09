package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// The execution-time check and the advertised tool set are one boundary: a
// registry tool is refused at execution exactly when FilterToolDefinitions
// would not have offered it to the model. Running every registered tool through
// both keeps a tool added to the registry from slipping between them.
func TestToolCallRefusedByModeAgreesWithTheAdvertisedSet(t *testing.T) {
	registry := tools.NewRegistry()
	for _, mode := range []string{"plan", "ask"} {
		set := ToolSetForMode(mode)
		for _, def := range registry.AllToolDefinitions() {
			if isMCPToolName(def.Name) {
				t.Errorf("built-in %q is named like an MCP tool and would be routed to callMCPTool", def.Name)
			}
			offered := len(FilterToolDefinitions([]llm.ToolDefinition{def}, set)) == 1
			msg, refused := toolCallRefusedByMode(mode, def.Name)
			if refused == offered {
				t.Errorf("%s mode, tool %q: offered=%v but refused=%v", mode, def.Name, offered, refused)
			}
			if refused && (!strings.Contains(msg, `"`+def.Name+`"`) || !strings.Contains(strings.ToLower(msg), mode+" mode")) {
				t.Errorf("%s mode refusal for %q does not name the tool and the mode: %q", mode, def.Name, msg)
			}
		}
	}
}

func TestToolCallRefusedByModePlan(t *testing.T) {
	for _, name := range planToolNames {
		if msg, refused := toolCallRefusedByMode("plan", name); refused {
			t.Errorf("plan mode must allow %q, refused with %q", name, msg)
		}
	}
	for _, name := range []string{
		"write", "edit", "apply_patch", "config_set", "config_commit", "config_rollback",
		"coddy_todo_plan_replace", "coddy_todo_item_add", "plan_exit", "worktree_create",
		"no_such_tool", "",
	} {
		msg, refused := toolCallRefusedByMode("plan", name)
		if !refused {
			t.Errorf("plan mode must refuse %q at execution time", name)
			continue
		}
		if !strings.Contains(msg, "not available in Plan mode") {
			t.Errorf("refusal for %q = %q, want the plan-mode notice", name, msg)
		}
	}
}

// MCP tools are part of what plan mode offers, so a namespaced name passes the
// mode check; whether the server is connected and the tool enabled is decided
// by callMCPTool, as in agent mode. Ask mode offers no MCP tool and refuses all.
func TestToolCallRefusedByModeMCPNames(t *testing.T) {
	for _, name := range []string{"srv__echo", "my-server__do_thing", "a__b__c"} {
		if _, refused := toolCallRefusedByMode("plan", name); refused {
			t.Errorf("plan mode must let the MCP tool %q through to the MCP layer", name)
		}
		if _, refused := toolCallRefusedByMode("agent", name); refused {
			t.Errorf("agent mode must not refuse the MCP tool %q", name)
		}
		if _, refused := toolCallRefusedByMode("ask", name); !refused {
			t.Errorf("ask mode must refuse the MCP tool %q", name)
		}
	}
}

func TestToolCallRefusedByModeLeavesAgentModeUnrestricted(t *testing.T) {
	for _, mode := range []string{"agent", "", "unknown"} {
		for _, name := range []string{"write", "run_command", "config_commit", "srv__echo", "no_such_tool"} {
			if msg, refused := toolCallRefusedByMode(mode, name); refused {
				t.Errorf("mode %q must not refuse %q, got %q", mode, name, msg)
			}
		}
	}
}

// What the MCP layer says about a call decides whether it runs, in plan mode
// as in agent mode: a connected, enabled tool reaches the client, a disabled
// tool and an unknown server are rejected there, and none of them is answered
// with the plan-mode refusal.
func TestExecuteToolCallPlanModeLeavesMCPCallsToTheMCPLayer(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		allowed bool
		wantErr string
	}{
		{"connected and enabled", "srv__echo", true, "client closed"},
		{"disabled tool", "srv__echo", false, "is disabled"},
		{"unknown server", "ghost__echo", true, "MCP server not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			st := &session.State{
				ID:         "sess_plan_mcp",
				CWD:        dir,
				Mode:       session.ModePlan,
				SessionDir: t.TempDir(),
				MCPFilterFactory: func() func(server, tool string) bool {
					return func(string, string) bool { return tc.allowed }
				},
			}
			// A closed static client answers a call at once with "client
			// closed": reaching it proves the call went past the mode check.
			client := mcp.NewStaticClient("srv", []mcp.ToolInfo{{Name: "echo"}})
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			st.AddSessionMCPClient(client)
			st.SetPermissionMode(config.PermModeBypass)
			ag := NewAgent(&config.Config{}, st, resumePermissionSender{}, nil)
			env := &tools.Env{CWD: dir, PermissionMode: config.PermModeBypass, SessionID: st.ID}

			res, err := ag.executeToolCall(context.Background(), llm.ToolCall{ID: "call_mcp", Name: tc.tool, InputJSON: `{}`}, env, "plan", st.ID, false)
			if strings.Contains(res, "not available in Plan mode") {
				t.Fatalf("an MCP call was answered with the plan-mode refusal: %q", res)
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

// A write approved after the session moved to plan mode is refused when the
// permission answer resumes the call, and "allow always" leaves no grant for
// it: the grant would outlive the refusal and apply once the mode changes back.
func TestResumeAfterPermissionInPlanModeRefusesWriteAndRecordsNoGrant(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "notes.md")
	args, err := json.Marshal(map[string]string{"path": target, "content": "SHOULD_NOT_BE_WRITTEN"})
	if err != nil {
		t.Fatal(err)
	}
	st := &session.State{
		ID:         "sess_resume_plan",
		CWD:        dir,
		Mode:       session.ModePlan,
		SessionDir: t.TempDir(),
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "write the notes"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_plan_write", Name: "write", InputJSON: string(args)}}},
		},
	}
	provider := &resumePermissionProvider{t: t}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	if err := session.WriteToolCallArgs(st.SessionDir, "call_plan_write", string(args)); err != nil {
		t.Fatal(err)
	}

	stop, err := ag.ResumeAfterPermission(context.Background(), "call_plan_write", &acp.PermissionResult{
		Outcome:  "selected",
		OptionID: "allow_always",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop reason %q", stop)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("the approved write ran in plan mode: stat err %v", err)
	}
	var toolMsg *llm.Message
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == "call_plan_write" {
			mm := m
			toolMsg = &mm
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("missing tool result for the refused call")
	}
	if !strings.Contains(toolMsg.Content, "not available in Plan mode") {
		t.Fatalf("tool result is not the plan-mode refusal: %q", toolMsg.Content)
	}
	if grants := st.GetPermissionWriteGrants(); len(grants) != 0 {
		t.Fatalf("refused call still recorded an allow-always grant: %v", grants)
	}
}

// A resumed call the plan tool set admits still runs: the refusal on the
// resume path is about the set, not about the mode.
func TestResumeAfterPermissionInPlanModeRunsAnAllowedTool(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.md")
	if err := os.WriteFile(source, []byte("PLAN_RESUME_READ_OK\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]string{"path": source})
	if err != nil {
		t.Fatal(err)
	}
	st := &session.State{
		ID:         "sess_resume_plan_read",
		CWD:        dir,
		Mode:       session.ModePlan,
		SessionDir: t.TempDir(),
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "read the source"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_plan_read", Name: "read", InputJSON: string(args)}}},
		},
	}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return &resumePermissionProvider{t: t}, nil }
	if err := session.WriteToolCallArgs(st.SessionDir, "call_plan_read", string(args)); err != nil {
		t.Fatal(err)
	}

	if _, err := ag.ResumeAfterPermission(context.Background(), "call_plan_read", &acp.PermissionResult{
		Outcome:  "selected",
		OptionID: "allow",
	}); err != nil {
		t.Fatal(err)
	}
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == "call_plan_read" {
			if !strings.Contains(m.Content, "PLAN_RESUME_READ_OK") {
				t.Fatalf("the resumed read did not run: %q", m.Content)
			}
			return
		}
	}
	t.Fatal("missing tool result for the resumed read")
}
