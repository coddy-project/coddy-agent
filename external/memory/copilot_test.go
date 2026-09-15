//go:build memory

package memory

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	memstorage "github.com/EvilFreelancer/coddy-agent/external/memory/storage"
	memtools "github.com/EvilFreelancer/coddy-agent/external/memory/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestRunBeforeTurnWhenDisabled(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Memory.Enabled = false
	cfg.Memory.ApplyDefaults()

	out, dur, err := RunBeforeTurn(context.Background(), nil, cfg, filepath.Join(tmp, "w"), "hello world", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if dur != 0 {
		t.Fatalf("duration = %d want 0", dur)
	}
	if out.ContextText != "" {
		t.Fatalf("context = %q want empty", out.ContextText)
	}
}

// A read-only pass (ask mode) offers the copilot recall tools only, so stored
// memory cannot change even when the model asks for it.
func TestBeforeTurnToolsReadOnlyOffersRecallOnly(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	cfg.Memory.ApplyDefaults()
	cfg.Paths.Home = tmp
	store, err := memstorage.NewStore(&cfg.Memory, cfg.Paths, filepath.Join(tmp, "w"))
	if err != nil {
		t.Fatal(err)
	}
	names := func(list []*tooling.Tool) map[string]bool {
		out := map[string]bool{}
		for _, tl := range list {
			out[tl.Definition.Name] = true
		}
		return out
	}
	ro := names(beforeTurnTools(store, &cfg.Memory, true))
	for _, mut := range []string{memtools.NameSave, memtools.NameMkdir, memtools.NameDelete} {
		if ro[mut] {
			t.Errorf("read-only pass still offers %s", mut)
		}
	}
	for _, want := range []string{memtools.NameSearch, memtools.NameList, memtools.NameRead} {
		if !ro[want] {
			t.Errorf("read-only pass lost %s", want)
		}
	}
	if rw := names(beforeTurnTools(store, &cfg.Memory, false)); !rw[memtools.NameSave] {
		t.Error("the full pass should still offer save")
	}
}

// readOnlyCopilotProvider asks for a save on its first round (a hallucinated
// mutation the read-only pass does not offer) and answers on the second.
type readOnlyCopilotProvider struct {
	calls int
	seen  [][]llm.Message
}

func (p *readOnlyCopilotProvider) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.calls++
	p.seen = append(p.seen, append([]llm.Message(nil), msgs...))
	if p.calls == 1 {
		call := llm.ToolCall{ID: "mem-save-1", Name: memtools.NameSave, InputJSON: `{"title":"t","body":"b","scope":"project"}`}
		return &llm.Response{ToolCalls: []llm.ToolCall{call}, StopReason: "tool_use"}, nil
	}
	return &llm.Response{Content: "nothing relevant stored", StopReason: "end_turn"}, nil
}

func (p *readOnlyCopilotProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	return nil, errors.New("Stream must not be used by this test")
}

