package agent

// Edge cases of the toolsets[] presets a session is offered its tools by
// (toolsets.go). The happy path - what a request carries, the system prompt's
// list, the refusal of a call outside the preset, a session's own pick - is
// features/toolsets.feature, run by bdd_toolsets_test.go.

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// toolRow is a models[] row with the lists of the toolset it names, for the
// tests: toolRows gives a row with lists a toolset of its own, named after the
// model, and points the row at it.
type toolRow struct {
	Model           string
	MaxTokens       int
	ReasoningLevels *[]string
	Tools           []string
	DisallowedTools []string
}

func toolRows(rows ...toolRow) ([]config.ModelEntry, []config.Toolset) {
	var models []config.ModelEntry
	var sets []config.Toolset
	for _, r := range rows {
		m := config.ModelEntry{Model: r.Model, MaxTokens: r.MaxTokens, ReasoningLevels: r.ReasoningLevels}
		if r.Tools != nil || r.DisallowedTools != nil {
			ts := config.Toolset{Name: "set-" + strings.ReplaceAll(r.Model, "/", "-"), Tools: r.Tools, DisallowedTools: r.DisallowedTools}
			ts.Normalize()
			sets = append(sets, ts)
			m.Toolset = ts.Name
		}
		models = append(models, m)
	}
	return models, sets
}

// modelToolsAgent is an agent over a hand-made session whose configuration
// holds the given models[] rows, the first of them the default; selected, when
// not empty, is the model the session picked.
func modelToolsAgent(t testing.TB, mode session.Mode, selected string, rows ...toolRow) (*Agent, *session.State, *recordingClient) {
	t.Helper()
	return modelToolsAgentMeta(t, mode, selected, nil, rows...)
}

// modelToolsAgentMeta is modelToolsAgent for a child: the subagent meta is on
// the state before the agent is built, as it is for a spawned run.
func modelToolsAgentMeta(t testing.TB, mode session.Mode, selected string, meta *session.SubagentMeta, rows ...toolRow) (*Agent, *session.State, *recordingClient) {
	t.Helper()
	for i := range rows {
		rows[i].MaxTokens = 100
	}
	models, sets := toolRows(rows...)
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    models,
		Toolsets:  sets,
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
		entry toolRow
		want  string
	}{
		{"allowlist only", toolRow{Tools: []string{"read", "grep"}}, "read,grep"},
		{"denylist only", toolRow{DisallowedTools: []string{"write", "run_command"}}, "read,grep,ctx__search,ctx__fetch,other__x"},
		{"a name in both lists is out", toolRow{Tools: []string{"read", "write", "grep"}, DisallowedTools: []string{"write"}}, "read,grep"},
		{"a prefix pattern admits one server's tools", toolRow{Tools: []string{"read", "ctx__*"}}, "read,ctx__search,ctx__fetch"},
		{"the denylist narrows a prefix pattern", toolRow{Tools: []string{"ctx__*"}, DisallowedTools: []string{"ctx__fetch"}}, "ctx__search"},
		{"a bare star with a denylist", toolRow{Tools: []string{"*"}, DisallowedTools: []string{"run_command", "other__*"}}, "read,write,grep,ctx__search,ctx__fetch"},
		{"a deny star offers nothing", toolRow{DisallowedTools: []string{"*"}}, ""},
		{"an allowlist of names that are not tools offers nothing", toolRow{Tools: []string{"no_such_tool"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := &config.Toolset{Name: "t", Tools: tc.entry.Tools, DisallowedTools: tc.entry.DisallowedTools}
			got := make([]string, 0)
			for _, d := range filterToolDefsByToolset(defs, ts) {
				got = append(got, d.Name)
			}
			if strings.Join(got, ",") != tc.want {
				t.Fatalf("offered = %v, want %q", got, tc.want)
			}
			// The execution-time check agrees with the definitions, name by name.
			for _, d := range defs {
				offered := strings.Contains(","+strings.Join(got, ",")+",", ","+d.Name+",")
				if toolsetAllows(ts, d.Name) != offered {
					t.Fatalf("toolsetAllows(%q) = %v disagrees with the offered set %v", d.Name, !offered, got)
				}
			}
		})
	}
}

