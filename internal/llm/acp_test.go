package llm

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/acptest"
)

// TestHelperACPAgent turns the test binary into the scripted ACP agent when
// acptest.Command started it; the ordinary run skips it.
func TestHelperACPAgent(t *testing.T) {
	if !acptest.Main(flag.Args()) {
		t.Skip("helper process")
	}
}

// acpRow returns the provider input of an acp row running script, and the
// path of the agent's log. Retries are off: a test that wants the wrapper's
// retry sets them itself.
func acpRow(t *testing.T, script acptest.Script) (ProviderInput, string) {
	t.Helper()
	if script.Log == "" {
		script.Log = t.TempDir() + "/acp.log"
	}
	// A test run inside an agent Coddy started still starts its agents at
	// depth one.
	t.Setenv(ACPDepthEnv, "")
	cmd, args := acptest.Command(t, script)
	t.Cleanup(StopACPAgents)
	return ProviderInput{
		Name:          "fake",
		Type:          "acp",
		Model:         "default",
		RetryDisabled: true,
		ACP:           &config.ACPProviderConfig{Command: cmd, Args: args},
	}, script.Log
}

func newACPTestProvider(t *testing.T, in ProviderInput) Provider {
	t.Helper()
	p, err := NewProvider(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// conversation returns a context of the session id, its own workspace.
func conversation(t *testing.T, id string, mutate ...func(*Conversation)) context.Context {
	t.Helper()
	conv := Conversation{SessionID: id, CWD: t.TempDir(), Mode: "agent", PermissionMode: config.PermModeAsk}
	for _, m := range mutate {
		m(&conv)
	}
	ctx, cancel := context.WithTimeout(WithConversation(context.Background(), conv), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func sys(msgs ...Message) []Message {
	return append([]Message{{Role: RoleSystem, Content: "coddy system prompt"}}, msgs...)
}

type promptParams struct {
	SessionID string `json:"sessionId"`
	Prompt    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"prompt"`
}

func decodePrompt(t *testing.T, r acptest.Received) promptParams {
	t.Helper()
	var p promptParams
	if err := json.Unmarshal(r.Params, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func promptTexts(t *testing.T, r acptest.Received) []string {
	t.Helper()
	var out []string
	for _, b := range decodePrompt(t, r).Prompt {
		out = append(out, b.Type+":"+b.Text)
	}
	return out
}

func readLog(t *testing.T, path, method string) []acptest.Received {
	t.Helper()
	return acptest.Methods(acptest.ReadLog(t, path), method)
}

func TestACPProviderStreamsTheAgentsAnswer(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{
		Thoughts: []string{"thinking"},
		Tools: []acptest.Tool{
			{ID: "t1", Title: "Read README.md", Kind: "read", Input: map[string]string{"file_path": "README.md"}, Output: "# Readme"},
			{ID: "t2", Title: "Run tests", Fail: true, Output: "FAIL ./..."},
		},
		Text:  []string{"Hello", " world"},
		Usage: map[string]int{"inputTokens": 120, "outputTokens": 7, "thoughtTokens": 3, "cachedReadTokens": 50},
	}}})
	var text, reasoning strings.Builder
	tools := map[string]AgentToolCall{}
	resp, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("hi")), nil, func(c StreamChunk) {
		text.WriteString(c.TextDelta)
		reasoning.WriteString(c.ReasoningDelta)
		if c.AgentTool != nil {
			tools[c.AgentTool.ID] = *c.AgentTool
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Hello world" || text.String() != "Hello world" || resp.StopReason != "end_turn" {
		t.Fatalf("content = %q, streamed %q, stop %q", resp.Content, text.String(), resp.StopReason)
	}
	// The agent's tools are rows of their own, out of its reasoning.
	if resp.Reasoning != "thinking" {
		t.Errorf("reasoning = %q", resp.Reasoning)
	}
	want := map[string]AgentToolCall{
		"t1": {ID: "t1", Title: "Read README.md", Kind: "read", Status: "completed", Input: `{"file_path":"README.md"}`, Output: "# Readme"},
		"t2": {ID: "t2", Title: "Run tests", Kind: "execute", Status: "failed", Output: "FAIL ./..."},
	}
	for id, w := range want {
		if tools[id] != w {
			t.Errorf("tool %s = %+v, want %+v", id, tools[id], w)
		}
	}
	if reasoning.String() != resp.Reasoning {
		t.Errorf("streamed reasoning %q, answer %q", reasoning.String(), resp.Reasoning)
	}
	if resp.InputTokens != 120 || resp.OutputTokens != 7 || resp.ReasoningTokens != 3 || resp.CachedInputTokens != 50 {
		t.Fatalf("usage = %+v", resp)
	}
	prompts := readLog(t, logPath, "session/prompt")
	if len(prompts) != 1 {
		t.Fatalf("prompts = %d", len(prompts))
	}
	if got := promptTexts(t, prompts[0]); len(got) != 1 || got[0] != "text:hi" {
		t.Fatalf("prompt = %q (the system message must not be sent)", got)
	}
	var init struct {
		ClientCapabilities struct {
			FS struct {
				Read  bool `json:"readTextFile"`
				Write bool `json:"writeTextFile"`
			} `json:"fs"`
			Terminal bool           `json:"terminal"`
			Meta     map[string]any `json:"_meta"`
		} `json:"clientCapabilities"`
	}
	_ = json.Unmarshal(readLog(t, logPath, "initialize")[0].Params, &init)
	if init.ClientCapabilities.FS.Read || init.ClientCapabilities.FS.Write || init.ClientCapabilities.Terminal {
		t.Fatalf("the client advertised capabilities it does not serve: %+v", init)
	}
	if init.ClientCapabilities.Meta["parameterizedModelPicker"] != true {
		t.Fatalf("initialize did not advertise parameterizedModelPicker: %+v", init.ClientCapabilities.Meta)
	}
}

func TestACPProviderContinuesTheConversationOnOneSession(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{})
	p := newACPTestProvider(t, in)
	ctx := conversation(t, "s1")
	first, err := p.Stream(ctx, sys(u("one")), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stream(ctx, sys(u("one"), a(first.Content), u("two"),
		Message{Role: RoleUser, Content: "<turn_context>clock</turn_context>", Ephemeral: true}), nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(readLog(t, logPath, "session/new")); n != 1 {
		t.Fatalf("session/new sent %d times, want one session for the conversation", n)
	}
	prompts := readLog(t, logPath, "session/prompt")
	if len(prompts) != 2 || decodePrompt(t, prompts[0]).SessionID != decodePrompt(t, prompts[1]).SessionID {
		t.Fatalf("prompts = %d", len(prompts))
	}
	got := promptTexts(t, prompts[1])
	if len(got) != 2 || got[0] != "text:two" || got[1] != "text:<turn_context>clock</turn_context>" {
		t.Fatalf("second prompt = %q, want only the new message and the turn context", got)
	}
}

func TestACPProviderKeepsConversationsApart(t *testing.T) {
	// Each prompt waits before it answers, so the two are in flight at once.
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Echo: true, DelayMS: 300}}})
	p := newACPTestProvider(t, in)
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			resp, err := p.Stream(conversation(t, id), sys(u("hi "+id)), nil, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if resp.Content != "echo: hi "+id {
				t.Errorf("conversation %s got %q", id, resp.Content)
			}
		}(id)
	}
	wg.Wait()
	prompts := readLog(t, logPath, "session/prompt")
	if len(prompts) != 2 || decodePrompt(t, prompts[0]).SessionID == decodePrompt(t, prompts[1]).SessionID {
		t.Fatalf("two conversations must get two ACP sessions of one process: %d prompts", len(prompts))
	}
	pids := map[int]bool{}
	for _, r := range acptest.ReadLog(t, logPath) {
		pids[r.PID] = true
	}
	if len(pids) != 1 {
		t.Fatalf("agent processes = %d, want one shared", len(pids))
	}
}

// Conversations on one shared agent run at once: three prompts meet at the
// agent's barrier, which a provider that sent them one after another would
// never reach (the first ones would time out waiting for the others).
func TestACPProviderRunsConversationsAtOnce(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Echo: true, Barrier: 3}}})
	p := newACPTestProvider(t, in)
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			resp, err := p.Stream(conversation(t, id), sys(u("hi "+id)), nil, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if want := "echo: hi " + id + "\nbarrier: 3"; resp.Content != want {
				t.Errorf("conversation %s got %q, want %q", id, resp.Content, want)
			}
		}(id)
	}
	wg.Wait()
	pids := map[int]bool{}
	for _, r := range acptest.ReadLog(t, logPath) {
		pids[r.PID] = true
	}
	if len(pids) != 1 {
		t.Fatalf("agent processes = %d, want one shared", len(pids))
	}
}

// The agent works where the session works: session/new names the
// conversation's workspace, and ${CWD} in the block is that workspace.
func TestACPProviderOpensSessionsInTheWorkspace(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Env: []string{"ROW_CWD"}}}})
	in.ACP.Env = map[string]string{"ROW_CWD": "${CWD}"}
	p := newACPTestProvider(t, in)
	wsA, wsB := t.TempDir(), t.TempDir()
	for i, ws := range []string{wsA, wsB} {
		ctx := conversation(t, "s"+string(rune('a'+i)), func(c *Conversation) { c.CWD = ws })
		resp, err := p.Stream(ctx, sys(u("where")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(resp.Content, "ROW_CWD="+ws) {
			t.Fatalf("${CWD} in env = %q, want %s", resp.Content, ws)
		}
	}
	var cwds []string
	pids := map[int]bool{}
	for _, r := range acptest.Methods(acptest.ReadLog(t, logPath), "session/new") {
		var p struct {
			CWD string `json:"cwd"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil {
			t.Fatal(err)
		}
		cwds = append(cwds, p.CWD)
		pids[r.PID] = true
	}
	if strings.Join(cwds, ",") != wsA+","+wsB {
		t.Fatalf("session/new cwd = %v, want the two workspaces", cwds)
	}
	if len(pids) != 2 {
		t.Fatalf("agent processes = %d, want one per workspace for a block naming ${CWD}", len(pids))
	}
}

func TestACPProviderProcessPerSession(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{})
	in.ACP.Process = config.ACPProcessPerSession
	p := newACPTestProvider(t, in)
	for _, id := range []string{"a", "b"} {
		if _, err := p.Stream(conversation(t, id), sys(u("hi "+id)), nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	pids := map[int]bool{}
	for _, r := range acptest.ReadLog(t, logPath) {
		pids[r.PID] = true
	}
	if len(pids) != 2 {
		t.Fatalf("agent processes = %d, want one per session", len(pids))
	}
}

func TestACPProviderRefusesCallsItDoesNotServe(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{})
	p := newACPTestProvider(t, in)
	cases := map[string]context.Context{
		"no conversation (a helper)": context.Background(),
		"restricted turn":            conversation(t, "s1", func(c *Conversation) { c.Restricted = true }),
		"plan mode with no mode":     conversation(t, "s1", func(c *Conversation) { c.Mode = "plan" }),
		"ask mode with no mode":      conversation(t, "s1", func(c *Conversation) { c.Mode = "ask" }),
	}
	for name, ctx := range cases {
		_, err := p.Stream(ctx, sys(u("hi")), nil, nil)
		var acpErr *ACPError
		if !errors.As(err, &acpErr) || acpErr.retryable() {
			t.Errorf("%s: err = %v, want a final ACPError", name, err)
		}
	}
	if got := readLog(t, logPath, "initialize"); len(got) != 0 {
		t.Fatalf("a refused call started the agent")
	}
	_, err := p.Stream(conversation(t, "s1", func(c *Conversation) { c.Mode = "ask" }), sys(u("hi")), nil, nil)
	if !strings.Contains(err.Error(), "acp.modes.ask") {
		t.Fatalf("the refusal must name the key to set: %v", err)
	}
}

func TestACPProviderSelectsTheMappedModeAndGoesBack(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Modes: []string{"default", "plan", "bypass"}})
	in.ACP.Modes = config.ACPModes{Agent: "bypass", Plan: "plan"}
	p := newACPTestProvider(t, in)
	agentCtx := conversation(t, "s1")
	planCtx := WithConversation(agentCtx, Conversation{SessionID: "s1", CWD: ConversationFrom(agentCtx).CWD, Mode: "plan"})
	h := []Message{u("one")}
	r, err := p.Stream(agentCtx, sys(h...), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h = append(h, a(r.Content), u("two"))
	if r, err = p.Stream(planCtx, sys(h...), nil, nil); err != nil {
		t.Fatal(err)
	}
	// The mapping for agent is dropped: the next agent turn goes back to the
	// agent's own default, not to the plan mode the last turn left.
	in.ACP.Modes = config.ACPModes{Plan: "plan"}
	h = append(h, a(r.Content), u("three"))
	if _, err := newACPTestProvider(t, in).Stream(agentCtx, sys(h...), nil, nil); err != nil {
		t.Fatal(err)
	}
	var values []string
	for _, r := range readLog(t, logPath, "session/set_config_option") {
		var p struct {
			ConfigID, Value string
		}
		_ = json.Unmarshal(r.Params, &p)
		values = append(values, p.ConfigID+"="+p.Value)
	}
	if strings.Join(values, ",") != "mode=bypass,mode=plan,mode=default" {
		t.Fatalf("mode selections = %v", values)
	}
}

// An agent that offers its modes only as modes, not as a config option,
// gets them through session/set_mode.
func TestACPProviderSelectsModesThroughSetMode(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Modes: []string{"default", "plan", "bypass"}, LegacyModes: true})
	in.ACP.Modes = config.ACPModes{Agent: "bypass", Plan: "plan"}
	agentCtx := conversation(t, "s1")
	planCtx := WithConversation(agentCtx, Conversation{SessionID: "s1", CWD: ConversationFrom(agentCtx).CWD, Mode: "plan"})
	h := []Message{u("one")}
	r, err := newACPTestProvider(t, in).Stream(agentCtx, sys(h...), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h = append(h, a(r.Content), u("two"))
	if r, err = newACPTestProvider(t, in).Stream(planCtx, sys(h...), nil, nil); err != nil {
		t.Fatal(err)
	}
	in.ACP.Modes = config.ACPModes{Plan: "plan"}
	h = append(h, a(r.Content), u("three"))
	if _, err := newACPTestProvider(t, in).Stream(agentCtx, sys(h...), nil, nil); err != nil {
		t.Fatal(err)
	}
	var values []string
	for _, r := range readLog(t, logPath, "session/set_mode") {
		var p struct{ ModeID string }
		_ = json.Unmarshal(r.Params, &p)
		values = append(values, p.ModeID)
	}
	if strings.Join(values, ",") != "bypass,plan,default" {
		t.Fatalf("set_mode = %v", values)
	}
	if n := len(readLog(t, logPath, "session/set_config_option")); n != 0 {
		t.Fatalf("set_config_option = %d, the agent offers no mode option", n)
	}
}

func TestACPProviderSelectsModels(t *testing.T) {
	t.Run("through the model config option, and back to the default", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{Models: []string{"m-default", "m-big"}})
		in.Model = "m-big"
		ctx := conversation(t, "s1")
		r, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		in.Model = "default"
		if _, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a(r.Content), u("two")), nil, nil); err != nil {
			t.Fatal(err)
		}
		sets := readLog(t, logPath, "session/set_config_option")
		if len(sets) != 2 || !strings.Contains(string(sets[0].Params), "m-big") || !strings.Contains(string(sets[1].Params), "m-default") {
			t.Fatalf("set_config_option = %d calls", len(sets))
		}
	})
	t.Run("a saved parameterized variant id selects its base model", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{
			Models: []string{"grok-4.7", "composer-2.5"},
			Parameters: []acptest.Parameter{
				{ID: "context", Values: []string{"256k", "500k"}},
				{ID: "reasoning_effort", Category: "thought_level", Values: []string{"low", "medium", "high", "xhigh"}},
				{ID: "fast", Values: []string{"false", "true"}},
			},
		})
		in.Model = "composer-2.5[fast=true]"
		if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil); err != nil {
			t.Fatal(err)
		}
		type selection struct {
			ConfigID string `json:"configId"`
			Value    string `json:"value"`
		}
		var got []string
		for _, r := range readLog(t, logPath, "session/set_config_option") {
			var p selection
			if err := json.Unmarshal(r.Params, &p); err != nil {
				t.Fatal(err)
			}
			got = append(got, p.ConfigID+"="+p.Value)
		}
		want := []string{"model=composer-2.5", "fast=true"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("set_config_option = %v, want %v", got, want)
		}
	})
	t.Run("through session/set_model", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{Models: []string{"a", "b"}, LegacyModels: true})
		in.Model = "b"
		if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil); err != nil {
			t.Fatal(err)
		}
		if got := readLog(t, logPath, "session/set_model"); len(got) != 1 {
			t.Fatalf("set_model = %d calls", len(got))
		}
	})
	t.Run("an unknown model names the offered ones", func(t *testing.T) {
		in, _ := acpRow(t, acptest.Script{Models: []string{"a", "b"}})
		in.Model = "c"
		_, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil)
		var acpErr *ACPError
		if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorSelect || !strings.Contains(err.Error(), "a, b") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a reasoning level the agent offers", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{ThoughtLevels: []string{"low", "high"}})
		in.ReasoningEffort = "HIGH"
		if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil); err != nil {
			t.Fatal(err)
		}
		sets := readLog(t, logPath, "session/set_config_option")
		if len(sets) != 1 || !strings.Contains(string(sets[0].Params), `"value":"high"`) {
			t.Fatalf("set_config_option = %v", sets)
		}
	})

	t.Run("a boolean thought toggle does not hide the effort levels behind it", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{
			Models: []string{"claude-opus-4-8"},
			Parameters: []acptest.Parameter{
				{ID: "thinking", Category: "thought_level", Values: []string{"false", "true"}},
				{ID: "context", Values: []string{"300k", "1m"}},
				{ID: "effort", Category: "thought_level", Values: []string{"low", "medium", "high"}},
			},
		})
		in.ReasoningEffort = "high"
		if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil); err != nil {
			t.Fatal(err)
		}
		sets := readLog(t, logPath, "session/set_config_option")
		if len(sets) != 1 || !strings.Contains(string(sets[0].Params), `"configId":"effort"`) || !strings.Contains(string(sets[0].Params), `"value":"high"`) {
			t.Fatalf("set_config_option = %v, want effort=high", sets)
		}
	})

	t.Run("a boolean toggle alone remains the thought control", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{
			Parameters: []acptest.Parameter{
				{ID: "thinking", Category: "thought_level", Values: []string{"false", "true"}},
			},
		})
		in.ReasoningEffort = "true"
		if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil); err != nil {
			t.Fatal(err)
		}
		sets := readLog(t, logPath, "session/set_config_option")
		if len(sets) != 1 || !strings.Contains(string(sets[0].Params), `"configId":"thinking"`) || !strings.Contains(string(sets[0].Params), `"value":"true"`) {
			t.Fatalf("set_config_option = %v, want thinking=true", sets)
		}
	})

	t.Run("a reasoning level beside a boolean toggle is undone", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{
			Parameters: []acptest.Parameter{
				{ID: "thinking", Category: "thought_level", Values: []string{"false", "true"}},
				{ID: "effort", Category: "thought_level", Values: []string{"low", "medium", "high"}},
			},
		})
		ctx := conversation(t, "s1")
		in.ReasoningEffort = "high"
		r, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		in.ReasoningEffort = ""
		if _, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a(r.Content), u("two")), nil, nil); err != nil {
			t.Fatal(err)
		}
		var values []string
		for _, r := range readLog(t, logPath, "session/set_config_option") {
			var p struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			}
			if err := json.Unmarshal(r.Params, &p); err != nil {
				t.Fatal(err)
			}
			values = append(values, p.ConfigID+"="+p.Value)
		}
		if strings.Join(values, ",") != "effort=high,effort=low" {
			t.Fatalf("thought selections = %v, want the session's starting low restored", values)
		}
	})

	t.Run("an undo never moves to another thought option", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{
			Parameters: []acptest.Parameter{
				{ID: "effort", Category: "thought_level", Values: []string{"low", "high"}},
				{ID: "other", Category: "thought_level", Values: []string{"high", "low"}, Hidden: true},
			},
			Turns: []acptest.Turn{
				{ConfigOptions: []acptest.Parameter{{ID: "other", Category: "thought_level", Values: []string{"high", "low"}}}},
				{},
			},
		})
		ctx := conversation(t, "s1")
		in.ReasoningEffort = "high"
		r, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		in.ReasoningEffort = ""
		if _, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a(r.Content), u("two")), nil, nil); err != nil {
			t.Fatal(err)
		}
		var values []string
		for _, r := range readLog(t, logPath, "session/set_config_option") {
			var p struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			}
			if err := json.Unmarshal(r.Params, &p); err != nil {
				t.Fatal(err)
			}
			values = append(values, p.ConfigID+"="+p.Value)
		}
		if strings.Join(values, ",") != "effort=high" {
			t.Fatalf("thought selections = %v, want no undo written to the replacement option", values)
		}
	})

	t.Run("an override the agent moved away from is not restored later", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{
			Parameters: []acptest.Parameter{
				{ID: "effort", Category: "thought_level", Values: []string{"low", "high"}},
			},
			Turns: []acptest.Turn{
				{ConfigOptions: []acptest.Parameter{{ID: "effort", Category: "thought_level", Values: []string{"medium", "high"}}}},
				{ConfigOptions: []acptest.Parameter{{ID: "effort", Category: "thought_level", Values: []string{"high", "low"}}}},
				{},
			},
		})
		ctx := conversation(t, "s1")
		in.ReasoningEffort = "high"
		first, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		in.ReasoningEffort = "medium"
		second, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a(first.Content), u("two")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		in.ReasoningEffort = ""
		if _, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a(first.Content), u("two"), a(second.Content), u("three")), nil, nil); err != nil {
			t.Fatal(err)
		}
		var values []string
		for _, r := range readLog(t, logPath, "session/set_config_option") {
			var p struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			}
			if err := json.Unmarshal(r.Params, &p); err != nil {
				t.Fatal(err)
			}
			values = append(values, p.ConfigID+"="+p.Value)
		}
		if strings.Join(values, ",") != "effort=high" {
			t.Fatalf("thought selections = %v, want the agent's later high left alone", values)
		}
	})

	t.Run("an undo waits for the baseline to be offered again", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{
			Parameters: []acptest.Parameter{
				{ID: "effort", Category: "thought_level", Values: []string{"low", "high"}},
			},
			Turns: []acptest.Turn{
				{ConfigOptions: []acptest.Parameter{{ID: "effort", Category: "thought_level", Values: []string{"high", "medium"}}}},
				{ConfigOptions: []acptest.Parameter{{ID: "effort", Category: "thought_level", Values: []string{"high", "low"}}}},
				{},
			},
		})
		ctx := conversation(t, "s1")
		in.ReasoningEffort = "high"
		first, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		in.ReasoningEffort = ""
		second, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a(first.Content), u("two")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a(first.Content), u("two"), a(second.Content), u("three")), nil, nil); err != nil {
			t.Fatal(err)
		}
		var values []string
		for _, r := range readLog(t, logPath, "session/set_config_option") {
			var p struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			}
			if err := json.Unmarshal(r.Params, &p); err != nil {
				t.Fatal(err)
			}
			values = append(values, p.ConfigID+"="+p.Value)
		}
		if strings.Join(values, ",") != "effort=high,effort=low" {
			t.Fatalf("thought selections = %v, want the delayed baseline restored once offered", values)
		}
	})

	t.Run("a serialized reasoning parameter owns the level it names", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{
			Models: []string{"grok-4.7"},
			Parameters: []acptest.Parameter{
				{ID: "thinking", Category: "thought_level", Values: []string{"false", "true"}},
				{ID: "reasoning_effort", Category: "thought_level", Values: []string{"low", "medium", "high"}},
			},
		})
		ctx := conversation(t, "s1")
		in.ReasoningEffort = "high"
		if _, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one")), nil, nil); err != nil {
			t.Fatal(err)
		}
		in.ReasoningEffort = ""
		in.Model = "grok-4.7[reasoning_effort=high]"
		if _, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a("echo: one"), u("two")), nil, nil); err != nil {
			t.Fatal(err)
		}
		var values []string
		for _, r := range readLog(t, logPath, "session/set_config_option") {
			var p struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			}
			if err := json.Unmarshal(r.Params, &p); err != nil {
				t.Fatal(err)
			}
			values = append(values, p.ConfigID+"="+p.Value)
		}
		if strings.Join(values, ",") != "reasoning_effort=high" {
			t.Fatalf("set_config_option = %v, want the explicit high never reset", values)
		}
	})
}

