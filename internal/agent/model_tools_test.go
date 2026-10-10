package agent

// Edge cases of models[].tools and models[].disallowed_tools (model_tools.go).
// The happy path - what a request carries, the system prompt's list and the
// refusal of a call outside the lists - is features/model_tool_restrictions.feature,
// run by bdd_model_tools_test.go.

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// modelToolsAgent is an agent over a hand-made session whose configuration
// holds the given models[] rows, the first of them the default; selected, when
// not empty, is the model the session picked.
func modelToolsAgent(t testing.TB, mode session.Mode, selected string, rows ...config.ModelEntry) (*Agent, *session.State, *recordingClient) {
	t.Helper()
	return modelToolsAgentMeta(t, mode, selected, nil, rows...)
}

// modelToolsAgentMeta is modelToolsAgent for a child: the subagent meta is on
// the state before the agent is built, as it is for a spawned run.
func modelToolsAgentMeta(t testing.TB, mode session.Mode, selected string, meta *session.SubagentMeta, rows ...config.ModelEntry) (*Agent, *session.State, *recordingClient) {
	t.Helper()
	for i := range rows {
		rows[i].MaxTokens = 100
		rows[i].Normalize()
	}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    rows,
		Agent:     config.Agent{Model: rows[0].Model, MaxTurns: 6},
	}
	st := &session.State{ID: "sess_model_tools_" + strings.ReplaceAll(t.Name(), "/", "_"), CWD: t.TempDir(), Mode: mode, SessionDir: t.TempDir()}
	if selected != "" {
		st.SetSelectedModelID(selected)
	}
	if meta != nil {
		st.SetSubagentMeta(*meta)
	}
	client := &recordingClient{answer: "allow"}
	return NewAgent(cfg, st, client, nil), st, client
}

func offeredNames(ag *Agent, mode string) []string {
	return sortedNames(ag.currentToolDefinitions(mode))
}

func toolDefs(names ...string) []llm.ToolDefinition {
	out := make([]llm.ToolDefinition, 0, len(names))
	for _, n := range names {
		out = append(out, llm.ToolDefinition{Name: n, Description: n})
	}
	return out
}

func TestModelToolListsApplyAllowThenDeny(t *testing.T) {
	defs := toolDefs("read", "write", "grep", "run_command", "ctx__search", "ctx__fetch", "other__x")
	cases := []struct {
		name  string
		entry config.ModelEntry
		want  string
	}{
		{"allowlist only", config.ModelEntry{Tools: []string{"read", "grep"}}, "read,grep"},
		{"denylist only", config.ModelEntry{DisallowedTools: []string{"write", "run_command"}}, "read,grep,ctx__search,ctx__fetch,other__x"},
		{"a name in both lists is out", config.ModelEntry{Tools: []string{"read", "write", "grep"}, DisallowedTools: []string{"write"}}, "read,grep"},
		{"a prefix pattern admits one server's tools", config.ModelEntry{Tools: []string{"read", "ctx__*"}}, "read,ctx__search,ctx__fetch"},
		{"the denylist narrows a prefix pattern", config.ModelEntry{Tools: []string{"ctx__*"}, DisallowedTools: []string{"ctx__fetch"}}, "ctx__search"},
		{"a bare star with a denylist", config.ModelEntry{Tools: []string{"*"}, DisallowedTools: []string{"run_command", "other__*"}}, "read,write,grep,ctx__search,ctx__fetch"},
		{"a deny star offers nothing", config.ModelEntry{DisallowedTools: []string{"*"}}, ""},
		{"an allowlist of names that are not tools offers nothing", config.ModelEntry{Tools: []string{"no_such_tool"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := tc.entry
			got := make([]string, 0)
			for _, d := range filterToolDefsForModel(defs, &entry) {
				got = append(got, d.Name)
			}
			if strings.Join(got, ",") != tc.want {
				t.Fatalf("offered = %v, want %q", got, tc.want)
			}
			// The execution-time check agrees with the definitions, name by name.
			for _, d := range defs {
				offered := strings.Contains(","+strings.Join(got, ",")+",", ","+d.Name+",")
				if modelToolsAllow(&entry, d.Name) != offered {
					t.Fatalf("modelToolsAllow(%q) = %v disagrees with the offered set %v", d.Name, !offered, got)
				}
			}
		})
	}
}

// Absent, empty and blank lists are the same thing: no restriction, and the
// request carries exactly the definitions it always did.
func TestModelToolListsEmptyMeansNoRestriction(t *testing.T) {
	blank := config.ModelEntry{Model: "fake/blank", Tools: []string{"", "  "}, DisallowedTools: []string{" "}}
	empty := config.ModelEntry{Model: "fake/empty", Tools: []string{}, DisallowedTools: []string{}}
	plain := config.ModelEntry{Model: "fake/plain"}
	ag, _, _ := modelToolsAgent(t, session.ModeAgent, "", plain, empty, blank)

	want := offeredNames(ag, "agent")
	if len(want) < 30 {
		t.Fatalf("the unrestricted set holds only %d tools: %v", len(want), want)
	}
	for _, model := range []string{"fake/plain", "fake/empty", "fake/blank"} {
		ag.state.(*session.State).SetSelectedModelID(model)
		if entry := ag.modelToolEntry(); entry != nil {
			t.Fatalf("%s: modelToolEntry = %+v, want none", model, entry)
		}
		if got := offeredNames(ag, "agent"); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s offers %v, want the unrestricted %v", model, got, want)
		}
		if _, refused := ag.toolCallRefusedByModel("write"); refused {
			t.Fatalf("%s refused a call although it lists nothing", model)
		}
	}
}