// Absent, empty and blank lists are the same thing: no restriction, and the
// request carries exactly the definitions it always did.
func TestModelToolListsEmptyMeansNoRestriction(t *testing.T) {
	blank := toolRow{Model: "fake/blank", Tools: []string{"", "  "}, DisallowedTools: []string{" "}}
	empty := toolRow{Model: "fake/empty", Tools: []string{}, DisallowedTools: []string{}}
	plain := toolRow{Model: "fake/plain"}
	ag, _, _ := modelToolsAgent(t, session.ModeAgent, "", plain, empty, blank)

	want := offeredNames(ag, "agent")
	if len(want) < 30 {
		t.Fatalf("the unrestricted set holds only %d tools: %v", len(want), want)
	}
	for _, model := range []string{"fake/plain", "fake/empty", "fake/blank"} {
		ag.state.(*session.State).SetSelectedModelID(model)
		if entry := ag.activeToolset(); entry != nil {
			t.Fatalf("%s: modelToolEntry = %+v, want none", model, entry)
		}
		if got := offeredNames(ag, "agent"); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s offers %v, want the unrestricted %v", model, got, want)
		}
		if _, refused := ag.toolCallRefusedByToolset("write"); refused {
			t.Fatalf("%s refused a call although it lists nothing", model)
		}
	}
}

// A list that leaves nothing offers no tools, and the turn still runs.
func TestModelToolListsMayOfferNoToolAtAll(t *testing.T) {
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "", toolRow{Model: "fake/chat", DisallowedTools: []string{"*"}})
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
	ag, _, _ := modelToolsAgent(t, session.ModeAsk, "", toolRow{Model: "fake/model", Tools: []string{"read", "write", "run_command", "no_such_tool"}})
	if got := offeredNames(ag, "ask"); strings.Join(got, ",") != "read" {
		t.Fatalf("ask mode with the allowlist read,write,run_command offers %v, want only read", got)
	}
	// Plan mode keeps its own shell and read tools; the allowlist picks within them.
	ag, _, _ = modelToolsAgent(t, session.ModePlan, "", toolRow{Model: "fake/model", Tools: []string{"read", "write", "run_command"}})
	if got := offeredNames(ag, "plan"); strings.Join(got, ",") != "read,run_command" {
		t.Fatalf("plan mode with the allowlist read,write,run_command offers %v, want read,run_command", got)
	}
}

// The model is read for every request: a switch moves the set, and the
// execution-time check follows it.
func TestModelToolListsFollowTheSessionsCurrentModel(t *testing.T) {
	small := toolRow{Model: "fake/small", Tools: []string{"read", "grep"}}
	big := toolRow{Model: "fake/big"}
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "fake/small", big, small)

	if got := offeredNames(ag, "agent"); strings.Join(got, ",") != "grep,read" {
		t.Fatalf("small model offers %v", got)
	}
	if _, refused := ag.toolCallRefusedByToolset("write"); !refused {
		t.Fatal("a write call on the small model was not refused")
	}

	st.SetSelectedModelID("fake/big")
	if got := offeredNames(ag, "agent"); len(got) < 30 {
		t.Fatalf("after the switch to the model without lists the offer is %v", got)
	}
	if _, refused := ag.toolCallRefusedByToolset("write"); refused {
		t.Fatal("a write call was refused on the model without lists")
	}

	st.SetSelectedModelID("fake/small")
	if got := offeredNames(ag, "agent"); strings.Join(got, ",") != "grep,read" {
		t.Fatalf("switching back offers %v", got)
	}
}

// MCP tools are named server__tool and obey the same patterns.
func TestModelToolListsCoverMCPTools(t *testing.T) {
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "", toolRow{Model: "fake/model", Tools: []string{"read", "srv__*"}, DisallowedTools: []string{"srv__other"}})
	st.AddSessionMCPClient(mcp.NewStaticClient("srv", []mcp.ToolInfo{{Name: "tool"}, {Name: "other"}}))
	st.AddSessionMCPClient(mcp.NewStaticClient("elsewhere", []mcp.ToolInfo{{Name: "tool"}}))
	if got := offeredNames(ag, "agent"); strings.Join(got, ",") != "read,srv__tool" {
		t.Fatalf("offered = %v, want read and srv__tool", got)
	}
	if _, refused := ag.toolCallRefusedByToolset("srv__tool"); refused {
		t.Fatal("the allowed MCP tool was refused")
	}
	for _, name := range []string{"srv__other", "elsewhere__tool"} {
		if refusal, refused := ag.toolCallRefusedByToolset(name); !refused || !strings.Contains(refusal, name) {
			t.Fatalf("%s: refusal = %q, %v", name, refusal, refused)
		}
	}
}

