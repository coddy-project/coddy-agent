package agent

// The performance group: benchmarks only, run by `make test-perf` and never by
// `make test`. Two kinds of cost are measured here: the time a turn spends on
// the rules (the look for what a tool call brings in, the send boundary that
// joins them to their results), and the context a session of a real project
// sends, reported as token metrics next to the timings.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// longRulesSession is a session of turns*2 tool steps whose rules were all
// delivered early, so every later call finds nothing new after looking through
// the whole history.
func longRulesSession(b *testing.B, turns int) (*Agent, string) {
	b.Helper()
	cwd := b.TempDir()
	writeGlobRule(b, cwd, "gofiles", "**/*.go", "PERF_GO_RULE_TOKEN")
	a := rulesTestAgent(b, cwd)
	body := strings.Repeat("x := 1 // a line of the file that was read\n", 12)
	for i := 0; i < turns; i++ {
		a.state.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("question %d", i)})
		tc := readCall(fmt.Sprintf("r%d", i), fmt.Sprintf("pkg%d/file.go", i))
		a.state.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{tc}})
		a.state.AddMessage(toolResultMessage(tc, body, nil, a.toolCallRules("agent", tc, cwd)))
		a.state.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)})
	}
	return a, cwd
}

// BenchmarkToolCallRulesOnALongSession is what every filesystem tool call pays
// to find the rules it brings in, on a session of 2000 messages.
func BenchmarkToolCallRulesOnALongSession(b *testing.B) {
	a, cwd := longRulesSession(b, 500)
	tc := readCall("again", "main.go")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := a.toolCallRules("agent", tc, cwd); got != "" {
			b.Fatalf("a delivered rule was attached again: %q", got)
		}
	}
}

// BenchmarkSendBoundaryOnALongSession is what every request pays to join the
// rules to the results that carry them.
func BenchmarkSendBoundaryOnALongSession(b *testing.B) {
	a, _ := longRulesSession(b, 500)
	msgs := a.state.GetMessages()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if sent := withToolRules(msgs); len(sent) != len(msgs) {
			b.Fatalf("sent %d messages, want %d", len(sent), len(msgs))
		}
	}
}

// BenchmarkContextOfThisRepositoryRules runs a scripted session over a copy of
// this repository's own rules - the Cursor folder and its Claude Code mirror,
// the case of issue #366 - and reports what its requests cost the context:
// the system message, the last request, and how many times the last request
// carries a rule the Claude Code mirror holds a copy of (duplicates, which
// should be none). The timing is the whole session and matters less than the
// metrics.
func BenchmarkContextOfThisRepositoryRules(b *testing.B) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		b.Fatal(err)
	}
	var systemTokens, requestTokens, duplicates int
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cwd := b.TempDir()
		for _, folder := range []string{".cursor/rules", ".claude/rules"} {
			if err := copyTree(filepath.Join(repo, filepath.FromSlash(folder)), filepath.Join(cwd, filepath.FromSlash(folder))); err != nil {
				b.Skipf("this checkout has no %s to measure: %v", folder, err)
			}
		}
		for _, f := range []string{"main.go", "internal/api/handler.go", "external/ui/src/App.tsx"} {
			path := filepath.Join(cwd, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("// "+f+"\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
		cfg := &config.Config{
			Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
			Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 262144}},
			Agent:     config.Agent{Model: "fake/model"},
			Tools:     config.Tools{PermissionMode: config.PermModeBypass},
		}
		cfg.Agent.ApplyDefaults()
		cfg.Prompts.ApplyDefaults()
		st := &session.State{ID: "sess_perf_rules", CWD: cwd, Mode: session.ModeAgent}
		st.ReplaceRulesCatalog(session.DiscoverRules(cfg, cwd))
		ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
		provider := &pcScriptProvider{}
		ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
		b.StartTimer()

		for turn, paths := range [][]string{{"main.go", "internal/api/handler.go"}, {"external/ui/src/App.tsx", "main.go"}} {
			var steps []pcStep
			for j, p := range paths {
				steps = append(steps, pcStep{calls: []llm.ToolCall{pcReadCall(fmt.Sprintf("t%d-r%d", turn, j), p)}})
			}
			provider.steps, provider.i = append(steps, pcStep{text: "done"}), 0
			if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "look around"}}); err != nil {
				b.Fatal(err)
			}
		}

		b.StopTimer()
		last := provider.seen[len(provider.seen)-1]
		var all strings.Builder
		for _, m := range last {
			all.WriteString(m.Content)
		}
		systemTokens = session.EstimateTokens(last[0].Content)
		requestTokens = session.EstimateTokens(all.String())
		duplicates = mirroredRuleRepeats(b, repo, all.String())
		b.StartTimer()
	}
	b.ReportMetric(float64(systemTokens), "system-tokens")
	b.ReportMetric(float64(requestTokens), "request-tokens")
	b.ReportMetric(float64(duplicates), "duplicate-rules")
}

