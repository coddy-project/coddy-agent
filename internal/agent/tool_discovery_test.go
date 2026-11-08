package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// These raw responses deliberately do not use the activation-aware legacy
// fixture adapter. The model must follow (or violate) the real request offer.
func discoveryResponse(calls ...llm.ToolCall) scriptStep {
	return func(_ []llm.Message, _ []llm.ToolDefinition, chunk func(llm.StreamChunk)) *llm.Response {
		for _, c := range calls {
			c := c
			chunk(llm.StreamChunk{ToolCall: &c})
		}
		return &llm.Response{ToolCalls: calls, StopReason: "tool_use"}
	}
}
func discoveryActivation(id string, names ...string) llm.ToolCall {
	raw, _ := json.Marshal(map[string]interface{}{"names": names})
	return llm.ToolCall{ID: id, Name: "activate_tools", InputJSON: string(raw)}
}
func discoveryFixture(t *testing.T, steps ...scriptStep) (*Agent, *session.State, *scriptedProvider, *int) {
	t.Helper()
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "fake/model", toolRow{Model: "fake/model", Tools: []string{"read", "grep", "write"}})
	ag.cfg.Agent.MaxTurns = 12
	count := new(int)
	for _, name := range []string{"read", "grep", "write"} {
		tool, ok := ag.registry.Get(name)
		if !ok {
			t.Fatal(name)
		}
		tool.Execute = func(context.Context, string, *tools.Env) (string, error) {
			*count++
			return "virtual fixture result", nil
		}
	}
	p := scripted(steps...)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return p, nil }
	return ag, st, p, count
}
func discoveryRun(t *testing.T, ag *Agent) {
	t.Helper()
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "Use the virtual fixture."}}); err != nil {
		t.Fatal(err)
	}
}
func TestToolDiscoveryGreeting(t *testing.T) {
	ag, _, p, count := discoveryFixture(t, answerStep("hello"))
	discoveryRun(t, ag)
	if got := strings.Join(p.offered[0], ","); got != "activate_tools" {
		t.Fatalf("initial native offer = %s", got)
	}
	if *count != 0 {
		t.Fatal("greeting executed a tool")
	}
	for _, name := range []string{"read", "grep", "write"} {
		if !strings.Contains(p.requests[0][0].Content, "`"+name+"`") {
			t.Fatalf("catalog omits %s", name)
		}
	}
}
func TestToolDiscoveryActivationUsesNextRequest(t *testing.T) {
	read := llm.ToolCall{ID: "read_first", Name: "read", InputJSON: `{"path":"/virtual/file"}`}
	ag, st, p, count := discoveryFixture(t, discoveryResponse(discoveryActivation("activate", "read"), read), discoveryResponse(llm.ToolCall{ID: "read_next", Name: "read", InputJSON: read.InputJSON}), answerStep("done"))
	original, _ := json.Marshal(ag.currentToolDefinitions("agent")[1])
	discoveryRun(t, ag)
	if *count != 1 {
		t.Fatalf("same-response tool executed, calls=%d", *count)
	}
	if strings.Join(p.offered[0], ",") != "activate_tools" || !contains(p.offered[1], "read") {
		t.Fatal("activation did not defer schema to next request")
	}
	var loaded []byte
	for _, d := range p.definitions[1] {
		if d.Name == "read" {
			loaded, _ = json.Marshal(d)
		}
	}
	if string(loaded) != string(original) {
		t.Fatal("loaded schema changed")
	}
	for _, m := range st.GetMessages() {
		if m.ToolCallID == "activate" && strings.Contains(m.Content, "properties") {
			t.Fatal("activation repeats schema into history")
		}
	}
	if issues := session.ValidateToolPairing(session.MessagesForLLM(st.GetMessages())); len(issues) != 0 {
		t.Fatal(issues)
	}
}
func TestToolDiscoveryRestrictionRefusalKeepsItsReason(t *testing.T) {
	for _, note := range []string{"admins only", ""} {
		t.Run(note, func(t *testing.T) {
			ag, st, p, count := discoveryFixture(t,
				discoveryResponse(llm.ToolCall{ID: "restricted_write", Name: "write", InputJSON: `{"path":"/virtual/blocked","content":"blocked"}`}), answerStep("done"))
			st.SetTurnRestriction(&session.TurnRestriction{AllowedTools: []string{"read"}, Note: note, ConfineToWorkspace: true})
			st.AddCommandGrantIfNew("fixture granted command")
			var events []ToolCallEvent
			remove := AddToolCallObserver(func(e ToolCallEvent) { events = append(events, e) })
			defer remove()
			discoveryRun(t, ag)
			want := note
			if want == "" {
				want = session.ToolResultTurnNoteDefault
			}
			result, ok := toolResultOf(p.requests[1], "restricted_write")
			if !ok || result != "error: write: "+want || strings.Contains(result, "activate") {
				t.Fatalf("restriction reason lost: %q", result)
			}
			for _, native := range p.offered {
				if strings.Join(native, ",") != activateToolsName {
					t.Fatalf("excluded tool was activated: %v", native)
				}
			}
			client := ag.server.(*recordingClient)
			if *count != 0 || len(client.perms) != 0 || len(events) != 0 || len(st.GetPermissionCommandGrants()) != 1 {
				t.Fatal("excluded call reached execution, observer, permissions or changed grants")
			}
		})
	}
}