// A list that leaves nothing offers no tools, and the turn still runs.
func TestModelToolListsMayOfferNoToolAtAll(t *testing.T) {
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "", config.ModelEntry{Model: "fake/chat", DisallowedTools: []string{"*"}})
	provider := &bddModelToolsProvider{}
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "hello"}})
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("Run = %q, %v", stop, err)
	}
	if len(provider.offered) != 1 || len(provider.offered[0]) != 0 {
		t.Fatalf("the request carried tools %v, want none", provider.offered)
	}
	system := provider.seen[0][0].Content
	if strings.Contains(system, "## Available tools") {
		t.Fatalf("the system prompt still has a tools section:\n%s", system)
	}
	if msgs := st.GetMessages(); len(msgs) == 0 || msgs[len(msgs)-1].Content != bddModelToolsAnswer {
		t.Fatalf("the turn did not end with the answer: %+v", msgs)
	}
}

// The lists only narrow: a tool the mode does not offer stays out, whatever
// the model's allowlist names.
func TestModelToolListsNeverWidenTheMode(t *testing.T) {
	ag, _, _ := modelToolsAgent(t, session.ModeAsk, "", config.ModelEntry{Model: "fake/model", Tools: []string{"read", "write", "run_command", "no_such_tool"}})
	if got := offeredNames(ag, "ask"); strings.Join(got, ",") != "read" {
		t.Fatalf("ask mode with the allowlist read,write,run_command offers %v, want only read", got)
	}
	// Plan mode keeps its own shell and read tools; the allowlist picks within them.
	ag, _, _ = modelToolsAgent(t, session.ModePlan, "", config.ModelEntry{Model: "fake/model", Tools: []string{"read", "write", "run_command"}})
	if got := offeredNames(ag, "plan"); strings.Join(got, ",") != "read,run_command" {
		t.Fatalf("plan mode with the allowlist read,write,run_command offers %v, want read,run_command", got)
	}
}

// The model is read for every request: a switch moves the set, and the
// execution-time check follows it.
func TestModelToolListsFollowTheSessionsCurrentModel(t *testing.T) {
	small := config.ModelEntry{Model: "fake/small", Tools: []string{"read", "grep"}}
	big := config.ModelEntry{Model: "fake/big"}
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "fake/small", big, small)

	if got := offeredNames(ag, "agent"); strings.Join(got, ",") != "grep,read" {
		t.Fatalf("small model offers %v", got)
	}
	if _, refused := ag.toolCallRefusedByModel("write"); !refused {
		t.Fatal("a write call on the small model was not refused")
	}

	st.SetSelectedModelID("fake/big")
	if got := offeredNames(ag, "agent"); len(got) < 30 {
		t.Fatalf("after the switch to the model without lists the offer is %v", got)
	}
	if _, refused := ag.toolCallRefusedByModel("write"); refused {
		t.Fatal("a write call was refused on the model without lists")
	}

	st.SetSelectedModelID("fake/small")
	if got := offeredNames(ag, "agent"); strings.Join(got, ",") != "grep,read" {
		t.Fatalf("switching back offers %v", got)
	}
}