// A call outside the lists is refused before any hook or permission prompt, is
// recorded as cancelled, and a call inside them runs.
func TestModelToolListsRefuseACallBeforeAnyPermissionPrompt(t *testing.T) {
	ag, st, client := modelToolsAgent(t, session.ModeAgent, "", toolRow{Model: "fake/model", Tools: []string{"read"}})
	env := &tools.Env{CWD: st.CWD, PermissionMode: config.PermModeAsk, SessionID: st.ID, Sender: client}

	res, err := ag.executeToolCall(context.Background(), llm.ToolCall{ID: "call_w", Name: "write", InputJSON: `{"path":"x.txt","content":"x"}`}, env, "agent", st.ID, false)
	if err != nil {
		t.Fatalf("a refusal is a tool result, got error %v", err)
	}
	if !strings.Contains(res, `tool "write" is not offered: this session runs with the toolset "set-fake-model"`) {
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
	models, sets := toolRows(toolRow{Model: "fake/model", MaxTokens: 100, DisallowedTools: []string{"run_command"}})
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    models,
		Toolsets:  sets,
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
	if !strings.Contains(result, `tool "run_command" is not offered: this session runs with the toolset "set-fake-model"`) || strings.Contains(result, "SHOULD_NOT_RUN\n") {
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
	ag, _, _ := modelToolsAgentMeta(t, session.ModeAgent, "", &meta, toolRow{Model: "fake/model", Tools: []string{"read"}})
	if entry := ag.activeToolset(); entry != nil {
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
	ag, _, _ := modelToolsAgentMeta(t, session.ModeAgent, "", &meta, toolRow{Model: "fake/model", Tools: []string{"read", "grep"}})
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
	hide := toolRow{Model: "fake/hidden", DisallowedTools: []string{"load_skill"}}
	keep := toolRow{Model: "fake/kept"}
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
		cfg.Models, cfg.Toolsets = toolRows(
			toolRow{Model: "fake/model", MaxTokens: 100},
			toolRow{Model: "fake/small", MaxTokens: 100, Tools: []string{"read", "grep", "spawn_agent"}},
		)
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
		cfg.Models, cfg.Toolsets = toolRows(
			toolRow{Model: "fake/model", MaxTokens: 100},
			toolRow{Model: "fake/small", MaxTokens: 100, Tools: []string{"read", "grep"}},
		)
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
		cfg.Models, cfg.Toolsets = toolRows(
			toolRow{Model: "fake/model", MaxTokens: 100},
			toolRow{Model: "fake/small", MaxTokens: 100, Tools: []string{"read", "grep"}},
		)
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
	big := toolRow{Model: "fake/big"}
	small := toolRow{Model: "fake/small", Tools: []string{"read", "grep"}}
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

// The offer a request carried and the check a call meets agree (toolsets.go),
// whatever the session does between them: a model switched to mid-turn or in
// the middle of a batch, a provider that cannot be built, an MCP set that
// changed. And a run that starts without a parent - a spawn, a scheduled job -
// is judged against the lists of the model it runs on before anything is dialed.

func joined(names []string) string { return strings.Join(names, ",") }

// A switch between models that carry no lists on either side leaves the system
// message and the tool list exactly as the turn began with them, even when the
// MCP set moved meanwhile: the rebuild belongs to the lists, not to every switch.
func TestModelToolListsMidTurnSwitchWithoutListsKeepsTheRequestAsIs(t *testing.T) {
	levels := []string{"low", "high"}
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "", toolRow{Model: "fake/model", ReasoningLevels: &levels})
	provider := scripted(
		func(_ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
			// An MCP server comes up and the reasoning level changes while the
			// first reply is produced: the transport is rebuilt at the next step.
			st.AddSessionMCPClient(mcp.NewStaticClient("srv", []mcp.ToolInfo{{Name: "tool"}}))
			st.SetSelectedReasoning("high")
			return toolStep(llm.ToolCall{ID: "call_read", Name: "read", InputJSON: `{"path":"missing.txt"}`})(nil, nil, onChunk)
		},
		answerStep("done"),
	)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "look around"}})
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("Run = %q, %v", stop, err)
	}
	if len(provider.offered) != 2 {
		t.Fatalf("the model was called %d times, want 2", len(provider.offered))
	}
	if joined(provider.offered[0]) != joined(provider.offered[1]) {
		t.Fatalf("the tool list moved with the switch:\nfirst  %v\nsecond %v", provider.offered[0], provider.offered[1])
	}
	for _, name := range provider.offered[1] {
		if strings.Contains(name, "__") {
			t.Fatalf("the MCP tool %s reached a request the switch only re-keyed", name)
		}
	}
	if first, second := provider.requests[0][0].Content, provider.requests[1][0].Content; first != second {
		t.Fatalf("the system message was rendered again by a switch that moved no tool list")
	}
}