func TestToolDiscoveryAtomicUnknownAndExcluded(t *testing.T) {
	ag, _, p, count := discoveryFixture(t, discoveryResponse(discoveryActivation("activate", "read", "rm")), discoveryResponse(llm.ToolCall{ID: "read", Name: "read", InputJSON: `{}`}), answerStep("done"))
	discoveryRun(t, ag)
	if *count != 0 || contains(p.offered[1], "read") {
		t.Fatal("excluded activation partially granted tools")
	}
}

func TestToolDiscoverySearchReadReuseAndNewRun(t *testing.T) {
	ag, _, p, count := discoveryFixture(t,
		discoveryResponse(discoveryActivation("activate_both", "grep", "read")),
		discoveryResponse(llm.ToolCall{ID: "grep", Name: "grep", InputJSON: `{"pattern":"value"}`}),
		discoveryResponse(llm.ToolCall{ID: "read_one", Name: "read", InputJSON: `{"path":"/virtual/one"}`}),
		discoveryResponse(llm.ToolCall{ID: "read_two", Name: "read", InputJSON: `{"path":"/virtual/two"}`}), answerStep("done"), answerStep("hello again"))
	discoveryRun(t, ag)
	discoveryRun(t, ag)
	if *count != 3 {
		t.Fatalf("fixture actions=%d", *count)
	}
	for _, i := range []int{1, 2, 3, 4} {
		if !contains(p.offered[i], "grep") || !contains(p.offered[i], "read") {
			t.Fatalf("activation lost at request %d", i)
		}
	}
	if strings.Join(p.offered[5], ",") != "activate_tools" {
		t.Fatal("new Run reused activation from history")
	}
	for _, r := range p.requests[1:5] {
		if r[0].Content != p.requests[0][0].Content {
			t.Fatal("catalog moved as native offer grew")
		}
	}
}
func TestToolDiscoveryLifetimeChanges(t *testing.T) {
	for _, change := range []string{"workspace", "model", "toolset", "compaction"} {
		t.Run(change, func(t *testing.T) {
			ag, st, p, _ := discoveryFixture(t)
			p.steps = []scriptStep{discoveryResponse(discoveryActivation("activate", "read")), discoveryResponse(llm.ToolCall{ID: "read", Name: "read", InputJSON: `{"path":"/virtual/one"}`}), answerStep("done")}
			read, _ := ag.registry.Get("read")
			read.Execute = func(_ context.Context, _ string, env *tools.Env) (string, error) {
				switch change {
				case "workspace":
					st.SetCWD(t.TempDir())
					env.CWD = st.GetCWD()
					env.WorkspaceChanged = true
				case "model":
					ag.cfg.Models, ag.cfg.Toolsets = toolRows(toolRow{Model: "fake/model", Tools: []string{"read", "grep", "write"}}, toolRow{Model: "fake/small", Tools: []string{"grep"}})
					st.SetSelectedModelID("fake/small")
				case "toolset":
					ag.cfg.Toolsets = append(ag.cfg.Toolsets, config.Toolset{Name: "only-grep", Tools: []string{"grep"}})
					st.SetSelectedToolset("only-grep")
				case "compaction":
					st.InsertCompactionSummary(len(st.GetMessages())-1, llm.Message{Role: llm.RoleUser, Content: "compacted fixture", CompactionSummary: true})
					env.ContextCompacted = true
				}
				return "fixture result", nil
			}
			discoveryRun(t, ag)
			if contains(p.offered[2], "read") != (change == "compaction") {
				t.Fatalf("activation lifetime wrong after %s: %v", change, p.offered[2])
			}
			if change == "model" || change == "toolset" {
				if contains(promptToolNames(p.requests[2][0].Content), "read") {
					t.Fatal("catalog kept excluded tool")
				}
			}
		})
	}
}
func TestToolDiscoveryPermissionStillRequired(t *testing.T) {
	ag, st, p, count := discoveryFixture(t)
	ag.cfg.Tools.PermissionMode = config.PermModeAsk
	sender := ag.server.(*recordingClient)
	sender.answer = "reject"
	raw, _ := json.Marshal(map[string]string{"path": filepath.Join(st.GetCWD(), "fixture.txt"), "content": "fixture"})
	p.steps = []scriptStep{discoveryResponse(discoveryActivation("activate", "write")), discoveryResponse(llm.ToolCall{ID: "write", Name: "write", InputJSON: string(raw)}), answerStep("done")}
	discoveryRun(t, ag)
	if *count != 0 || len(sender.perms) != 1 {
		t.Fatalf("activation granted execution: count=%d permissions=%d", *count, len(sender.perms))
	}
}
func TestToolDiscoveryResumeOnlyPendingCall(t *testing.T) {
	ag, st, p, count := discoveryFixture(t, discoveryResponse(llm.ToolCall{ID: "fresh_read", Name: "read", InputJSON: `{"path":"/virtual/other"}`}), answerStep("done"))
	pending := llm.ToolCall{ID: "pending", Name: "read", InputJSON: `{"path":"/virtual/approved"}`}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "fixture"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{pending, {ID: "sibling", Name: "write", InputJSON: `{}`}}})
	if err := session.WriteToolCallArgs(st.GetPersistedSessionDir(), pending.ID, pending.InputJSON); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.ResumeAfterPermission(context.Background(), pending.ID, &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}); err != nil {
		t.Fatal(err)
	}
	if *count != 1 || strings.Join(p.offered[0], ",") != "activate_tools" {
		t.Fatal("resume activated tools or executed an unapproved sibling/fresh call")
	}
	if issues := session.ValidateToolPairing(session.MessagesForLLM(st.GetMessages())); len(issues) != 0 {
		t.Fatal(issues)
	}
}
func TestToolDiscoveryCustomAndChildCatalogFallback(t *testing.T) {
	for _, child := range []bool{false, true} {
		t.Run(map[bool]string{false: "custom", true: "child"}[child], func(t *testing.T) {
			ag, _, p, _ := discoveryFixture(t, answerStep("hello"))
			if child {
				ag.subagent = &session.SubagentMeta{Name: "fixture", Kind: "fixture", Tools: []string{"read"}, PromptTemplate: "Child instructions without Tools."}
			} else {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte("Custom instructions without Tools."), 0600); err != nil {
					t.Fatal(err)
				}
				ag.cfg.Prompts.Dir = dir
			}
			discoveryRun(t, ag)
			catalog := promptToolNames(p.requests[0][0].Content)
			if !contains(catalog, "read") || (child && contains(catalog, "write")) {
				t.Fatalf("discovery disappeared or exceeded child bounds: %v", catalog)
			}
		})
	}
}
func TestToolDiscoveryCatalogSentenceBoundaries(t *testing.T) {
	for _, description := range []string{
		"Read the fixture.\nKeep this later detail deferred.",
		"Need a fixture? Keep this later detail deferred.",
		"Inspect the fixture!\tKeep this later detail deferred.",
	} {
		t.Run(description, func(t *testing.T) {
			catalog := toolDiscoveryCatalog([]llm.ToolDefinition{{Name: "fixture", Description: description}})
			if strings.Contains(catalog, "later detail") {
				t.Fatalf("discovery included text after the first sentence: %q", catalog)
			}
		})
	}
}