func TestACPParameterizedModelIDParser(t *testing.T) {
	tests := []struct {
		model  string
		base   string
		params []acpModelParameter
		ok     bool
	}{
		{model: "composer-2.5[fast=true]", base: "composer-2.5", params: []acpModelParameter{{id: "fast", value: "true"}}, ok: true},
		{model: "default[]", base: "default", ok: true},
		{model: "grok-4.7[context=256k,reasoning_effort=high,fast=false]", base: "grok-4.7", params: []acpModelParameter{{id: "context", value: "256k"}, {id: "reasoning_effort", value: "high"}, {id: "fast", value: "false"}}, ok: true},
		{model: "plain"},
		{model: "composer-2.5[fast=true"},
		{model: "composer-2.5[fast]"},
		{model: "composer-2.5[=true]"},
		{model: "composer-2.5[fast=]"},
		{model: "composer-2.5[fast=true,fast=false]"},
		{model: "composer-2.5[fast=[true]]"},
	}
	for _, tt := range tests {
		base, params, ok := parseACPParameterizedModelID(tt.model)
		if ok != tt.ok || base != tt.base || fmt.Sprint(params) != fmt.Sprint(tt.params) {
			t.Errorf("%s = %q, %v, %t; want %q, %v, %t", tt.model, base, params, ok, tt.base, tt.params, tt.ok)
		}
	}
}

