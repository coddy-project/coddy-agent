package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestParseCompactCommandOptions(t *testing.T) {
	cases := []struct {
		in   string
		want compactCommandArgs
	}{
		{in: "/compact --model fake/second", want: compactCommandArgs{Model: "fake/second"}},
		{in: "/compact --model=fake/second keep the paths", want: compactCommandArgs{Model: "fake/second", Instructions: "keep the paths"}},
		{in: "/compact --model qwen keep  the\npaths", want: compactCommandArgs{Model: "qwen", Instructions: "keep  the\npaths"}},
		{in: "/compact\t--model\tqwen", want: compactCommandArgs{Model: "qwen"}},
		// Options come first: an instruction may mention one.
		{in: "/compact explain what --model does", want: compactCommandArgs{Instructions: "explain what --model does"}},
		{in: "/compact --model", want: compactCommandArgs{ModelMissing: true}},
		{in: "/compact --model=", want: compactCommandArgs{ModelMissing: true}},
		{in: "/compact --model --fast", want: compactCommandArgs{ModelMissing: true, UnknownOptions: []string{"--fast"}}},
		{in: "/compact --fast keep it short", want: compactCommandArgs{UnknownOptions: []string{"--fast"}, Instructions: "keep it short"}},
	}
	for _, tc := range cases {
		got, ok := parseCompactCommand(tc.in)
		if !ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseCompactCommand(%q) = (%+v, %v), want %+v", tc.in, got, ok, tc.want)
		}
	}
}

// twoModelCompactAgent has the session on fake/model and a second configured
// model, each answered by its own provider.
func twoModelCompactAgent(t *testing.T, st *session.State, comp config.Compaction) (ag *Agent, first, second *compactCannedProvider) {
	t.Helper()
	first = &compactCannedProvider{t: t, summary: "SUMMARY FROM THE SESSION MODEL"}
	second = &compactCannedProvider{t: t, summary: "SUMMARY FROM THE SECOND MODEL"}
	ag = compactTestAgent(t, st, comp, nil)
	ag.cfg.Models = append(ag.cfg.Models, config.ModelEntry{Model: "fake/second-qwen", MaxTokens: 100})
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		if strings.Contains(in.Model, "second") {
			return second, nil
		}
		return first, nil
	}
	return ag, first, second
}

func TestCompactSessionUsesTheModelNamedForTheCall(t *testing.T) {
	keep := 1
	for _, name := range []string{"fake/second-qwen", "QWEN"} {
		st := seededCompactState(t, 3)
		ag, first, second := twoModelCompactAgent(t, st, config.Compaction{KeepRecentTurns: &keep})

		res, err := ag.CompactSession(context.Background(), CompactOptions{Model: name, Force: true})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Model != "fake/second-qwen" || len(second.requests) != 1 || len(first.requests) != 0 {
			t.Fatalf("%s: model = %q, second asked %d time(s), session model %d", name, res.Model, len(second.requests), len(first.requests))
		}
		if !strings.Contains(compactionOutcomeText(res), "Summarizer: fake/second-qwen.") {
			t.Fatalf("outcome does not name the summarizer: %q", compactionOutcomeText(res))
		}
	}
}

func TestCompactionChainPutsTheNamedModelFirst(t *testing.T) {
	keep := 1
	st := seededCompactState(t, 2)
	ag, _, _ := twoModelCompactAgent(t, st, config.Compaction{KeepRecentTurns: &keep, Model: "fake/model"})

	chain, err := ag.compactionChain("fake/second-qwen")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range chain {
		ids = append(ids, c.modelID)
	}
	// The configured chain stays behind the named model as its fallback.
	if !reflect.DeepEqual(ids, []string{"fake/second-qwen", "fake/model"}) {
		t.Fatalf("chain = %v", ids)
	}
}

func TestCompactSessionRefusesAModelItCannotName(t *testing.T) {
	keep := 1
	for _, name := range []string{"nope", "fake/"} { // unknown, ambiguous
		st := seededCompactState(t, 3)
		ag, first, second := twoModelCompactAgent(t, st, config.Compaction{KeepRecentTurns: &keep})

		_, err := ag.CompactSession(context.Background(), CompactOptions{Model: name, Force: true})
		if !errors.Is(err, ErrCompactionModel) {
			t.Fatalf("%s: err = %v, want ErrCompactionModel", name, err)
		}
		if len(first.requests)+len(second.requests) != 0 {
			t.Fatalf("%s: a summarizer was called for a model that names nothing", name)
		}
		for _, m := range st.GetMessages() {
			if m.CompactionSummary {
				t.Fatalf("%s: a summary was inserted", name)
			}
		}
	}
}