// BenchmarkToolOfferOfAModelAllowlist measures what a toolset spares a
// small-context local model: the tools a request offers, what the context
// meter counts for them (the list the system message prints plus the schemas)
// the size of the tools array as an OpenAI-compatible request carries it and
// of the tool list the system message prints, for a model with no lists and for the dozen tools a coding session on a local
// model ever calls. The timing is the building of both sets and matters less
// than the metrics.
func BenchmarkToolOfferOfAModelAllowlist(b *testing.B) {
	allow := []string{"read", "write", "edit", "apply_patch", "glob", "grep", "print_tree", "keep_result",
		"run_command", "background_output", "background_wait", "background_stop", "compact_context"}
	var tools, meterTokens, jsonTokens, promptTokens [2]int
	for i := 0; i < b.N; i++ {
		ag, st, _ := modelToolsAgent(b, session.ModeAgent, "fake/full",
			toolRow{Model: "fake/full"}, toolRow{Model: "fake/small", Tools: allow})
		for k, model := range []string{"fake/full", "fake/small"} {
			st.SetSelectedModelID(model)
			defs := ag.currentToolDefinitions("agent")
			sys := ag.buildSystemPromptParts("agent", nil, defs)
			tools[k] = len(defs)
			promptTokens[k] = session.EstimateContextTokens(sys.ToolsMD)
			meterTokens[k] = computeContextBreakdown(sys.Content, sys.SkillsMD, sys.ToolsMD, sys.RulesMD, nil, false, defs).ToolDefinitions
			wire := make([]map[string]interface{}, 0, len(defs))
			for _, d := range defs {
				wire = append(wire, map[string]interface{}{"type": "function", "function": map[string]interface{}{
					"name": d.Name, "description": d.Description, "parameters": d.InputSchema}})
			}
			raw, err := json.Marshal(wire)
			if err != nil {
				b.Fatal(err)
			}
			jsonTokens[k] = session.EstimateContextTokens(string(raw))
		}
	}
	b.ReportMetric(float64(tools[0]), "tools-full")
	b.ReportMetric(float64(tools[1]), "tools-allowlist")
	b.ReportMetric(float64(meterTokens[0]), "tool-context-tokens-full")
	b.ReportMetric(float64(meterTokens[1]), "tool-context-tokens-allowlist")
	b.ReportMetric(float64(jsonTokens[0]), "tools-json-tokens-full")
	b.ReportMetric(float64(jsonTokens[1]), "tools-json-tokens-allowlist")
	b.ReportMetric(float64(promptTokens[0]), "tools-prompt-tokens-full")
	b.ReportMetric(float64(promptTokens[1]), "tools-prompt-tokens-allowlist")
}