func TestACPProviderSelectsParameterizedModelEdges(t *testing.T) {
	script := acptest.Script{
		Models: []string{"grok-4.7", "composer-2.5", "default"},
		Parameters: []acptest.Parameter{
			{ID: "fast", Values: []string{"false", "true"}},
		},
	}
	selections := func(t *testing.T, logPath string) []string {
		t.Helper()
		var got []string
		for _, r := range readLog(t, logPath, "session/set_config_option") {
			var p struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			}
			if err := json.Unmarshal(r.Params, &p); err != nil {
				t.Fatal(err)
			}
			got = append(got, p.ConfigID+"="+p.Value)
		}
		return got
	}

	t.Run("an exact bracketed model wins over the compatibility translation", func(t *testing.T) {
		exact := script
		exact.Models = append([]string{"grok-4.7"}, append([]string{"composer-2.5[fast=true]"}, script.Models[1:]...)...)
		in, logPath := acpRow(t, exact)
		in.Model = "composer-2.5[fast=true]"
		if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s-exact"), sys(u("one")), nil, nil); err != nil {
			t.Fatal(err)
		}
		got := selections(t, logPath)
		if len(got) != 1 || got[0] != "model=composer-2.5[fast=true]" {
			t.Fatalf("set_config_option = %v, want only the exact model", got)
		}
	})

	t.Run("an empty parameter list selects the base model only", func(t *testing.T) {
		in, logPath := acpRow(t, script)
		in.Model = "default[]"
		if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s-empty"), sys(u("one")), nil, nil); err != nil {
			t.Fatal(err)
		}
		got := selections(t, logPath)
		if len(got) != 1 || got[0] != "model=default" {
			t.Fatalf("set_config_option = %v, want only model=default", got)
		}
	})

	t.Run("a malformed bracketed model is not translated", func(t *testing.T) {
		in, logPath := acpRow(t, script)
		in.Model = "composer-2.5[fast"
		_, err := newACPTestProvider(t, in).Stream(conversation(t, "s-malformed"), sys(u("one")), nil, nil)
		var acpErr *ACPError
		if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorSelect {
			t.Fatalf("err = %v", err)
		}
		if got := selections(t, logPath); len(got) != 0 {
			t.Fatalf("set_config_option = %v, want no selection", got)
		}
	})

	t.Run("an unadvertised serialized parameter is not written", func(t *testing.T) {
		in, logPath := acpRow(t, script)
		in.Model = "composer-2.5[unknown=true]"
		if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s-unknown"), sys(u("one")), nil, nil); err != nil {
			t.Fatal(err)
		}
		got := selections(t, logPath)
		if len(got) != 1 || got[0] != "model=composer-2.5" {
			t.Fatalf("set_config_option = %v, want only the base model", got)
		}
	})

	t.Run("a hidden single-value parameter is not written", func(t *testing.T) {
		hidden := script
		hidden.Parameters = append(append([]acptest.Parameter{}, script.Parameters...), acptest.Parameter{ID: "context", Values: []string{"200k"}, Hidden: true})
		in, logPath := acpRow(t, hidden)
		in.Model = "composer-2.5[context=200k,fast=true]"
		if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s-hidden"), sys(u("one")), nil, nil); err != nil {
			t.Fatal(err)
		}
		got := selections(t, logPath)
		want := []string{"model=composer-2.5", "fast=true"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("set_config_option = %v, want %v", got, want)
		}
	})

	t.Run("a parameter already at its value is not written again", func(t *testing.T) {
		in, logPath := acpRow(t, script)
		in.Model = "composer-2.5[fast=false]"
		if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s-current"), sys(u("one")), nil, nil); err != nil {
			t.Fatal(err)
		}
		got := selections(t, logPath)
		if len(got) != 1 || got[0] != "model=composer-2.5" {
			t.Fatalf("set_config_option = %v, want only the base model", got)
		}
	})
}