func TestRunCompactCommandWithModel(t *testing.T) {
	keep := 1
	st := seededCompactState(t, 3)
	ag, _, second := twoModelCompactAgent(t, st, config.Compaction{KeepRecentTurns: &keep})

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/compact --model qwen keep the paths"}})
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q, err = %v", stop, err)
	}
	if len(second.requests) != 1 {
		t.Fatalf("the named model was asked %d time(s)", len(second.requests))
	}
	asked := transcriptText(second.requests[0])
	if !strings.Contains(asked, "keep the paths") || strings.Contains(asked, "--model") {
		t.Fatalf("instructions as the summarizer read them: %q", asked)
	}
	msgs := st.GetMessages()
	if last := msgs[len(msgs)-1]; !strings.Contains(last.Content, "Summarizer: fake/second-qwen.") {
		t.Fatalf("answer = %q", last.Content)
	}
}

func TestRunCompactCommandAnswersWhatItCannotRun(t *testing.T) {
	cases := []struct {
		prompt string
		want   string
	}{
		{prompt: "/compact --model nope", want: `unknown model "nope" (configured: fake/model, fake/second-qwen)`},
		{prompt: "/compact --model fake/", want: "ambiguous"},
		{prompt: "/compact --model", want: "--model needs a model id."},
		{prompt: "/compact --fast", want: "Unknown option: --fast."},
	}
	keep := 1
	for _, tc := range cases {
		st := seededCompactState(t, 3)
		ag, first, second := twoModelCompactAgent(t, st, config.Compaction{KeepRecentTurns: &keep})

		stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: tc.prompt}})
		if err != nil || stop != string(acp.StopReasonEndTurn) {
			t.Fatalf("%s: stop = %q, err = %v", tc.prompt, stop, err)
		}
		if len(first.requests)+len(second.requests) != 0 {
			t.Fatalf("%s: a summarizer was called", tc.prompt)
		}
		msgs := st.GetMessages()
		last := msgs[len(msgs)-1]
		if !strings.Contains(last.Content, tc.want) || !strings.Contains(last.Content, "Usage: /compact") {
			t.Fatalf("%s: answer = %q", tc.prompt, last.Content)
		}
	}
}

// A model that calls compact_context in the first turn of a session used to
// fold the assistant message carrying that very call: its result then answered
// a call the provider never saw, and the next request was refused.
func TestCompactFromToolKeepsTheCallInFlight(t *testing.T) {
	st := &session.State{ID: "sess_compact_tool", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "read the log and then compact"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_read", Name: "read_file", InputJSON: `{}`}}})
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "call_read", Content: "a long build log"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_compact", Name: "compact_context", InputJSON: `{}`}}})

	keep := 2
	provider := &compactCannedProvider{t: t, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	out, err := ag.compactFromTool(context.Background(), tooling.CompactRequest{})
	if err != nil || !strings.Contains(out, "Context compacted:") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	// What the loop does next: the result of the call lands in the transcript.
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "call_compact", Content: out})

	visible := session.MessagesForLLM(st.GetMessages())
	if len(visible) != 3 || !visible[0].CompactionSummary {
		t.Fatalf("visible window = %+v", visible)
	}
	if len(visible[1].ToolCalls) != 1 || visible[1].ToolCalls[0].ID != "call_compact" || visible[2].ToolCallID != "call_compact" {
		t.Fatalf("the tool result is not preceded by its call: %+v", visible)
	}
}

func TestCompactFromToolWithNothingBeforeTheCall(t *testing.T) {
	st := &session.State{ID: "sess_compact_tool_empty", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "earlier"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "earlier answer"})
	st.InsertCompactionSummary(2, session.NewCompactionSummaryMessage("old summary", "fake/model"))
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_compact", Name: "compact_context", InputJSON: `{}`}}})

	provider := &compactCannedProvider{t: t, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{}, provider)

	out, err := ag.compactFromTool(context.Background(), tooling.CompactRequest{})
	if err != nil || !strings.HasPrefix(out, "Nothing to compact") || len(provider.requests) != 0 {
		t.Fatalf("out = %q, err = %v, summarizer calls = %d", out, err, len(provider.requests))
	}
}