// A hallucinated save during a read-only pass is rejected, nothing is written,
// and the outcome stays a recall pass rather than a "persist" that saved nothing.
func TestRunBeforeTurnReadOnlyIgnoresHallucinatedSave(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	cfg.Memory.ApplyDefaults()
	cfg.Paths.Home = tmp
	prov := &readOnlyCopilotProvider{}
	orig := copilotProviderFactory
	copilotProviderFactory = func(*config.Config, string) (llm.Provider, error) { return prov, nil }
	t.Cleanup(func() { copilotProviderFactory = orig })

	out, _, err := RunBeforeTurn(context.Background(), nil, cfg, filepath.Join(tmp, "w"), "what did we decide?", "", &RunBeforeTurnOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "recall" {
		t.Fatalf("outcome mode = %q, want recall", out.Mode)
	}
	if out.Persist.Saved {
		t.Fatal("read-only pass reported a saved memory")
	}
	if prov.calls != 2 {
		t.Fatalf("provider called %d times, want 2", prov.calls)
	}
	rejected := false
	for _, m := range prov.seen[1] {
		if m.Role == llm.RoleTool && m.ToolCallID == "mem-save-1" && strings.Contains(m.Content, "error") {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("the save call was not answered with an error: %+v", prov.seen[1])
	}
	if !strings.Contains(prov.seen[0][0].Content, "read-only ask mode") {
		t.Fatalf("system prompt lacks the read-only addendum: %.200s", prov.seen[0][0].Content)
	}
}

// failingCopilotProvider refuses every call, the way a deployment that is down
// or overloaded answers.
type failingCopilotProvider struct{ calls int }

func (p *failingCopilotProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	p.calls++
	return nil, errors.New("503 model overloaded")
}

func (p *failingCopilotProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	return nil, errors.New("503 model overloaded")
}

// answeringCopilotProvider ends the pass on its first round.
type answeringCopilotProvider struct{ calls int }

func (p *answeringCopilotProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	p.calls++
	return &llm.Response{Content: "nothing relevant stored", StopReason: "end_turn"}, nil
}

func (p *answeringCopilotProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	return nil, errors.New("Stream must not be used by this test")
}

// Issue #247: the memory pass is a side errand of the turn, so one unreachable
// model must not take it down.
func TestRunBeforeTurnFallsBackToTheNextModel(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	cfg.Memory.Model = "fake/down"
	cfg.Memory.FallbackModels = []string{"fake/deputy"}
	cfg.Memory.ApplyDefaults()
	cfg.Agent.Model = "fake/session"
	cfg.Paths.Home = tmp

	down := &failingCopilotProvider{}
	deputy := &answeringCopilotProvider{}
	orig := copilotProviderFactory
	copilotProviderFactory = func(_ *config.Config, ref string) (llm.Provider, error) {
		if ref == "fake/down" {
			return down, nil
		}
		return deputy, nil
	}
	t.Cleanup(func() { copilotProviderFactory = orig })

	out, _, err := RunBeforeTurn(context.Background(), nil, cfg, filepath.Join(tmp, "w"), "what did we decide?", cfg.Memory.Model, nil)
	if err != nil {
		t.Fatalf("copilot: %v", err)
	}
	if down.calls == 0 {
		t.Fatal("the configured model was never tried")
	}
	if deputy.calls == 0 {
		t.Fatal("the fallback never ran")
	}
	if out.ContextText != "nothing relevant stored" {
		t.Fatalf("outcome text = %q, want the fallback's answer", out.ContextText)
	}
}

func TestCopilotChainIsOrderedAndDeduplicated(t *testing.T) {
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	// The session model repeated in the list must not be built twice.
	cfg.Memory.FallbackModels = []string{"fake/deputy", "fake/session"}
	cfg.Agent.Model = "fake/session"

	orig := copilotProviderFactory
	copilotProviderFactory = func(*config.Config, string) (llm.Provider, error) {
		return &answeringCopilotProvider{}, nil
	}
	t.Cleanup(func() { copilotProviderFactory = orig })

	chain, err := copilotChain(cfg, "fake/memory")
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	var refs []string
	for _, c := range chain {
		refs = append(refs, c.modelRef)
	}
	want := []string{"fake/memory", "fake/deputy", "fake/session"}
	if len(refs) != len(want) {
		t.Fatalf("chain = %v, want %v", refs, want)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Fatalf("chain = %v, want %v", refs, want)
		}
	}
}

// A model that resolves to nothing is skipped rather than ending the chain.
func TestCopilotChainSkipsAModelThatCannotBeBuilt(t *testing.T) {
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	cfg.Memory.FallbackModels = []string{"fake/deputy"}
	cfg.Agent.Model = "fake/session"

	orig := copilotProviderFactory
	copilotProviderFactory = func(_ *config.Config, ref string) (llm.Provider, error) {
		if ref == "fake/missing" {
			return nil, errors.New("unknown model")
		}
		return &answeringCopilotProvider{}, nil
	}
	t.Cleanup(func() { copilotProviderFactory = orig })

	chain, err := copilotChain(cfg, "fake/missing")
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	if len(chain) != 2 || chain[0].modelRef != "fake/deputy" {
		t.Fatalf("chain = %+v, want the deputy then the session model", chain)
	}
}