func permissionTurn(kind string) acptest.Turn {
	return acptest.Turn{Permission: &acptest.Permission{
		Title: "Write a.txt", Kind: kind, Content: "write hello to a.txt",
		Options: []acptest.Option{
			{ID: "yes", Name: "Allow once", Kind: "allow_once"},
			{ID: "yes-forever", Name: "Always", Kind: "allow_always"},
			{ID: "no", Name: "Reject", Kind: "reject_once"},
		},
	}}
}

func TestACPProviderPermissions(t *testing.T) {
	type tc struct {
		name     string
		policy   string
		mutate   func(*Conversation)
		kind     string
		answer   *PermissionAnswer
		want     string
		wantAsks int
	}
	cases := []tc{
		{name: "ask: allowed", answer: &PermissionAnswer{OptionID: "allow"}, want: "permission: yes", wantAsks: 1},
		{name: "ask: allowed always", answer: &PermissionAnswer{OptionID: "allow_always"}, want: "permission: yes-forever", wantAsks: 1},
		{name: "ask: rejected", answer: &PermissionAnswer{OptionID: "reject"}, want: "permission: no", wantAsks: 1},
		{name: "ask: dismissed is a refusal, not a cancelled turn", answer: &PermissionAnswer{Cancelled: true}, want: "permission: no", wantAsks: 1},
		{name: "bypass allows without asking", mutate: func(c *Conversation) { c.PermissionMode = config.PermModeBypass }, want: "permission: yes"},
		{name: "accept_edits allows an edit", kind: "edit", mutate: func(c *Conversation) { c.PermissionMode = config.PermModeAcceptEdits }, want: "permission: yes"},
		{name: "accept_edits asks for a command", kind: "execute", mutate: func(c *Conversation) { c.PermissionMode = config.PermModeAcceptEdits }, answer: &PermissionAnswer{OptionID: "reject"}, want: "permission: no", wantAsks: 1},
		{name: "allow policy", policy: "allow", want: "permission: yes"},
		{name: "deny policy", policy: "deny", want: "permission: no"},
		{name: "plan mode refuses whatever the policy", policy: "allow", mutate: func(c *Conversation) { c.Mode = "plan" }, want: "permission: no"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind := c.kind
			if kind == "" {
				kind = "edit"
			}
			in, _ := acpRow(t, acptest.Script{Turns: []acptest.Turn{permissionTurn(kind)}, Modes: []string{"default", "plan"}})
			in.ACP.Permissions = c.policy
			in.ACP.Modes.Plan = "plan"
			var asks []PermissionRequest
			ctx := conversation(t, "s1", func(conv *Conversation) {
				conv.AskPermission = func(_ context.Context, req PermissionRequest) (PermissionAnswer, error) {
					asks = append(asks, req)
					if c.answer == nil {
						return PermissionAnswer{Cancelled: true}, errors.New("unexpected prompt")
					}
					return *c.answer, nil
				}
				if c.mutate != nil {
					c.mutate(conv)
				}
			})
			resp, err := newACPTestProvider(t, in).Stream(ctx, sys(u("write it")), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(resp.Content, c.want) {
				t.Fatalf("answer = %q, want %q", resp.Content, c.want)
			}
			if len(asks) != c.wantAsks {
				t.Fatalf("asked %d times, want %d", len(asks), c.wantAsks)
			}
			if c.wantAsks > 0 {
				req := asks[0]
				var ids []string
				for _, o := range req.Options {
					ids = append(ids, o.ID)
				}
				if req.Title != "Write a.txt" || !strings.Contains(req.Body, "write hello to a.txt") || strings.Join(ids, ",") != "allow,allow_always,reject" {
					t.Fatalf("prompt = %+v", req)
				}
			}
		})
	}
}

