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

// newModeToolsAgent is an agent with the registry its config builds and one
// connected MCP server, which is what currentToolDefinitions works from.
func newModeToolsAgent(t *testing.T, mode session.Mode) *Agent {
	t.Helper()
	st := &session.State{ID: "sess_mode_tools_" + string(mode), CWD: t.TempDir(), Mode: mode, SessionDir: t.TempDir()}
	st.AddSessionMCPClient(mcp.NewStaticClient("srv", []mcp.ToolInfo{{Name: "echo"}}))
	return NewAgent(&config.Config{}, st, resumePermissionSender{}, nil)
}

// The execution-time check must never refuse what the loop really offers the
// model: the definitions come from currentToolDefinitions, the function every
// turn and every resumed turn calls, with the registry, the runtime wiring and
// the MCP servers it works from. A tool added to the offered set (a new append
// in currentToolDefinitions, a tool added to the registry and to the list)
// without the check knowing about it fails here.
func TestToolCallRefusedByModeNeverRefusesWhatIsOffered(t *testing.T) {
	for _, mode := range []session.Mode{session.ModePlan, session.ModeAsk} {
		t.Run(string(mode), func(t *testing.T) {
			ag := newModeToolsAgent(t, mode)
			offered := ag.currentToolDefinitions(string(mode))
			names := make(map[string]bool, len(offered))
			for _, def := range offered {
				names[def.Name] = true
				if msg, refused := toolCallRefusedByMode(string(mode), def.Name); refused {
					t.Errorf("%s mode offers %q to the model but refuses the call: %q", mode, def.Name, msg)
				}
			}
			// The comparison is not vacuous: the read tools are offered, and the
			// MCP tool of the connected server is offered in plan mode only.
			for _, want := range []string{"read", "grep", "glob"} {
				if !names[want] {
					t.Errorf("%s mode does not offer %q: %v", mode, want, names)
				}
			}
			if wantMCP := mode == session.ModePlan; names["srv__echo"] != wantMCP {
				t.Errorf("%s mode offers the MCP tool = %v, want %v", mode, names["srv__echo"], wantMCP)
			}
		})
	}
}

// ToolSetForMode and the check read the same lists, so this guards the wiring
// of the check (a mode branch, an early return) and not the list contents:
// every built-in outside the mode's list is refused with a notice naming the
// tool and the mode, every one inside it runs, and no built-in is named like an
// MCP tool, which the plan check would wave through.
func TestToolCallRefusedByModeCoversEveryBuiltIn(t *testing.T) {
	registry := tools.NewRegistry()
	for _, mode := range []string{"plan", "ask"} {
		set := ToolSetForMode(mode)
		for _, def := range registry.AllToolDefinitions() {
			if isMCPToolName(def.Name) {
				t.Errorf("built-in %q is named like an MCP tool and would be routed to callMCPTool", def.Name)
			}
			msg, refused := toolCallRefusedByMode(mode, def.Name)
			if refused == set.Allows(def.Name) {
				t.Errorf("%s mode, tool %q: allowed by the list=%v but refused=%v", mode, def.Name, set.Allows(def.Name), refused)
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