func TestToolDiscoveryInitialAccountingAndSummary(t *testing.T) {
	ag, st, _, _ := discoveryFixture(t)
	defs := ag.currentToolDefinitions("agent")
	defs[0].Description = strings.Repeat("word ", 40) + ". Full later detail."
	build := ag.buildSystemPromptParts("agent", nil, defs)
	raw, _ := json.Marshal(activationToolDefinition())
	want := session.EstimateContextTokens(build.ToolsMD) + session.EstimateContextTokens(string(raw))
	if got := st.GetLastContextBreakdown().ToolDefinitions; got != want {
		t.Fatalf("initial accounting includes deferred schemas: got%d want%d", got, want)
	}
	if strings.Contains(build.ToolsMD, "Full later detail") || !strings.Contains(build.ToolsMD, "Lossy discovery summaries") {
		t.Fatal("catalog is not a labeled bounded discovery summary")
	}
	if len(build.ToolDefs) != 1 || build.ToolDefs[0].Name != "activate_tools" {
		t.Fatal("pre-first-call compaction sees full schemas")
	}
}

func TestToolDiscoveryActivationFinishesStreamedAndPersistedRow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		names  []string
		status string
	}{
		{"success", []string{"read"}, "completed"}, {"invalid", []string{"unknown_fixture_tool"}, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ag, st, _, _ := discoveryFixture(t, discoveryResponse(discoveryActivation("activate", tc.names...)), answerStep("done"))
			discoveryRun(t, ag)
			meta, err := session.ReadToolCallMeta(st.GetPersistedSessionDir(), "activate")
			if err != nil || meta.Status != tc.status || meta.StartedAt == "" || meta.FinishedAt == "" {
				t.Fatalf("activation row left unfinished: meta=%+v error=%v", meta, err)
			}
			result, err := session.ReadToolCallResult(st.GetPersistedSessionDir(), "activate")
			if err != nil || !strings.Contains(result, tc.names[0]) {
				t.Fatalf("activation result not persisted: %q error=%v", result, err)
			}
			sender := ag.server.(*recordingClient)
			finished := false
			for _, raw := range sender.updates {
				if update, ok := raw.(acp.ToolCallStatusUpdate); ok && update.ToolCallID == "activate" && update.Status == tc.status {
					finished = true
				}
			}
			if !finished {
				t.Fatal("activation streamed pending without a terminal update")
			}
		})
	}
}