func TestACPProviderReadsCoddysReasoningChunks(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{Turns: []acptest.Turn{{CoddyReasoning: []string{"weighing", " it"}, Text: []string{"done"}}}})
	resp, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("go")), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "done" || resp.Reasoning != "weighing it" {
		t.Fatalf("content %q, reasoning %q", resp.Content, resp.Reasoning)
	}
}

func TestACPProviderReasoningLevelIsAPreference(t *testing.T) {
	t.Run("a level an earlier turn set is undone", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{ThoughtLevels: []string{"max", "low"}})
		ctx := conversation(t, "s1")
		in.ReasoningEffort = "low"
		r, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		in.ReasoningEffort = ""
		if _, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a(r.Content), u("two")), nil, nil); err != nil {
			t.Fatal(err)
		}
		var values []string
		for _, r := range readLog(t, logPath, "session/set_config_option") {
			var p struct{ Value string }
			_ = json.Unmarshal(r.Params, &p)
			values = append(values, p.Value)
		}
		if strings.Join(values, ",") != "low,max" {
			t.Fatalf("thought selections = %v", values)
		}
	})
	t.Run("a later level keeps the baseline of the first override", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{ThoughtLevels: []string{"low", "medium", "high"}})
		ctx := conversation(t, "s1")
		in.ReasoningEffort = "high"
		first, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		in.ReasoningEffort = "medium"
		second, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a(first.Content), u("two")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		in.ReasoningEffort = ""
		if _, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a(first.Content), u("two"), a(second.Content), u("three")), nil, nil); err != nil {
			t.Fatal(err)
		}
		var values []string
		for _, r := range readLog(t, logPath, "session/set_config_option") {
			var p struct{ Value string }
			_ = json.Unmarshal(r.Params, &p)
			values = append(values, p.Value)
		}
		if strings.Join(values, ",") != "high,medium,low" {
			t.Fatalf("thought selections = %v, want the first turn's low restored", values)
		}
	})
	t.Run("the baseline follows the model selected before the level", func(t *testing.T) {
		in, logPath := acpRow(t, acptest.Script{
			Models: []string{"small", "large"},
			ThoughtLevelsByModel: map[string][]string{
				"small": {"low", "high"},
				"large": {"medium", "high"},
			},
		})
		ctx := conversation(t, "s1")
		in.Model = "large"
		in.ReasoningEffort = "high"
		r, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		in.ReasoningEffort = ""
		if _, err := newACPTestProvider(t, in).Stream(ctx, sys(u("one"), a(r.Content), u("two")), nil, nil); err != nil {
			t.Fatal(err)
		}
		var values []string
		for _, r := range readLog(t, logPath, "session/set_config_option") {
			var p struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			}
			if err := json.Unmarshal(r.Params, &p); err != nil {
				t.Fatal(err)
			}
			values = append(values, p.ConfigID+"="+p.Value)
		}
		if strings.Join(values, ",") != "model=large,thought=high,thought=medium" {
			t.Fatalf("selections = %v, want the large model's medium restored", values)
		}
	})
	t.Run("a refused level leaves the agent's own and the turn runs", func(t *testing.T) {
		in, _ := acpRow(t, acptest.Script{ThoughtLevels: []string{"max", "low"}, RefuseThought: "low"})
		in.ReasoningEffort = "low"
		if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil); err != nil {
			t.Fatalf("a refused reasoning level failed the turn: %v", err)
		}
	})
}

func TestACPProviderRefusesClientMethods(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{Turns: []acptest.Turn{{ClientRequest: "fs/read_text_file"}}})
	resp, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("read")), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The client advertises no fs or terminal capability: the agent's request
	// is answered with the protocol's method-not-found, never left waiting.
	if !strings.Contains(resp.Content, "client: Method not found") {
		t.Fatalf("answer = %q", resp.Content)
	}
}

func TestACPProviderAuthentication(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{AuthMethods: []string{"api-key", "chat-gpt"}, RequireAuth: "api-key"})
	_, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("hi")), nil, nil)
	var acpErr *ACPError
	if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorAuth || !strings.Contains(err.Error(), "api-key, chat-gpt") {
		t.Fatalf("err = %v", err)
	}
	StopACPAgents()
	in.ACP.AuthMethod = "api-key"
	if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("hi")), nil, nil); err != nil {
		t.Fatalf("with auth_method: %v", err)
	}
}

func TestACPProviderStopReasonsAndEmptyAnswers(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{Turns: []acptest.Turn{
		{Text: []string{"partial"}, StopReason: "max_tokens"},
		{Tools: []acptest.Tool{{ID: "t", Title: "Edit a.go"}}},
		{StopReason: "refusal"},
	}})
	p := newACPTestProvider(t, in)
	want := []string{
		"partial\n\n(the agent stopped: it reached its output limit)",
		acpEmptyAnswer,
		"(the agent refused to continue)",
	}
	for i, w := range want {
		resp, err := p.Stream(conversation(t, "s"+string(rune('a'+i))), sys(u("go")), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Content != w || resp.StopReason != "end_turn" {
			t.Errorf("turn %d: content %q stop %q, want %q end_turn", i, resp.Content, resp.StopReason, w)
		}
	}
}