// MCP tools are named server__tool and obey the same patterns.
func TestModelToolListsCoverMCPTools(t *testing.T) {
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "", config.ModelEntry{Model: "fake/model", Tools: []string{"read", "srv__*"}, DisallowedTools: []string{"srv__other"}})
	st.AddSessionMCPClient(mcp.NewStaticClient("srv", []mcp.ToolInfo{{Name: "tool"}, {Name: "other"}}))
	st.AddSessionMCPClient(mcp.NewStaticClient("elsewhere", []mcp.ToolInfo{{Name: "tool"}}))
	if got := offeredNames(ag, "agent"); strings.Join(got, ",") != "read,srv__tool" {
		t.Fatalf("offered = %v, want read and srv__tool", got)
	}
	if _, refused := ag.toolCallRefusedByModel("srv__tool"); refused {
		t.Fatal("the allowed MCP tool was refused")
	}
	for _, name := range []string{"srv__other", "elsewhere__tool"} {
		if refusal, refused := ag.toolCallRefusedByModel(name); !refused || !strings.Contains(refusal, name) {
			t.Fatalf("%s: refusal = %q, %v", name, refusal, refused)
		}
	}
}

// A call outside the lists is refused before any hook or permission prompt, is
// recorded as cancelled, and a call inside them runs.
func TestModelToolListsRefuseACallBeforeAnyPermissionPrompt(t *testing.T) {
	ag, st, client := modelToolsAgent(t, session.ModeAgent, "", config.ModelEntry{Model: "fake/model", Tools: []string{"read"}})
	env := &tools.Env{CWD: st.CWD, PermissionMode: config.PermModeAsk, SessionID: st.ID, Sender: client}

	res, err := ag.executeToolCall(context.Background(), llm.ToolCall{ID: "call_w", Name: "write", InputJSON: `{"path":"x.txt","content":"x"}`}, env, "agent", st.ID, false)
	if err != nil {
		t.Fatalf("a refusal is a tool result, got error %v", err)
	}
	if !strings.Contains(res, `tool "write" is not available on model fake/model`) {
		t.Fatalf("result = %q", res)
	}
	if n := len(client.permissions()); n != 0 {
		t.Fatalf("the refused call raised %d permission prompt(s)", n)
	}
	cancelled := false
	client.mu.Lock()
	for _, u := range client.updates {
		if su, ok := u.(acp.ToolCallStatusUpdate); ok && su.ToolCallID == "call_w" && su.Status == "cancelled" {
			cancelled = true
		}
	}
	client.mu.Unlock()
	if !cancelled {
		t.Fatal("the refused call was not marked cancelled")
	}
	if _, statErr := filepath.Glob(filepath.Join(st.CWD, "x.txt")); statErr != nil {
		t.Fatal(statErr)
	}
	if matches, _ := filepath.Glob(filepath.Join(st.CWD, "x.txt")); len(matches) != 0 {
		t.Fatalf("the refused write created %v", matches)
	}
}

// A pending approval answered after the model changed must not run the call or
// leave an "allow always" grant behind for it.
func TestModelToolListsRefuseAResumedApproval(t *testing.T) {
	st := &session.State{
		ID:         "sess_resume_model_tools",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "run it"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_model_hidden", Name: "run_command", InputJSON: `{"command":"printf SHOULD_NOT_RUN"}`}}},
		},
	}
	provider := &resumePermissionProvider{t: t}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, DisallowedTools: []string{"run_command"}}},
		Agent:     config.Agent{Model: "fake/model"},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	if err := session.WriteToolCallArgs(st.SessionDir, "call_model_hidden", `{"command":"printf SHOULD_NOT_RUN"}`); err != nil {
		t.Fatal(err)
	}
	stop, err := ag.ResumeAfterPermission(context.Background(), "call_model_hidden", &acp.PermissionResult{Outcome: "allow", OptionID: "allow_always"})
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("resume = %q, %v", stop, err)
	}
	var result string
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == "call_model_hidden" {
			result = m.Content
		}
	}
	if !strings.Contains(result, `tool "run_command" is not available on model fake/model`) || strings.Contains(result, "SHOULD_NOT_RUN\n") {
		t.Fatalf("tool result = %q, want the model refusal", result)
	}
	if grants := st.GetPermissionCommandGrants(); len(grants) != 0 {
		t.Fatalf("the refused call recorded an allow-always grant: %v", grants)
	}
}