func TestToolDiscoveryExecutionHooksAndObservers(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(map[bool]string{false: "allow", true: "deny"}[deny], func(t *testing.T) {
			ag, _, _, count := discoveryFixture(t, discoveryResponse(discoveryActivation("activate", "read")), discoveryResponse(llm.ToolCall{ID: "read", Name: "read", InputJSON: `{"path":"/virtual/file"}`}), answerStep("done"))
			home := t.TempDir()
			record := filepath.Join(home, "hook-events.jsonl")
			var entries []hooktest.Entry
			for _, event := range []string{hooks.EventPreToolUse, hooks.EventPostToolUse, hooks.EventPostToolUseFailure} {
				entries = append(entries, hooktest.Entry{Event: event, Matcher: "*", Handlers: []hooks.Handler{hooktest.Handler("record", record)}})
			}
			if deny {
				entries = append(entries, hooktest.Entry{Event: hooks.EventPreToolUse, Matcher: "read", Handlers: []hooks.Handler{hooktest.Handler("deny", "")}})
			}
			if err := hooktest.Write(filepath.Join(home, "hooks.json"), entries...); err != nil {
				t.Fatal(err)
			}
			ag.cfg.Paths.Home = home
			ag.cfg.Hooks.ApplyDefaults(ag.cfg.Paths)
			var mu sync.Mutex
			var events []ToolCallEvent
			remove := AddToolCallObserver(func(e ToolCallEvent) { mu.Lock(); events = append(events, e); mu.Unlock() })
			defer remove()
			discoveryRun(t, ag)
			recorded, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(recorded), "activate_tools") || !strings.Contains(string(recorded), `"tool_name":"read"`) {
				t.Fatalf("discovery entered execution hooks or read skipped them: %s", recorded)
			}
			want := 1
			if deny {
				want = 0
			}
			if *count != want {
				t.Fatalf("hook denial ignored: actions=%d", *count)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(events) != 1 || events[0].Tool != "read" {
				t.Fatalf("discovery emitted execution observer event: %+v", events)
			}
		})
	}
}