func TestACPProviderAgentThatDiesMidPrompt(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Text: []string{"half"}, Exit: true}, {Echo: true}}})
	// Retries on: the wrapper must still not send a prompt the agent worked on.
	in.RetryDisabled, in.RetryMax, in.RetryBase = false, 2, time.Millisecond
	p := newACPTestProvider(t, in)
	ctx := conversation(t, "s1")
	resp, err := p.Stream(ctx, sys(u("one")), nil, nil)
	var acpErr *ACPError
	if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorExited || acpErr.retryable() {
		t.Fatalf("err = %v, want a final exited error", err)
	}
	if resp == nil || resp.Content != "half" {
		t.Fatalf("the partial answer is lost: %+v", resp)
	}
	if n := len(readLog(t, logPath, "session/prompt")); n != 1 {
		t.Fatalf("prompts = %d after a prompt the agent worked on, want it sent once", n)
	}
	// The next call starts the agent again and replays the history into a
	// new session.
	resp, err = p.Stream(ctx, sys(u("one"), u("two")), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Content, "one") || !strings.Contains(resp.Content, "two") {
		t.Fatalf("the new session did not get the history: %q", resp.Content)
	}
	pids := map[int]bool{}
	for _, r := range acptest.ReadLog(t, logPath) {
		pids[r.PID] = true
	}
	if len(pids) != 2 {
		t.Fatalf("agent processes = %d, want a second one after the first died", len(pids))
	}
}

func TestACPProviderRetriesAnAgentThatDiedBeforeAnyUpdate(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{ExitFirst: true}, {Echo: true}}})
	in.RetryDisabled, in.RetryMax, in.RetryBase = false, 1, time.Millisecond
	resp, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "echo: one" {
		t.Fatalf("answer = %q", resp.Content)
	}
	if n := len(readLog(t, logPath, "session/prompt")); n != 2 {
		t.Fatalf("prompts = %d, want the retry", n)
	}
}

func TestACPProviderRejectedPromptStartsANewSession(t *testing.T) {
	// "500" in the text: the wrapper decides by the error's kind, never by
	// a status it reads in what the agent said.
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Error: "upstream 500: model overloaded, connection reset by peer"}, {Echo: true}}})
	in.RetryDisabled, in.RetryMax, in.RetryBase = false, 2, time.Millisecond
	p := newACPTestProvider(t, in)
	ctx := conversation(t, "s1")
	_, err := p.Stream(ctx, sys(u("one")), nil, nil)
	var acpErr *ACPError
	if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorRPC || acpErr.retryable() {
		t.Fatalf("err = %v", err)
	}
	if n := len(readLog(t, logPath, "session/prompt")); n != 1 {
		t.Fatalf("prompts = %d, the wrapper must not resend a prompt the agent refused", n)
	}
	if isRetryableLLMError(err) || IsTransientProviderError(err) {
		t.Fatal("an rpc error of an agent must not be taken for a transient one")
	}
	if _, err := p.Stream(ctx, sys(u("one")), nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(readLog(t, logPath, "session/new")); n != 2 {
		t.Fatalf("session/new = %d, want a fresh session after the refusal", n)
	}
}

// Status updates of the agent's own say nothing about the prompt: a prompt
// that failed after them alone is not taken for one the agent acted on.
func TestACPProviderStatusUpdatesDoNotMarkAPromptAsRun(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Status: true, Error: "upstream overloaded"}, {Echo: true}}})
	p := newACPTestProvider(t, in)
	ctx := conversation(t, "s1")
	_, err := p.Stream(ctx, sys(u("one")), nil, nil)
	var acpErr *ACPError
	if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorRPC || acpErr.Updated {
		t.Fatalf("err = %v, want an rpc error not marked as acted on", err)
	}
	if _, err := p.Stream(ctx, sys(u("one")), nil, nil); err != nil {
		t.Fatalf("the same request after a refused prompt: %v", err)
	}
	if n := len(readLog(t, logPath, "session/new")); n != 2 {
		t.Fatalf("session/new = %d, want a fresh session for the retry", n)
	}
}

// A call never hangs on an agent that stopped reading its input: the call's
// own bound ends it, and the agent with it.
func TestACPProviderAgentThatStopsReading(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{DeafAfterNew: true})
	ctx, cancel := context.WithTimeout(conversation(t, "s1"), time.Second)
	defer cancel()
	big := strings.Repeat("x", 1<<20)
	start := time.Now()
	_, err := newACPTestProvider(t, in).Stream(ctx, sys(u(big)), nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the call took %v past its one-second bound", d)
	}
}

// The start of a shared agent belongs to the pool: a caller that gives up
// does not fail the start for the others.
func TestACPProviderStartOutlivesTheCallerThatAsked(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{StartDelayMS: 600})
	spec, err := resolveACPSpec(in, "", "")
	if err != nil {
		t.Fatal(err)
	}
	first, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	if _, _, err := acpAgents.acquire(first, spec); !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller: err = %v", err)
	}
	second, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	entry, release, err := acpAgents.acquire(second, spec)
	if err != nil {
		t.Fatalf("second caller: %v", err)
	}
	defer release()
	if !entry.proc.alive() {
		t.Fatal("the agent is not running")
	}
}

func TestACPProviderCancellation(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Text: []string{"working"}, WaitCancel: true}, {Echo: true}}})
	p := newACPTestProvider(t, in)
	base := conversation(t, "s1")
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	// Stop once the agent is at work, however long its start took.
	resp, err := p.Stream(ctx, sys(u("one")), nil, func(c StreamChunk) {
		if c.TextDelta != "" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if resp == nil || resp.Content != "working" {
		t.Fatalf("partial = %+v", resp)
	}
	if n := len(readLog(t, logPath, "session/cancel")); n != 1 {
		t.Fatalf("session/cancel = %d", n)
	}
	// The exact request again is not sent: the agent may have acted on it.
	_, err = p.Stream(base, sys(u("one")), nil, nil)
	var acpErr *ACPError
	if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorStalled {
		t.Fatalf("repeat after cancel: err = %v", err)
	}
	// A new message goes on in the same session. Coddy kept the partial
	// answer, which the agent's session holds too: only the new message goes.
	if _, err := p.Stream(base, sys(u("one"), a("working"), u("two")), nil, nil); err != nil {
		t.Fatal(err)
	}
	prompts := readLog(t, logPath, "session/prompt")
	if len(prompts) != 2 || decodePrompt(t, prompts[0]).SessionID != decodePrompt(t, prompts[1]).SessionID {
		t.Fatalf("prompts = %d", len(prompts))
	}
	if texts := promptTexts(t, prompts[1]); len(texts) != 1 || texts[0] != "text:two" {
		t.Fatalf("after a cancel the next prompt = %q", texts)
	}
}

// The loop's guard against a degenerating stream cuts a prompt the same way a
// person stops it, then calls again with the answer so far kept and a nudge:
// the agent gets the nudge alone, on the same session, and never its own
// words back as another model's.
func TestACPProviderGoesOnWithANudgeAfterACutPrompt(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Text: []string{"again and again"}, WaitCancel: true}, {Echo: true}}})
	p := newACPTestProvider(t, in)
	base := conversation(t, "s1")
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	if _, err := p.Stream(ctx, sys(u("one")), nil, func(c StreamChunk) {
		if c.TextDelta != "" {
			cancel()
		}
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	nudge := Message{Role: RoleUser, Content: "You are repeating yourself; go on.", Ephemeral: true}
	resp, err := p.Stream(base, sys(u("one"), a("again and again"), nudge), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "echo: "+nudge.Content {
		t.Fatalf("answer = %q", resp.Content)
	}
	prompts := readLog(t, logPath, "session/prompt")
	if len(prompts) != 2 || decodePrompt(t, prompts[0]).SessionID != decodePrompt(t, prompts[1]).SessionID {
		t.Fatalf("prompts = %d, want two on one session", len(prompts))
	}
}

func TestACPProviderAbandonsAPromptThatIgnoresItsCancel(t *testing.T) {
	defer func(c, a time.Duration) { acpCancelWait, acpAbandonWait = c, a }(acpCancelWait, acpAbandonWait)
	acpCancelWait, acpAbandonWait = 200*time.Millisecond, 500*time.Millisecond
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Text: []string{"stuck"}, IgnoreCancel: true}, {Echo: true}}})
	p := newACPTestProvider(t, in)
	base := conversation(t, "s1")
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	var cancelledAt time.Time
	if _, err := p.Stream(ctx, sys(u("one")), nil, func(c StreamChunk) {
		if c.TextDelta != "" && cancelledAt.IsZero() {
			cancelledAt = time.Now()
			cancel()
		}
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if cancelledAt.IsZero() {
		t.Fatal("the stuck prompt never streamed")
	}
	if d := time.Since(cancelledAt); d > 5*time.Second {
		t.Fatalf("the cancelled call took %v after its cancel", d)
	}
	// The exact request again is refused even though its session is gone:
	// the agent may have acted on it.
	_, err := p.Stream(base, sys(u("one")), nil, nil)
	var acpErr *ACPError
	if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorStalled {
		t.Fatalf("repeat after abandonment: err = %v", err)
	}
	// A new message runs on a new agent once the abandoned one is gone.
	resp, err := p.Stream(base, sys(u("one"), u("two")), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Content, "two") {
		t.Fatalf("answer = %q", resp.Content)
	}
	pids := map[int]bool{}
	for _, r := range acptest.ReadLog(t, logPath) {
		pids[r.PID] = true
	}
	if len(pids) != 2 {
		t.Fatalf("agent processes = %d, want the stuck one replaced", len(pids))
	}
}

func TestACPProviderSilenceBound(t *testing.T) {
	defer func(s, c time.Duration) { acpSilence, acpCancelWait = s, c }(acpSilence, acpCancelWait)
	acpSilence, acpCancelWait = 200*time.Millisecond, 2*time.Second
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{{WaitCancel: true}}})
	_, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil)
	var acpErr *ACPError
	if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorStalled {
		t.Fatalf("err = %v", err)
	}
	if p, c := len(readLog(t, logPath, "session/prompt")), len(readLog(t, logPath, "session/cancel")); p != 1 || c != 1 {
		t.Fatalf("prompts = %d, cancels = %d; a silent prompt is cancelled the protocol's way", p, c)
	}
}

