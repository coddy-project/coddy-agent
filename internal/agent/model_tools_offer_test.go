package agent

// The offer a request carried and the check a call meets agree (model_tools.go),
// whatever the session does between them: a model switched to mid-turn or in
// the middle of a batch, a provider that cannot be built, an MCP set that
// changed. And a run that starts without a parent - a spawn, a scheduled job -
// is judged against the lists of the model it runs on before anything is dialed.

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
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
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
)

func joined(names []string) string { return strings.Join(names, ",") }

// A switch between models that carry no lists on either side leaves the system
// message and the tool list exactly as the turn began with them, even when the
// MCP set moved meanwhile: the rebuild belongs to the lists, not to every switch.
func TestModelToolListsMidTurnSwitchWithoutListsKeepsTheRequestAsIs(t *testing.T) {
	levels := []string{"low", "high"}
	ag, st, _ := modelToolsAgent(t, session.ModeAgent, "", config.ModelEntry{Model: "fake/model", ReasoningLevels: &levels})
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
	big := config.ModelEntry{Model: "fake/big"}
	small := config.ModelEntry{Model: "fake/small", Tools: []string{"read"}}
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
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "is not available on model") {
			t.Fatalf("a tool the request offered was refused: %q", m.Content)
		}
	}
	if len(provider.offered) != 2 || joined(provider.offered[0]) != joined(provider.offered[1]) {
		t.Fatalf("offers = %v, want the same set on both requests (the switch was not built)", provider.offered)
	}
	// Once the offer is over the lists of the state's model apply again.
	if _, refused := ag.toolCallRefusedByModel("write"); !refused {
		t.Fatal("after the turn the small model's lists no longer apply")
	}
}

// A batch that holds a switch_model: each call is decided against the tools the
// response that produced the batch was offered, so the write that follows the
// switch still runs, and the next request carries the new model's set.
func TestModelToolListsABatchWithASwitchIsJudgedByItsOwnOffer(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) {
		cfg.Models = []config.ModelEntry{
			{Model: "fake/big", MaxTokens: 100},
			{Model: "fake/small", MaxTokens: 100, Tools: []string{"read", "grep"}},
		}
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
	if writeResult == "" || strings.Contains(writeResult, "is not available on model") {
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
	if _, refused := ag.toolCallRefusedByModel("write"); !refused {
		t.Fatal("after the turn a write on the small model was not refused")
	}
}

// modelMayAdmitServer decides, before a server's tools exist, whether a model's
// lists could ever admit one of them.
func TestModelMayAdmitServer(t *testing.T) {
	cases := []struct {
		name  string
		entry *config.ModelEntry
		want  bool
	}{
		{"no entry", nil, true},
		{"no lists", &config.ModelEntry{Model: "m"}, true},
		{"an allowlist of built-ins", &config.ModelEntry{Tools: []string{"read", "grep"}}, false},
		{"the server's wildcard", &config.ModelEntry{Tools: []string{"read", "srv__*"}}, true},
		{"an exact tool of the server", &config.ModelEntry{Tools: []string{"srv__tool"}}, true},
		{"an exact tool of another server", &config.ModelEntry{Tools: []string{"other__tool"}}, false},
		{"a wildcard over every server", &config.ModelEntry{Tools: []string{"*"}}, true},
		{"a shorter prefix that covers the server", &config.ModelEntry{Tools: []string{"sr*"}}, true},
		{"a longer prefix inside the server", &config.ModelEntry{Tools: []string{"srv__to*"}}, true},
		{"a prefix of another server", &config.ModelEntry{Tools: []string{"oth*"}}, false},
		{"a denylist of the whole server", &config.ModelEntry{DisallowedTools: []string{"srv__*"}}, false},
		{"a denylist of every tool", &config.ModelEntry{DisallowedTools: []string{"*"}}, false},
		{"a denylist of one tool of the server", &config.ModelEntry{DisallowedTools: []string{"srv__tool"}}, true},
		{"a denylist of another server", &config.ModelEntry{DisallowedTools: []string{"other__*"}}, true},
		{"the allowlist admits it, the denylist takes the whole server", &config.ModelEntry{Tools: []string{"srv__tool"}, DisallowedTools: []string{"srv__*"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelMayAdmitServer(tc.entry, "srv"); got != tc.want {
				t.Fatalf("modelMayAdmitServer(%+v, srv) = %v, want %v", tc.entry, got, tc.want)
			}
		})
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
		cfg.Models = []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, Tools: []string{"read"}}}
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

// Whether a scheduled run dials its MCP servers is decided after the model's
// lists apply: a model that can admit none of their tools never starts them.
func TestModelToolListsDecideWhetherAScheduledRunDialsMCP(t *testing.T) {
	cases := []struct {
		name  string
		entry config.ModelEntry
		want  bool
	}{
		{"no lists", config.ModelEntry{}, true},
		{"an allowlist of built-ins", config.ModelEntry{Tools: []string{"read", "grep"}}, false},
		{"an allowlist that names the server", config.ModelEntry{Tools: []string{"read", "srv__*"}}, true},
		{"an exact tool of the server", config.ModelEntry{Tools: []string{"srv__tool"}}, true},
		{"a denylist of the server", config.ModelEntry{DisallowedTools: []string{"srv__*"}}, false},
		{"a denylist of another tool", config.ModelEntry{DisallowedTools: []string{"write"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := tc.entry
			rig := newSubagentRig(t, func(cfg *config.Config) {
				entry.Model, entry.MaxTokens = "fake/model", 100
				cfg.Models = []config.ModelEntry{entry}
			})
			rig.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("REPORT: done")) })
			spy := &spyRuntime{SubagentRuntime: rig.mgr}
			scheduledRun(t, rig, spy, nil, []string{"srv"})
			// The spec is what the run asked for, whether or not the run then
			// had a tool left (no server is running in this rig, so an exact
			// MCP name leaves the run none).
			spec, _ := spy.last(t)
			if spec.ConnectMCP != tc.want {
				t.Fatalf("ConnectMCP = %v, want %v", spec.ConnectMCP, tc.want)
			}
		})
	}
}

// A spawn dials MCP for the child only when the child's model can admit one of
// the parent's MCP tools.
func TestModelToolListsDecideWhetherASpawnDialsMCP(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) {
		cfg.Models = []config.ModelEntry{
			{Model: "fake/model", MaxTokens: 100},
			{Model: "fake/small", MaxTokens: 100, Tools: []string{"read", "grep"}},
		}
	})
	rig.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("REPORT: done")) })
	rig.parent.AddSessionMCPClient(mcp.NewStaticClient("srv", []mcp.ToolInfo{{Name: "tool"}}))
	rig.approvedDefinition("on-small", "model: fake/small\ntools: read, srv__tool\n")
	rig.approvedDefinition("on-big", "model: fake/model\ntools: read, srv__tool\n")

	for _, tc := range []struct {
		agent string
		want  bool
	}{{"on-small", false}, {"on-big", true}} {
		spy := &spyRuntime{SubagentRuntime: rig.mgr}
		ag := rig.parentAgent()
		ag.SetSubagentRuntime(spy)
		if _, err := ag.spawnSubagent(context.Background(), spawnReq(tc.agent)); err != nil {
			t.Fatalf("spawn %s: %v", tc.agent, err)
		}
		spec, _ := spy.last(t)
		if spec.ConnectMCP != tc.want {
			t.Fatalf("%s: ConnectMCP = %v, want %v", tc.agent, spec.ConnectMCP, tc.want)
		}
	}
}