func TestToolDiscoveryMCPDefinitionRemainsComplete(t *testing.T) {
	ag, st, p, _ := discoveryFixture(t, discoveryResponse(discoveryActivation("activate", "fixture__inspect")), answerStep("done"))
	ag.cfg.Toolsets[0].Tools = append(ag.cfg.Toolsets[0].Tools, "fixture__inspect")
	st.AddSessionMCPClient(mcp.NewStaticClient("fixture", []mcp.ToolInfo{{Name: "inspect", Description: "Short purpose. Complete detailed MCP contract must be preserved after activation.", InputSchema: map[string]interface{}{"type": "object", "required": []string{"ids"}, "additionalProperties": false, "properties": map[string]interface{}{"ids": map[string]interface{}{"type": "array", "minItems": 1, "items": map[string]interface{}{"type": "string", "enum": []string{"alpha", "beta"}}}}}}}))
	var original []byte
	for _, d := range ag.currentToolDefinitions("agent") {
		if d.Name == "fixture__inspect" {
			original, _ = json.Marshal(d)
		}
	}
	discoveryRun(t, ag)
	if len(original) == 0 {
		t.Fatal("MCP fixture was excluded")
	}
	var loaded []byte
	for _, d := range p.definitions[1] {
		if d.Name == "fixture__inspect" {
			loaded, _ = json.Marshal(d)
		}
	}
	if string(original) != string(loaded) {
		t.Fatal("MCP schema or complete description changed on load")
	}
	if st.GetLastContextBreakdown().MCP != session.EstimateContextTokens(string(original)) {
		t.Fatal("active MCP native definition missing from context estimate")
	}
}
func TestToolDiscoveryInvalidArgumentsCannotPartiallyActivate(t *testing.T) {
	for _, args := range []string{`{}`, `null`, `{"names":[]}`, `{"names":["read","unknown_fixture_tool"]}`, `{"names":["read","read"]}`, `{"names":["read"],"unexpected":true}`, `{"names":["read"]} {}`, `{"names":["activate_tools"]}`} {
		t.Run(args, func(t *testing.T) {
			ag, _, p, count := discoveryFixture(t, discoveryResponse(llm.ToolCall{ID: "invalid", Name: "activate_tools", InputJSON: args}), discoveryResponse(llm.ToolCall{ID: "read", Name: "read", InputJSON: `{}`}), answerStep("done"))
			discoveryRun(t, ag)
			if *count != 0 || strings.Join(p.offered[1], ",") != "activate_tools" {
				t.Fatal("invalid activation changed outbound tools or executed an action")
			}
		})
	}
}

func TestToolDiscoveryNoPermittedToolsSendsNoNativeToolsOrCatalog(t *testing.T) {
	ag, st, p, _ := discoveryFixture(t, answerStep("hello"))
	ag.cfg.Toolsets[0].DisallowedTools = []string{"*"}
	discoveryRun(t, ag)
	if len(p.definitions[0]) != 0 {
		t.Fatalf("deny-all sent native tools: %v", p.offered[0])
	}
	if strings.Contains(p.requests[0][0].Content, "## Available tools") || len(promptToolNames(p.requests[0][0].Content)) != 0 || strings.Contains(p.requests[0][0].Content, "Lossy discovery summaries") || strings.Contains(p.requests[0][0].Content, "call activate_tools") {
		t.Fatal("deny-all advertises unavailable discovery")
	}
	if b := st.GetLastContextBreakdown(); b.ToolDefinitions != 0 || b.MCP != 0 {
		t.Fatalf("deny-all counted native tools/catalog: %+v", b)
	}
}
func TestToolDiscoveryEmptyTransitionClearsActivation(t *testing.T) {
	ag, st, p, count := discoveryFixture(t)
	ag.cfg.Toolsets = append(ag.cfg.Toolsets, config.Toolset{Name: "none", DisallowedTools: []string{"*"}})
	read, _ := ag.registry.Get("read")
	read.Execute = func(context.Context, string, *tools.Env) (string, error) {
		*count++
		if *count == 1 {
			st.SetSelectedToolset("none")
		}
		return "fixture result", nil
	}
	p.steps = []scriptStep{
		discoveryResponse(discoveryActivation("activate_first", "read")),
		discoveryResponse(llm.ToolCall{ID: "read_first", Name: "read", InputJSON: `{"path":"/virtual/first"}`}),
		func(messages []llm.Message, defs []llm.ToolDefinition, chunk func(llm.StreamChunk)) *llm.Response {
			st.SetSelectedToolset("")
			return discoveryResponse(llm.ToolCall{ID: "read_before_reactivation", Name: "read", InputJSON: `{"path":"/virtual/next"}`})(messages, defs, chunk)
		},
		discoveryResponse(discoveryActivation("activate_again", "read")),
		discoveryResponse(llm.ToolCall{ID: "read_after_reactivation", Name: "read", InputJSON: `{"path":"/virtual/next"}`}), answerStep("done"),
	}
	discoveryRun(t, ag)
	if *count != 2 {
		t.Fatalf("removed activation survived or later legitimate read failed: %d", *count)
	}
	if len(p.offered[2]) != 0 || strings.Join(p.offered[3], ",") != "activate_tools" || !contains(p.offered[4], "read") {
		t.Fatalf("empty transition native offers: %v", p.offered)
	}
	if strings.Contains(p.requests[2][0].Content, "Lossy discovery summaries") {
		t.Fatal("empty transition kept discovery instructions")
	}
}