// When the provider of the model switched to cannot be built, the turn goes on
// with the transport it has, and a call is checked against the tools that
// transport's request offered, not against the model the state now names.
func TestModelToolListsRefusalFollowsTheTransportWhenTheSwitchCannotBeBuilt(t *testing.T) {
	big := toolRow{Model: "fake/big"}
	small := toolRow{Model: "fake/small", Tools: []string{"read"}}
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "fake/big", big, small)
	target := filepath.Join(st.CWD, "notes.md")
	provider := scripted(
		func(_ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
			st.SetSelectedModelID("fake/small")
			call := llm.ToolCall{ID: "call_write", Name: "write", InputJSON: `{"path":` + quoteJSON(target) + `,"content":"hello"}`}
			return toolStep(call)(nil, nil, onChunk)
		},
		answerStep("done"),
	)
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		if in.Model == "small" {
			return nil, os.ErrNotExist
		}
		return provider, nil
	}

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "write it"}})
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("Run = %q, %v", stop, err)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "hello" {
		t.Fatalf("the write offered to the model did not run: %q, %v", body, err)
	}
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "is not offered") {
			t.Fatalf("a tool the request offered was refused: %q", m.Content)
		}
	}
	if len(provider.offered) != 2 || joined(provider.offered[0]) != joined(provider.offered[1]) {
		t.Fatalf("offers = %v, want the same set on both requests (the switch was not built)", provider.offered)
	}
	// Once the offer is over the lists of the state's model apply again.
	if _, refused := ag.toolCallRefusedByToolset("write"); !refused {
		t.Fatal("after the turn the small model's lists no longer apply")
	}
}

// A batch that holds a switch_model: each call is decided against the tools the
// response that produced the batch was offered, so the write that follows the
// switch still runs, and the next request carries the new model's set.
func TestModelToolListsABatchWithASwitchIsJudgedByItsOwnOffer(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) {
		cfg.Models, cfg.Toolsets = toolRows(
			toolRow{Model: "fake/big", MaxTokens: 100},
			toolRow{Model: "fake/small", MaxTokens: 100, Tools: []string{"read", "grep"}},
		)
		cfg.Agent.Model = "fake/big"
	})
	rig.parent.SetSelectedModelID("fake/big")
	target := filepath.Join(rig.cwd, "batch.md")
	provider := scripted(
		toolStep(
			llm.ToolCall{ID: "call_switch", Name: "switch_model", InputJSON: `{"model":"fake/small"}`},
			llm.ToolCall{ID: "call_write", Name: "write", InputJSON: `{"path":` + quoteJSON(target) + `,"content":"batch"}`},
		),
		answerStep("done"),
	)
	ag := NewAgent(rig.cfg, rig.parent, rig.client, slog.Default())
	ag.SetSubagentRuntime(rig.mgr)
	ag.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return provider, nil })

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "switch, then write"}})
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("Run = %q, %v", stop, err)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "batch" {
		t.Fatalf("the write that followed the switch did not run: %q, %v", body, err)
	}
	var writeResult string
	for _, m := range rig.parent.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == "call_write" {
			writeResult = m.Content
		}
	}
	if writeResult == "" || strings.Contains(writeResult, "is not offered") {
		t.Fatalf("the write was answered with %q", writeResult)
	}
	if len(provider.offered) != 2 {
		t.Fatalf("the model was called %d times, want 2", len(provider.offered))
	}
	if !contains(provider.offered[0], "write") || !contains(provider.offered[0], "switch_model") {
		t.Fatalf("the first request did not offer write and switch_model: %v", provider.offered[0])
	}
	if got := joined(provider.offered[1]); got != "read,grep" && got != "grep,read" {
		t.Fatalf("the request after the switch carried %v, want the small model's read and grep", provider.offered[1])
	}
	// The next batch is judged by the new offer.
	if _, refused := ag.toolCallRefusedByToolset("write"); !refused {
		t.Fatal("after the turn a write on the small model was not refused")
	}
}