func TestACPProviderStopsAnIdleAgent(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{})
	in.ACP.IdleTimeoutMS = 100
	if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil); err != nil {
		t.Fatal(err)
	}
	acpAgents.mu.Lock()
	var proc *acpProcess
	for _, e := range acpAgents.entries {
		proc = e.proc
	}
	acpAgents.mu.Unlock()
	if proc == nil {
		t.Fatal("no agent ran")
	}
	select {
	case <-proc.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the idle agent process is still running")
	}
}

func TestACPProviderAgentEnvironment(t *testing.T) {
	t.Setenv("NO_PROXY", "*")
	t.Setenv("ALL_PROXY", "socks5://inherited:1")
	in, _ := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Env: []string{ACPDepthEnv, "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "ROW_VAR"}}}})
	in.ProxyURL = "http://proxy.test:3128"
	in.ACP.Env = map[string]string{"ROW_VAR": "set"}
	resp, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("env")), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{ACPDepthEnv + "=1", "HTTPS_PROXY=http://proxy.test:3128", "NO_PROXY=localhost,127.0.0.1,::1", "ALL_PROXY=\n", "ROW_VAR=set"} {
		if !strings.Contains(resp.Content+"\n", want) {
			t.Errorf("agent environment lacks %q:\n%s", want, resp.Content)
		}
	}
}

func TestACPProviderDepthGuard(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{})
	t.Setenv(ACPDepthEnv, "3")
	_, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("hi")), nil, nil)
	if err == nil || !strings.Contains(err.Error(), ACPDepthEnv) {
		t.Fatalf("err = %v", err)
	}
	if len(acptest.ReadLog(t, logPath)) != 0 {
		t.Fatal("an agent started past the depth limit")
	}
}

func TestACPThoughtLevelsReadsTheModelTheAgentIsOn(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{
		Models:        []string{"grok-4.7", "small"},
		ThoughtLevels: []string{"low", "medium", "high", "xhigh"},
	})
	got, err := ACPThoughtLevels(context.Background(), in, "grok-4.7")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "low,medium,high,xhigh" {
		t.Fatalf("levels = %v", got)
	}
	legacy, err := ACPThoughtLevels(context.Background(), in, "grok-4.7[reasoning_effort=high,fast=true]")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(legacy, ",") != "low,medium,high,xhigh" {
		t.Fatalf("legacy variant levels = %v", legacy)
	}
	other, err := ACPThoughtLevels(context.Background(), in, "small")
	if err != nil {
		t.Fatal(err)
	}
	if other != nil {
		t.Fatalf("another model = %v, want nothing rather than a selection", other)
	}
	if n := len(readLog(t, logPath, "session/set_config_option")); n != 0 {
		t.Fatalf("reading the levels sent %d set_config_option", n)
	}
}

// A boolean thought toggle can share the thought_level category with the
// option that carries the levels. The values of the toggle are not levels;
// the first enum-shaped option of the category is.
func TestACPThoughtLevelsSkipsABooleanToggleForTheLevelOption(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{
		Models: []string{"claude-opus-4-8"},
		Parameters: []acptest.Parameter{
			{ID: "thinking", Category: "thought_level", Values: []string{"false", "true"}},
			{ID: "context", Values: []string{"300k", "1m"}},
			{ID: "effort", Category: "thought_level", Values: []string{"low", "medium", "high", "xhigh", "max"}},
		},
	})
	got, err := ACPThoughtLevels(context.Background(), in, "claude-opus-4-8")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "low,medium,high,xhigh,max" {
		t.Fatalf("levels = %v", got)
	}
}

// An exact bracketed model id remains a different model for the probe, just as
// it is for selection: the compatibility base fallback applies only when the
// exact id is not offered.
func TestACPThoughtLevelsDoesNotFallbackAcrossAnExactVariantID(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{
		Models:        []string{"small", "small[fast=true]"},
		ThoughtLevels: []string{"low", "high"},
	})
	got, err := ACPThoughtLevels(context.Background(), in, "small[fast=true]")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("exact but non-current model levels = %v, want nothing", got)
	}
}

// A levels fetch does not wait forever when the agent accepts session/new
// but never answers it.
func TestACPThoughtLevelsBoundsANewSession(t *testing.T) {
	old := acpSessionNewTimeout
	acpSessionNewTimeout = 100 * time.Millisecond
	t.Cleanup(func() { acpSessionNewTimeout = old })
	in, _ := acpRow(t, acptest.Script{
		HoldNew:       true,
		Models:        []string{"grok-4.7"},
		ThoughtLevels: []string{"low"},
	})
	start := time.Now()
	_, err := ACPThoughtLevels(context.Background(), in, "grok-4.7")
	if err == nil {
		t.Fatal("a silent session/new unexpectedly answered")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the fetch took %v, want the session/new bound to cut it", elapsed)
	}
}

func TestACPListModelsEscapesAnAgentModelNamedDefault(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{Models: []string{"default", "m1"}, Close: true})
	models, err := ListModels(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "default,default[],m1" {
		t.Fatalf("model ids = %v", ids)
	}
}

// The legacy models API has no parameter options, but an empty parameter
// list needs none: default[] selects a literal model named default there too.
func TestACPListModelsEscapesLegacyDefault(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Models: []string{"m1", "default", "m2"}, LegacyModels: true, Close: true})
	models, err := ListModels(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "default,m1,default[],m2" {
		t.Fatalf("model ids = %v", ids)
	}
	in.Model = "default[]"
	if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s-legacy-default"), sys(u("one")), nil, nil); err != nil {
		t.Fatal(err)
	}
	sets := readLog(t, logPath, "session/set_model")
	if len(sets) != 1 || !strings.Contains(string(sets[0].Params), `"modelId":"default"`) {
		t.Fatalf("set_model = %v", sets)
	}
}