// mirroredRuleRepeats counts the rules of the Claude Code mirror whose body
// the request carries more than once.
func mirroredRuleRepeats(b *testing.B, repo, request string) int {
	b.Helper()
	entries, err := os.ReadDir(filepath.Join(repo, ".claude", "rules"))
	if err != nil {
		return 0
	}
	repeats := 0
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(repo, ".claude", "rules", e.Name()))
		if err != nil {
			continue
		}
		// The first line of the body after the header is distinctive enough
		// to find each copy of the rule by.
		parts := strings.SplitN(string(data), "\n---\n", 2)
		body := strings.TrimSpace(parts[len(parts)-1])
		line, _, _ := strings.Cut(body, "\n")
		if line = strings.TrimSpace(line); line != "" && strings.Count(request, line) > 1 {
			repeats++
		}
	}
	return repeats
}

// copyTree copies the regular files under src to dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// Compare the old joined-text estimate with counting message by message on
// the same history. Fixture allocations are outside both measurements.
func BenchmarkConversationTokensLongSession(b *testing.B) {
	msgs := make([]llm.Message, 0, 3000)
	for i := 0; i < 750; i++ {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleUser, Content: strings.Repeat("Please look at the session manager and explain. ", 6)},
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{Name: "read", InputJSON: `{"path":"internal/session/manager.go"}`}}},
			llm.Message{Role: llm.RoleTool, Content: strings.Repeat("func x() {}\n", 120)},
			llm.Message{Role: llm.RoleAssistant, Content: "Here is the explanation.\n\n```go\nfunc main() {}\n```\n" + strings.Repeat("Lorem ipsum dolor sit amet. ", 10)},
		)
	}
	b.Run("joined", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = referenceConversationTokens(msgs, false)
		}
	})
	b.Run("counts", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = conversationTokens(msgs, false)
		}
	})
}

// BenchmarkPromptSuggestionCacheSplit measures what one prompt suggestion
// costs over a session of a realistic size, read off the wire of the Anthropic
// provider, which is the one that places the cache marks explicitly. A real
// turn runs first, then the suggestion its surface asks for, and both requests
// are split against the marks they carry:
//
//   - cache-read-tokens, the prefix the turn already cached, which the
//     suggestion reads back at a tenth of the input price;
//   - cache-write-tokens, what it writes on top of that prefix, at a quarter
//     above the input price;
//   - reusable-write-tokens, how much of that write the operator's next turn
//     reads back instead of processing again. It is zero when the mark sits on
//     a message no later request repeats, which makes the write pure waste;
//   - fresh-tokens, what no cache holds and nothing writes: the instruction.
//
// The numbers scale with the history, so read them as the shape of the cost,
// not as a bound: what matters is that the read part carries the history and
// the written and fresh parts stay small. A change that moves the mark onto
// the instruction, or that sends thinking parameters the turn did not, turns
// the read part into fresh tokens; TestPromptCacheSuggestionReusesTheTurnsPrefix
// fails first when it does.
func BenchmarkPromptSuggestionCacheSplit(b *testing.B) {
	// Both halves of the switch: a session that asks the model to think pays
	// the whole history again if the side call's thinking parameters drift
	// from the turn's, which is what ties the output bound to the cache.
	for _, reasoning := range []string{"", "high"} {
		name := "no-reasoning"
		if reasoning != "" {
			name = "reasoning-" + reasoning
		}
		b.Run(name, func(b *testing.B) {
			var cost suggestionCost
			for i := 0; i < b.N; i++ {
				cost = measurePromptSuggestionCache(b, reasoning)
			}
			b.ReportMetric(float64(cost.read), "cache-read-tokens")
			b.ReportMetric(float64(cost.written), "cache-write-tokens")
			b.ReportMetric(float64(cost.reusable), "reusable-write-tokens")
			b.ReportMetric(float64(cost.fresh), "fresh-tokens")
			b.ReportMetric(float64(cost.turn), "turn-request-tokens")
		})
	}
}

// suggestionCost is one suggestion request split against the cache marks of
// the turn before it.
type suggestionCost struct{ read, written, reusable, fresh, turn int }