// A config_commit that gives the model the turn runs on a toolset takes
// effect from the next request, as a switch does: a call later in the batch
// that committed is judged by the offer that batch was made under, and the next
// request carries the new set with a system message listing the same tools.
func TestModelToolListsConfigReloadTakesEffectFromTheNextRequest(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	body := "providers:\n  - name: fake\n    type: openai\n    api_key: test\n" +
		"models:\n  - model: fake/model\n    max_tokens: 100\n" +
		"toolsets:\n  - name: lean\n    tools: [read, grep]\n" +
		"agent:\n  model: fake/model\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "sess_model_tools_reload", CWD: dir, Mode: session.ModeAgent, SessionDir: t.TempDir()}
	target := filepath.Join(dir, "batch.md")
	provider := scripted(
		toolStep(llm.ToolCall{ID: "call_set", Name: "config_set",
			InputJSON: `{"commands":["set models[model=fake/model].toolset=lean"]}`}),
		toolStep(
			llm.ToolCall{ID: "call_commit", Name: "config_commit", InputJSON: `{}`},
			llm.ToolCall{ID: "call_write", Name: "write", InputJSON: `{"path":` + quoteJSON(target) + `,"content":"batch"}`},
		),
		answerStep("done"),
	)
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.SetConfigReloader(func(context.Context) ([]string, error) { return nil, nil })
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "narrow the tools, then write"}})
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("Run = %q, %v", stop, err)
	}
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == "call_commit" && strings.HasPrefix(m.Content, "error") {
			t.Fatalf("config_commit failed: %q", m.Content)
		}
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "batch" {
		t.Fatalf("the write the batch was offered did not run after the commit: %q, %v", b, err)
	}
	if len(provider.offered) != 3 {
		t.Fatalf("the model was called %d times, want 3", len(provider.offered))
	}
	if !contains(provider.offered[1], "write") {
		t.Fatalf("the batch that committed was not offered write: %v", provider.offered[1])
	}
	if got := joined(provider.offered[2]); got != "read,grep" && got != "grep,read" {
		t.Fatalf("the request after the commit carried %v, want read and grep", provider.offered[2])
	}
	system := provider.requests[2][0]
	if system.Role != llm.RoleSystem {
		t.Fatalf("the request after the commit starts with %s", system.Role)
	}
	if got := joined(promptToolNames(system.Content)); got != "grep,read" {
		t.Fatalf("the system message after the commit lists %s, want the tools the request carries", got)
	}
}

// spyRuntime records what the agent asks the session manager to create.
type spyRuntime struct {
	SubagentRuntime
	mu    sync.Mutex
	specs []session.SubagentSpec
	errs  []error
}

func (s *spyRuntime) CreateSubagentSession(ctx context.Context, spec session.SubagentSpec) (*session.State, error) {
	st, err := s.SubagentRuntime.CreateSubagentSession(ctx, spec)
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	s.errs = append(s.errs, err)
	s.mu.Unlock()
	return st, err
}

func (s *spyRuntime) last(t *testing.T) (session.SubagentSpec, error) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.specs) == 0 {
		t.Fatal("no child session was asked for")
	}
	return s.specs[len(s.specs)-1], s.errs[len(s.errs)-1]
}