func TestACPListModels(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Models: []string{"m1", "m2"}, Close: true})
	models, err := ListModels(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "default,m1,m2" {
		t.Fatalf("models = %v", ids)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(readLog(t, logPath, "session/close")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the listing session was not closed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A call whose context has already ended hands the agent nothing, and a
// healthy agent is never stopped for it.
func TestACPProcessCancelledCallKeepsTheAgent(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{})
	spec, err := resolveACPSpec(in, "s1", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := startACPProcess(context.Background(), spec, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer p.stop()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 200; i++ {
		_ = p.call(ctx, "session/new", acpsdk.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acpsdk.McpServer{}}, nil)
		if p.closing.Load() {
			t.Fatalf("attempt %d: a cancelled call stopped a healthy agent", i)
		}
	}
}

// Cancelled turns of one session leave the agent every session shares
// running.
func TestACPProviderCancelledTurnKeepsTheSharedAgent(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{})
	p := newACPTestProvider(t, in)
	s1 := conversation(t, "s1")
	if _, err := p.Stream(s1, sys(u("hi")), nil, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		ctx, cancel := context.WithCancel(conversation(t, "s2"))
		cancel()
		_, _ = p.Stream(ctx, sys(u("hello")), nil, nil)
	}
	if _, err := p.Stream(s1, sys(u("hi"), a("echo: hi"), u("again")), nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(readLog(t, logPath, "initialize")); n != 1 {
		t.Fatalf("the agent was started %d times", n)
	}
}

// A prompt that failed after the agent acted is remembered by the pool, not
// only by its binding: once the binding is gone (here the idle stop), the
// same history is refused rather than run again in a new session.
func TestACPProviderRemembersAnUncertainPromptPastItsBinding(t *testing.T) {
	in, logPath := acpRow(t, acptest.Script{Turns: []acptest.Turn{
		{Tools: []acptest.Tool{{ID: "t1", Title: "Edit main.go"}}, Error: "upstream failed", ErrorAfterTools: true},
		{Echo: true},
	}})
	p := newACPTestProvider(t, in)
	ctx := conversation(t, "s1")
	history := sys(u("fix it"))
	var acpErr *ACPError
	if _, err := p.Stream(ctx, history, nil, nil); !errors.As(err, &acpErr) || !acpErr.Updated {
		t.Fatalf("err = %v, want a failure after the agent acted", err)
	}
	acpAgents.mu.Lock()
	var entry *acpPoolEntry
	for _, e := range acpAgents.entries {
		entry = e
	}
	acpAgents.mu.Unlock()
	acpAgents.expire(entry)
	if _, err := p.Stream(ctx, history, nil, nil); !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorStalled {
		t.Fatalf("err = %v, want the repeat refused", err)
	}
	if n := len(readLog(t, logPath, "session/prompt")); n != 1 {
		t.Fatalf("prompts = %d, want the uncertain one sent once", n)
	}
	resp, err := p.Stream(ctx, sys(u("fix it"), u("is it fixed?")), nil, nil)
	if err != nil || !strings.Contains(resp.Content, "is it fixed?") {
		t.Fatalf("a new message must still go on: %v %+v", err, resp)
	}
}

// A line the agent never takes is bounded whoever waits for it: the agent
// is stopped and the call fails, with no caller cancelling anything.
func TestACPProviderBoundsAWriteTheAgentNeverReads(t *testing.T) {
	old := acpWriteTimeout
	acpWriteTimeout = 300 * time.Millisecond
	t.Cleanup(func() { acpWriteTimeout = old })
	in, _ := acpRow(t, acptest.Script{DeafAfterNew: true})
	start := time.Now()
	_, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u(strings.Repeat("x", 1<<20))), nil, nil)
	var acpErr *ACPError
	if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorExited {
		t.Fatalf("err = %v, want the agent retired", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("the write took %v past its bound", d)
	}
}

// Shutdown does not wait out a handshake that hangs.
func TestACPStopAgentsCancelsAHangingStart(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{StartDelayMS: 60000})
	ctx, cancel := context.WithTimeout(conversation(t, "s1"), 300*time.Millisecond)
	defer cancel()
	if _, err := newACPTestProvider(t, in).Stream(ctx, sys(u("hi")), nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	start := time.Now()
	StopACPAgents()
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("stopping the agents took %v", d)
	}
}

// While kill stops an agent, a caller waits for it to be gone instead of
// starting a second agent next to it.
func TestACPPoolWaitsForAnAgentBeingKilled(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{})
	spec, err := resolveACPSpec(in, "s1", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pl := newACPPool()
	t.Cleanup(pl.stopAll)
	ready := make(chan struct{})
	close(ready)
	dying := &acpPoolEntry{spec: spec, proc: &acpProcess{done: make(chan struct{})}, ready: ready, dying: true, conversations: map[string]*acpConversation{}}
	pl.entries[spec.key] = dying
	got := make(chan *acpPoolEntry, 1)
	go func() {
		e, release, err := pl.acquire(context.Background(), spec)
		if err != nil {
			t.Error(err)
			got <- nil
			return
		}
		release()
		got <- e
	}()
	select {
	case <-got:
		t.Fatal("acquire did not wait for the agent being killed")
	case <-time.After(200 * time.Millisecond):
	}
	close(dying.proc.done)
	select {
	case e := <-got:
		if e == nil || e == dying || e.proc == nil || !e.proc.alive() {
			t.Fatalf("acquire after the kill = %+v, want a new running agent", e)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("acquire never returned")
	}
}

// An agent that names its tool beside its title (ACP's toolCall.name) gives a
// row it can be recognised by: a title that is only the tool's bare name reads
// as the tool the agent actually ran, not as the kind every other row shares.
func TestACPProviderTitlesAnAgentToolByItsProgrammaticName(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{Turns: []acptest.Turn{{
		Tools: []acptest.Tool{{
			ID:     "t1",
			Title:  "read",
			Name:   "fs_read_file",
			Kind:   "read",
			Input:  map[string]string{"file_path": "README.md"},
			Output: "# Readme",
		}},
		Text: []string{"done"},
	}}})
	tools := map[string]AgentToolCall{}
	if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("hi")), nil, func(c StreamChunk) {
		if c.AgentTool != nil {
			tools[c.AgentTool.ID] = *c.AgentTool
		}
	}); err != nil {
		t.Fatal(err)
	}
	got, ok := tools["t1"]
	if !ok {
		t.Fatalf("no tool row in %#v", tools)
	}
	if got.ToolName != "fs_read_file" {
		t.Errorf("ToolName = %q, want fs_read_file", got.ToolName)
	}
	if got.Title != "fs_read_file README.md" {
		t.Errorf("Title = %q, want the tool name and what it works on", got.Title)
	}
}

// A title the agent wrote itself is what a person reads: the programmatic name
// never overrides it.
func TestACPProviderKeepsAnAgentsOwnTitleOverTheToolName(t *testing.T) {
	in, _ := acpRow(t, acptest.Script{Turns: []acptest.Turn{{
		Tools: []acptest.Tool{{
			ID:     "t1",
			Title:  "Read the changelog",
			Name:   "fs_read_file",
			Kind:   "read",
			Output: "changelog",
		}},
		Text: []string{"done"},
	}}})
	tools := map[string]AgentToolCall{}
	if _, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("hi")), nil, func(c StreamChunk) {
		if c.AgentTool != nil {
			tools[c.AgentTool.ID] = *c.AgentTool
		}
	}); err != nil {
		t.Fatal(err)
	}
	got, ok := tools["t1"]
	if !ok {
		t.Fatalf("no tool row in %#v", tools)
	}
	if got.Title != "Read the changelog" {
		t.Errorf("Title = %q, want the agent's own title", got.Title)
	}
	if got.ToolName != "fs_read_file" {
		t.Errorf("ToolName = %q, want it kept beside the title", got.ToolName)
	}
}