// measurePromptSuggestionCache runs one turn and one suggestion against a
// stub Messages API and returns the suggestion's token split plus what the
// turn itself sent.
func measurePromptSuggestionCache(b *testing.B, reasoning string) suggestionCost {
	b.Helper()
	b.StopTimer()
	defer b.StartTimer()

	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if len(raw) == 0 {
			// The manager's model lookup, which asks for no completion.
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		mu.Lock()
		n := len(bodies)
		bodies = append(bodies, string(raw))
		mu.Unlock()
		answer := "Fixed it, and the tests are green."
		if n > 0 {
			answer = "run the tests"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"msg","type":"message","role":"assistant","model":"model","content":[{"type":"text","text":%q}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`, answer)
	}))
	defer server.Close()

	home := b.TempDir()
	blocking, on, zero := false, true, 0
	levels := []string{"low", "medium", "high"}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: home},
		Providers: []config.ProviderConfig{{Name: "a", Type: "anthropic", APIBase: server.URL, APIKey: "test", Proxy: "none"}},
		Models:    []config.ModelEntry{{Model: "a/model", MaxTokens: 8192, Stream: &blocking, ReasoningLevels: &levels, ReasoningDefault: reasoning}},
		Agent:     config.Agent{Model: "a/model", MaxTurns: 2, PromptSuggestions: &on, LLMRetryMax: &zero},
	}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		return NewAgent(cfg, st, sender, nil).Run(ctx, prompt)
	}
	mgr := session.NewManager(cfg, resumePermissionSender{}, runner, slog.New(slog.DiscardHandler), home, nil)
	defer mgr.CloseMCP()
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: home})
	if err != nil {
		b.Fatal(err)
	}
	st := mgr.SessionByID(created.SessionID)
	// A session already underway: fifty exchanges of the size a working
	// conversation carries, so the cached prefix is the bulk of the request.
	for i := 0; i < 50; i++ {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("step %d: have a look at the handler and tell me what it does", i)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("step %d: %s", i, strings.Repeat("It reads the request, resolves the session and writes the answer back. ", 16))})
	}
	if _, err := mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: created.SessionID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "fix the bug and run the tests"}},
	}, nil, nil); err != nil {
		b.Fatal(err)
	}
	if _, err := mgr.GeneratePromptSuggestion(context.Background(), created.SessionID); err != nil {
		b.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		b.Fatalf("requests = %d, want the turn and its suggestion", len(bodies))
	}
	turn, suggestion := messageTokens(b, bodies[0]), messageTokens(b, bodies[1])
	if (reasoning != "") != (turn.thinking != "") {
		b.Fatalf("the turn ran with thinking %q at reasoning %q", turn.thinking, reasoning)
	}
	cost := suggestionCost{turn: total(turn.tokens)}
	// The suggestion repeats the turn's messages, so everything up to the mark
	// the turn left is read back; what follows it up to the suggestion's own
	// mark is written, and the rest is processed at full price.
	for i, n := range suggestion.tokens {
		switch {
		case i <= turn.mark:
			cost.read += n
		case i <= suggestion.mark:
			cost.written += n
		default:
			cost.fresh += n
		}
	}
	if turn.thinking != suggestion.thinking {
		// Anthropic drops the cached message blocks when a request's thinking
		// parameters move, so a side call that sends its own reads none of
		// them and leaves nothing a turn with the turn's parameters can read.
		cost.fresh += cost.read
		cost.read = 0
	} else if suggestion.mark < len(suggestion.tokens)-1 {
		// The mark is behind the trailing instruction, so the prefix it names
		// is one the next turn repeats and reads back.
		cost.reusable = cost.written
	}
	return cost
}

type markedRequest struct {
	tokens   []int
	mark     int
	thinking string
}

// messageTokens reads a Messages API request body into the estimated token
// count of each message and the index of the one carrying the cache mark.
func messageTokens(b *testing.B, body string) markedRequest {
	b.Helper()
	out := markedRequest{mark: -1, thinking: gjson.Get(body, "thinking").Raw}
	for i, msg := range gjson.Get(body, "messages").Array() {
		var text strings.Builder
		for _, block := range msg.Get("content").Array() {
			text.WriteString(block.Get("text").String())
			if block.Get("cache_control").Exists() {
				out.mark = i
			}
		}
		out.tokens = append(out.tokens, session.EstimateTokens(text.String()))
	}
	if out.mark < 0 {
		b.Fatalf("request carries no cache mark: %.200s", body)
	}
	return out
}

func total(ns []int) int {
	sum := 0
	for _, n := range ns {
		sum += n
	}
	return sum
}

// BenchmarkCompactionCacheSplit measures what a long session costs a provider
// that caches by prefix: the model reads a large file and answers, turn after
// turn, until the window has been folded several times. Every request the
// session makes - the turns and the summarizer's own - is split against the
// requests before it:
//
//   - raw-tokens, everything the requests carry;
//   - cached-tokens, the leading messages some earlier request already sent,
//     which the provider reads back at about a tenth of the input price;
//   - fresh-tokens, the rest, which it processes at full price;
//   - effective-units, fresh plus a tenth of cached: the bill, in units of one
//     fresh token;
//   - rewrites, requests that changed a message an earlier one carried rather
//     than only adding to it. Each one is a cached prefix thrown away.
//
// Read them as the shape of the cost, not as a bound: the numbers move with the
// size of the file and the window. What matters is that the folds stay small
// beside the turns and that the rewrites stay few. A fold that stops repeating
// the turn's own prefix (compact_cached.go), or results that go back to
// collapsing one at a time (resultEvictionBatch), show up here as fresh tokens
// and rewrites; TestPromptCacheCompactionRepeatsTheRecordedRequest and the
// prompt_cache_compaction.feature scenarios fail first when they do.
func BenchmarkCompactionCacheSplit(b *testing.B) {
	var split cacheSplit
	for i := 0; i < b.N; i++ {
		split = measureCompactionCache(b)
	}
	b.ReportMetric(float64(split.requests), "requests")
	b.ReportMetric(float64(split.folds), "folds")
	b.ReportMetric(float64(split.raw), "raw-tokens")
	b.ReportMetric(float64(split.cached), "cached-tokens")
	b.ReportMetric(float64(split.fresh), "fresh-tokens")
	b.ReportMetric(float64(split.cached/10+split.fresh), "effective-units")
	b.ReportMetric(float64(split.rewrites), "rewrites")
}

// cacheSplitTurns is how many turns the measured session runs.
const cacheSplitTurns = 30

// cacheSplit is one session's requests measured against the prompt cache.
type cacheSplit struct{ requests, folds, raw, cached, fresh, rewrites int }

// cacheSplitProvider answers a turn with one read and one reply, and any
// blocking call (the summarizer) with a short summary, keeping every request.
type cacheSplitProvider struct {
	file  string
	turn  int
	step  int
	seen  [][]llm.Message
	folds int
}

func (p *cacheSplitProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	p.folds++
	return &llm.Response{Content: fmt.Sprintf("Summary of the work so far, part %d.", p.folds), StopReason: "end_turn"}, nil
}

func (p *cacheSplitProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	p.step++
	if p.step%2 == 1 {
		call := llm.ToolCall{
			ID:        fmt.Sprintf("read_%03d", p.turn),
			Name:      "read",
			InputJSON: fmt.Sprintf(`{"path":%q}`, p.file),
		}
		return &llm.Response{ToolCalls: []llm.ToolCall{call}, StopReason: "tool_use"}, nil
	}
	// The answers are what a fold cannot evict, so they are what pushes the
	// session past the threshold turn after turn. Every line differs: a reply
	// that repeats one phrase is cut off by the repetition guard.
	var text strings.Builder
	fmt.Fprintf(&text, "I read %s and here is what it does.\n", p.file)
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&text, "Call site %d of %s wraps the error of run(ctx, opts) and returns it to its caller.\n", i, p.file)
	}
	onChunk(llm.StreamChunk{TextDelta: text.String()})
	return &llm.Response{Content: text.String(), StopReason: "end_turn"}, nil
}

// measureCompactionCache runs a session of thirty turns over a 200k window and
// splits every request it made against the ones before it.
func measureCompactionCache(b *testing.B) cacheSplit {
	b.Helper()
	b.StopTimer()
	defer b.StartTimer()

	cwd := b.TempDir()
	// About 6k tokens a turn, which fills the window below every few turns.
	body := strings.Repeat("\tif err := run(ctx, opts); err != nil { return fmt.Errorf(\"run: %w\", err) }\n", 240)
	// One file per turn: a model that asks for the same page twice is stopped
	// by the loop guard, which is not what this measures.
	for i := 0; i < cacheSplitTurns; i++ {
		name := fmt.Sprintf("service_%02d.go", i)
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("package service\n\n"+body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	provider := &cacheSplitProvider{}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 4096, MaxContextTokens: 60000}},
		Agent:     config.Agent{Model: "fake/model"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	store := b.TempDir()
	st := &session.State{ID: "sess_cache_split", CWD: cwd, Mode: session.ModeAgent, SessionDir: store}
	st.ReplaceRulesCatalog(session.DiscoverRules(cfg, cwd))
	ag := NewAgent(cfg, st, resumePermissionSender{}, slog.New(slog.DiscardHandler))
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	for turn := 0; turn < cacheSplitTurns; turn++ {
		provider.turn, provider.step = turn, 0
		provider.file = fmt.Sprintf("service_%02d.go", turn)
		prompt := fmt.Sprintf("Look at %s and tell me about part %d.", provider.file, turn)
		if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: prompt}}); err != nil {
			b.Fatalf("turn %d failed: %v", turn, err)
		}
	}
	if provider.folds < 2 {
		b.Fatalf("the session folded %d time(s): the fixture no longer reaches the threshold", provider.folds)
	}
	return splitAgainstTheCache(provider.seen, provider.folds)
}

// splitAgainstTheCache measures each request against every request before it:
// its cached part is the longest run of leading messages one of them carried,
// which is what a provider that caches by prefix serves without reading.
func splitAgainstTheCache(reqs [][]llm.Message, folds int) cacheSplit {
	out := cacheSplit{requests: len(reqs), folds: folds}
	// The turn context block trails a request, is request-only and is rebuilt
	// every time, so it is never part of a prefix; its tokens count as fresh
	// below, where they fall past the shared run anyway.
	bodies := make([][]llm.Message, len(reqs))
	for i, r := range reqs {
		bodies[i] = withoutTurnContext(r)
	}
	shareWith := func(req, prev []llm.Message) int {
		n := 0
		for n < len(req) && n < len(prev) && sameSentMessage(req[n], prev[n]) {
			n++
		}
		return n
	}
	for i, req := range reqs {
		shared := 0
		for _, prev := range bodies[:i] {
			if n := shareWith(bodies[i], prev); n > shared {
				shared = n
			}
		}
		// A request that changed a message the one before it carried, rather
		// than only adding after it, threw that cached prefix away. A shorter
		// request is not one of them: a fold is the turn's prefix cut at the
		// boundary, and the turn after a compaction starts from the summary row.
		if i > 0 && len(bodies[i]) >= len(bodies[i-1]) && shareWith(bodies[i], bodies[i-1]) < len(bodies[i-1]) {
			out.rewrites++
		}
		for j, m := range req {
			n := session.EstimateContextTokens(m.Content)
			out.raw += n
			if j < shared {
				out.cached += n
			} else {
				out.fresh += n
			}
		}
	}
	return out
}

// sameSentMessage is whether two messages of two requests are the same bytes to
// a provider: the role, the text, the call they answer and the calls they make.
func sameSentMessage(a, b llm.Message) bool {
	if a.Role != b.Role || a.Content != b.Content || a.ToolCallID != b.ToolCallID || len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	for i := range a.ToolCalls {
		if a.ToolCalls[i] != b.ToolCalls[i] {
			return false
		}
	}
	return true
}