func scheduledRun(t *testing.T, rig *subagentRig, spy *spyRuntime, def *subagents.Definition, servers []string) {
	t.Helper()
	runID := session.NewSessionID()
	snap, err := RunScheduledJob(context.Background(), rig.cfg, spy, bgtask.Default(), slog.Default(), ScheduledRunSpec{
		JobID:          "nightly",
		JobSessionID:   rig.parent.ID,
		JobSessionDir:  rig.parent.GetPersistedSessionDir(),
		RunSessionID:   runID,
		Label:          "nightly (manual)",
		Trigger:        "manual",
		CWD:            rig.cwd,
		Mode:           "agent",
		Instruction:    "Check the build and report.",
		Definition:     def,
		MCPServerNames: servers,
	})
	if err != nil {
		t.Fatalf("RunScheduledJob: %v", err)
	}
	if _, err := bgtask.Default().Wait(context.Background(), rig.parent.ID, snap.ID, 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

// A scheduled run whose model leaves it no tool is refused where its tool set
// is decided, and the refusal names the model.
func TestModelToolListsRefuseAScheduledRunTheModelLeavesNoTools(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) {
		cfg.Models, cfg.Toolsets = toolRows(toolRow{Model: "fake/model", MaxTokens: 100, Tools: []string{"read"}})
	})
	rig.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("REPORT: done")) })
	def, err := subagents.Parse("nightly.md", []byte("---\ndescription: nightly check\ntools: write, edit\n---\nCheck the build.\n"))
	if err != nil {
		t.Fatal(err)
	}
	spy := &spyRuntime{SubagentRuntime: rig.mgr}
	scheduledRun(t, rig, spy, def, nil)
	_, createErr := spy.last(t)
	if createErr == nil || !strings.Contains(createErr.Error(), "would have no tools at all") || !strings.Contains(createErr.Error(), "fake/model") {
		t.Fatalf("creation error = %v, want a refusal that names fake/model", createErr)
	}

	// The same definition on a model without lists runs.
	rig2 := newSubagentRig(t, nil)
	rig2.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("REPORT: done")) })
	spy2 := &spyRuntime{SubagentRuntime: rig2.mgr}
	scheduledRun(t, rig2, spy2, def, nil)
	if _, createErr := spy2.last(t); createErr != nil {
		t.Fatalf("the model without lists was refused: %v", createErr)
	}
}

// The session's own pick of a toolset wins over the one its model names:
// another preset, or default for none.
func TestToolsetSessionPickWinsOverTheModels(t *testing.T) {
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "", toolRow{Model: "fake/small", Tools: []string{"read", "grep"}})
	ag.cfg.Toolsets = append(ag.cfg.Toolsets, config.Toolset{Name: "writer", Tools: []string{"read", "write"}})
	if got := joined(offeredNames(ag, "agent")); got != "grep,read" {
		t.Fatalf("the model's own toolset offers %s", got)
	}
	st.SetSelectedToolset("writer")
	if got := joined(offeredNames(ag, "agent")); got != "read,write" {
		t.Fatalf("the session's pick offers %s", got)
	}
	if refusal, refused := ag.toolCallRefusedByToolset("grep"); !refused || !strings.Contains(refusal, `toolset "writer"`) {
		t.Fatalf("a tool outside the pick: %q, %v", refusal, refused)
	}
	st.SetSelectedToolset("default")
	if got := offeredNames(ag, "agent"); len(got) < 20 || ag.activeToolset() != nil {
		t.Fatalf("default still narrows the set: %v", got)
	}
}

// A toolset picked while a turn runs (another tab, an editor) takes effect
// from the next request, and the system message lists the new set.
func TestToolsetPickedMidTurnTakesEffectFromTheNextRequest(t *testing.T) {
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "", toolRow{Model: "fake/model"})
	ag.cfg.Toolsets = []config.Toolset{{Name: "lean", Tools: []string{"read", "grep"}}}
	provider := scripted(
		func(_ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
			st.SetSelectedToolset("lean")
			return toolStep(llm.ToolCall{ID: "call_read", Name: "read", InputJSON: `{"path":"missing.txt"}`})(nil, nil, onChunk)
		},
		answerStep("done"),
	)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	if stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "look"}}); err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("Run = %q, %v", stop, err)
	}
	if len(provider.offered) != 2 || len(provider.offered[0]) < 20 {
		t.Fatalf("offers = %v", provider.offered)
	}
	if got := joined(provider.offered[1]); got != "read,grep" && got != "grep,read" {
		t.Fatalf("the request after the pick carried %v", provider.offered[1])
	}
	if got := joined(promptToolNames(provider.requests[1][0].Content)); got != "grep,read" {
		t.Fatalf("the system message after the pick lists %s", got)
	}
}