// The memory subagent is a system child whose tool set the runtime fixes; an
// allowlist written for the main loop must not strip it.
func TestModelToolListsLeaveSystemChildrenAlone(t *testing.T) {
	meta := session.SubagentMeta{Name: session.SubagentKindMemory, Kind: session.SubagentKindMemory, ParentSessionID: "sess_parent", TaskID: "bg_1", Depth: 1, Tools: []string{"read", "grep"}}
	ag, _, _ := modelToolsAgentMeta(t, session.ModeAgent, "", &meta, config.ModelEntry{Model: "fake/model", Tools: []string{"read"}})
	if entry := ag.modelToolEntry(); entry != nil {
		t.Fatalf("a system child is bound by the model's lists: %+v", entry)
	}
	if got := offeredNames(ag, "agent"); strings.Join(got, ",") != "grep,read" {
		t.Fatalf("the system child offers %v, want its own set grep,read", got)
	}
}

// A spawned child that is not a system child is bound by the lists of the
// model it runs on, on top of its effective set.
func TestModelToolListsNarrowAChildOnTopOfItsEffectiveSet(t *testing.T) {
	meta := session.SubagentMeta{Name: "explore", ParentSessionID: "sess_parent", TaskID: "bg_1", Depth: 1, Tools: []string{"read", "grep", "write", "glob"}}
	ag, _, _ := modelToolsAgentMeta(t, session.ModeAgent, "", &meta, config.ModelEntry{Model: "fake/model", Tools: []string{"read", "grep"}})
	if got := offeredNames(ag, "agent"); strings.Join(got, ",") != "grep,read" {
		t.Fatalf("the child offers %v, want the intersection grep,read", got)
	}
	// The child's own gate still refuses what its set leaves out; the model's
	// refuses what its lists do.
	if ag.subagentAllows("run_command") {
		t.Fatal("subagentAllows admitted a tool outside the child's set")
	}
	if _, refused := ag.toolCallRefused("agent", "glob"); !refused {
		t.Fatal("glob is in the child's set but not in the model's lists, and was not refused")
	}
	if _, refused := ag.toolCallRefused("agent", "read"); refused {
		t.Fatal("read is in both and was refused")
	}
}

// The parts of the system prompt that describe a tool follow the lists: a
// model that is not offered load_skill is not told to call it.
func TestModelToolListsDropTheSkillHintFromThePrompt(t *testing.T) {
	sk := &skills.Skill{Name: "SKILL", FilePath: filepath.Join("skills", "code-review", "SKILL.md"), Description: "review code", Content: "body"}
	hide := config.ModelEntry{Model: "fake/hidden", DisallowedTools: []string{"load_skill"}}
	keep := config.ModelEntry{Model: "fake/kept"}
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "fake/kept", keep, hide)
	st.ReplaceSkills([]*skills.Skill{sk})

	if p := ag.buildSystemPrompt("agent", nil, ag.currentToolDefinitions("agent")); !strings.Contains(p, "load_skill") {
		t.Fatalf("the model that keeps load_skill is not told about it:\n%s", p)
	}
	st.SetSelectedModelID("fake/hidden")
	defs := ag.currentToolDefinitions("agent")
	if p := ag.buildSystemPrompt("agent", nil, defs); strings.Contains(p, "load_skill") {
		t.Fatalf("the model without load_skill is still told to call it:\n%s", p)
	}
}

// The lists are read for the model the spawned child runs on, whichever model
// that is: the parent's lists bound what the parent is shown, not the child.
func TestModelToolListsBindASpawnedChildByItsOwnModel(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) {
		cfg.Models = []config.ModelEntry{
			{Model: "fake/model", MaxTokens: 100},
			{Model: "fake/small", MaxTokens: 100, Tools: []string{"read", "grep", "spawn_agent"}},
		}
	})
	rig.approvedDefinition("on-small", "model: fake/small\ntools: read, grep, write\n")
	rig.approvedDefinition("on-big", "model: fake/model\ntools: read, grep, write\n")
	rig.approvedDefinition("inherits", "tools: read, grep, write\n")

	spawn := func(parentModel, agent string) []string {
		t.Helper()
		rig.parent.SetSelectedModelID(parentModel)
		before := len(rig.childStates())
		if _, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq(agent)); err != nil {
			t.Fatalf("spawn %s: %v", agent, err)
		}
		children := rig.childStates()
		if len(children) != before+1 {
			t.Fatalf("spawn %s started %d children", agent, len(children)-before)
		}
		offered := rig.childProviderOf(children[len(children)-1]).offered
		if len(offered) == 0 {
			t.Fatalf("spawn %s: the child never called its model", agent)
		}
		got := append([]string(nil), offered[0]...)
		sort.Strings(got)
		return got
	}

	// The child's model is the small one: write is cut from its set.
	if got := spawn("fake/model", "on-small"); strings.Join(got, ",") != "grep,read" {
		t.Fatalf("child on the small model offered %v, want grep,read", got)
	}
	// The child inherits the small parent's model, so it is cut the same way.
	if got := spawn("fake/small", "inherits"); strings.Join(got, ",") != "grep,read" {
		t.Fatalf("child inheriting the small model offered %v, want grep,read", got)
	}
	// A small parent may hand work to a child on a model without lists, which
	// then has the definition's whole set.
	if got := spawn("fake/small", "on-big"); strings.Join(got, ",") != "grep,read,write" {
		t.Fatalf("child on the unrestricted model offered %v, want grep,read,write", got)
	}
}

// A model that leaves a child nothing is refused up front, naming the model.
func TestModelToolListsRefuseASpawnThatLeavesNoTools(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) {
		cfg.Models = []config.ModelEntry{
			{Model: "fake/model", MaxTokens: 100},
			{Model: "fake/small", MaxTokens: 100, Tools: []string{"read", "grep"}},
		}
	})
	rig.approvedDefinition("writer", "model: fake/small\ntools: write, edit\n")
	before := subagentLimiter.InFlight()
	_, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("writer"))
	if err == nil || !strings.Contains(err.Error(), "would have no tools at all on model fake/small") {
		t.Fatalf("error = %v, want a refusal naming the model", err)
	}
	if got := subagentLimiter.InFlight(); got != before {
		t.Fatalf("limiter in flight = %d, want %d", got, before)
	}
	if tasks := rig.agentTasks(); len(tasks) != 0 {
		t.Fatalf("a refused spawn left agent tasks behind: %+v", tasks)
	}
}

// The roster of subagents is shown only to a model that is offered spawn_agent.
func TestModelToolListsDropTheSubagentRosterWhenSpawnIsHidden(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) {
		cfg.Models = []config.ModelEntry{
			{Model: "fake/model", MaxTokens: 100},
			{Model: "fake/small", MaxTokens: 100, Tools: []string{"read", "grep"}},
		}
	})
	rig.approvedDefinition("helper", "")
	rig.parent.SetSelectedModelID("fake/model")
	if block := rig.parentAgent().subagentCatalogBlock(); !strings.Contains(block, "helper") {
		t.Fatalf("the unrestricted model is not shown the roster: %q", block)
	}
	rig.parent.SetSelectedModelID("fake/small")
	if block := rig.parentAgent().subagentCatalogBlock(); block != "" {
		t.Fatalf("the model without spawn_agent is shown the roster: %q", block)
	}
}

// switchingProvider moves the session to another model while its first reply
// is being produced, the way the model's own switch_model or an operator's
// selector lands in the middle of a turn.
type switchingProvider struct {
	bddModelToolsProvider
	st *session.State
	to string
}

func (p *switchingProvider) Stream(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	if p.calls == 0 {
		p.st.SetSelectedModelID(p.to)
	}
	return p.bddModelToolsProvider.Stream(ctx, messages, defs, onChunk)
}

// A switch to a model with lists, in the middle of a turn, takes effect from
// the next request: its tool list and the system message that lists the tools.
func TestModelToolListsTakeEffectFromTheRequestAfterAMidTurnSwitch(t *testing.T) {
	big := config.ModelEntry{Model: "fake/big"}
	small := config.ModelEntry{Model: "fake/small", Tools: []string{"read", "grep"}}
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "fake/big", big, small)
	provider := &switchingProvider{st: st, to: "fake/small"}
	provider.toolCall = &llm.ToolCall{ID: "call_read", Name: "read", InputJSON: `{"path":"missing.txt"}`}
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "look around"}})
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("Run = %q, %v", stop, err)
	}
	if len(provider.offered) != 2 {
		t.Fatalf("the model was called %d times, want 2", len(provider.offered))
	}
	if first := sortedNames(provider.offered[0]); len(first) < 30 {
		t.Fatalf("the first request, on the model without lists, carried %v", first)
	}
	if second := sortedNames(provider.offered[1]); strings.Join(second, ",") != "grep,read" {
		t.Fatalf("the request after the switch carried %v, want grep,read", second)
	}
	if listed := promptToolNames(provider.seen[1][0].Content); strings.Join(listed, ",") != "grep,read" {
		t.Fatalf("the system message after the switch lists %v, want grep,read", listed)
	}
	if listed := promptToolNames(provider.seen[0][0].Content); len(listed) < 30 {
		t.Fatalf("the first system message lists only %v", listed)
	}
}
