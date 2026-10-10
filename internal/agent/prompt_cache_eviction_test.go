package agent

// Prompt-cache check of result eviction inside one long turn (issue #490): a
// placeholder written into the middle of the replayed history costs the provider
// its cached copy of everything behind it, so eviction waits for
// compaction.result_eviction.start_percent. One user prompt, a model that fans
// out parallel glob and print_tree calls step after step, and a context window
// sized so the gate is crossed halfway through. Below it every request has to
// repeat the previous one and add to it; above it the only bytes that move are
// listing results turned into placeholders.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	pcEvictFolders = 7  // fan-out steps of the turn, one folder each
	pcEvictFiles   = 30 // files per folder, so a listing is a few KB
	pcEvictKeep    = 2  // keep_recent_steps of the run
)

// pcEvictWorkspace creates the folders the model will list.
func pcEvictWorkspace(t *testing.T) string {
	t.Helper()
	cwd := t.TempDir()
	for f := 0; f < pcEvictFolders; f++ {
		dir := filepath.Join(cwd, fmt.Sprintf("dir%d", f))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= pcEvictFiles; i++ {
			name := filepath.Join(dir, fmt.Sprintf("dir%d_source_file_%02d.go", f, i))
			if err := os.WriteFile(name, []byte("package x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return cwd
}

// pcEvictTurn is one run of the long turn: its agent and the requests the
// provider received.
type pcEvictTurn struct {
	ag   *Agent
	reqs [][]llm.Message
}

// runPCacheListingTurn answers one prompt with pcEvictFolders fan-out steps -
// a glob, a tree and a recursive glob of one folder each, in parallel - and then
// a final answer, on a model whose context window is maxContext tokens.
func runPCacheListingTurn(t *testing.T, cwd string, maxContext int) pcEvictTurn {
	t.Helper()
	prov := &pcScriptProvider{}
	for f := 0; f < pcEvictFolders; f++ {
		dir := fmt.Sprintf("dir%d", f)
		prov.steps = append(prov.steps, pcStep{calls: []llm.ToolCall{
			tcGlobIn(fmt.Sprintf("s%d_glob", f), "*.go", dir),
			tcTreeOf(fmt.Sprintf("s%d_tree", f), dir),
			tcGlobIn(fmt.Sprintf("s%d_all", f), "**/*", dir),
		}})
	}
	prov.steps = append(prov.steps, pcStep{text: "answer"})

	off := false
	enabled := true
	minBytes := 200
	keepSteps := pcEvictKeep
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: maxContext}},
		Agent:     config.Agent{Model: "fake/model"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
		// Automatic compaction has no earlier turn to fold here and would only
		// add noise; the gate under test is the eviction one, at its default 50%.
		Compaction: config.Compaction{
			AutoEnabled: &off,
			ResultEviction: config.ResultEviction{
				Enabled: &enabled, MinResultBytes: &minBytes, KeepRecentSteps: &keepSteps,
			},
		},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()

	store := t.TempDir()
	sessionDir := filepath.Join(store, "bundle")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "sess_pcache_evict", CWD: cwd, Mode: session.ModeAgent, SessionDir: sessionDir}
	st.ReplaceRulesCatalog(session.DiscoverRules(cfg, cwd))
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return prov, nil }

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "resume"}}); err != nil {
		t.Fatalf("the turn failed: %v", err)
	}
	return pcEvictTurn{ag: ag, reqs: prov.seen}
}

// withoutTurnContext is the history of a request: everything but the turn
// context block that trails it and is rebuilt for every call.
func withoutTurnContext(req []llm.Message) []llm.Message {
	if n := len(req); n > 0 && strings.Contains(req[n-1].Content, turnContextOpenTag) {
		return req[:n-1]
	}
	return req
}

// largestWindowForGate is the largest context window, in tokens, at which the
// eviction gate is still due for msgs: due means the conversation is at least
// start_percent of the window, so the answer is the most window the
// conversation still fills that far.
func largestWindowForGate(ag *Agent, msgs []llm.Message) int {
	due := func(window int) bool {
		ag.cfg.Models[0].MaxContextTokens = window
		return ag.evictionDue(msgs)
	}
	lo, hi := 1, 1<<30
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if due(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

func isPlaceholder(m llm.Message) bool {
	return m.Role == llm.RoleTool && strings.HasPrefix(m.Content, "[evicted:")
}

func TestPromptCacheInTurnEvictionKeepsThePrefixBelowTheGate(t *testing.T) {
	cwd := pcEvictWorkspace(t)

	// Calibration: the same turn on a window nothing fills, so no gate opens and
	// every request is the plain history. What each one weighs, to the gate's own
	// arithmetic, sizes the window of the real run.
	plain := runPCacheListingTurn(t, cwd, 1<<30)
	if got := len(plain.reqs); got != pcEvictFolders+1 {
		t.Fatalf("calibration made %d requests, want %d", got, pcEvictFolders+1)
	}
	for i, req := range plain.reqs {
		for _, m := range req {
			if isPlaceholder(m) {
				t.Fatalf("calibration request %d already carries a placeholder: %q", i, m.Content)
			}
		}
	}
	const crossAt = 4 // the first request that finds the gate open
	below := largestWindowForGate(plain.ag, withoutTurnContext(plain.reqs[crossAt-1]))
	above := largestWindowForGate(plain.ag, withoutTurnContext(plain.reqs[crossAt]))
	if above-below < 1000 {
		t.Fatalf("one step moves the gate by only %d tokens of window; the run cannot place it reliably", above-below)
	}
	window := below + (above-below+1)/2

	run := runPCacheListingTurn(t, cwd, window)
	reqs := run.reqs
	if len(reqs) != pcEvictFolders+1 {
		t.Fatalf("the run made %d requests, want %d", len(reqs), pcEvictFolders+1)
	}
	hist := make([][]llm.Message, len(reqs))
	for i, r := range reqs {
		hist[i] = withoutTurnContext(r)
	}

	// The gate has to open somewhere in the run, and not at its start: both
	// phases are exercised, or this test would pass without testing anything.
	gate := -1
	for i, h := range hist {
		for _, m := range h {
			if isPlaceholder(m) {
				gate = i
				break
			}
		}
		if gate >= 0 {
			break
		}
	}
	if gate < 0 {
		t.Fatal("no request carried a placeholder: the gate never opened, nothing was tested")
	}
	// The window was sized so that request crossAt is the first to find the gate
	// open. Three steps already sit outside a window of two at request 3, so a
	// gate that opened early would show up as a collapse there.
	if gate != crossAt {
		t.Fatalf("the gate opened at request %d, the window was sized to open it at request %d", gate, crossAt)
	}
	t.Logf("window of %d tokens: the gate opens at request %d of %d", window, gate, len(hist))

	// The system message and the prompt are the same bytes in every request.
	for i, h := range hist {
		if h[0].Role != llm.RoleSystem || h[0].Content != hist[0][0].Content {
			t.Fatalf("request %d does not open with the system message of request 0", i)
		}
		if h[1].Role != llm.RoleUser || !strings.Contains(h[1].Content, "resume") {
			t.Fatalf("request %d lost the prompt being answered: %+v", i, h[1])
		}
		if issues := session.ValidateToolPairing(h); len(issues) != 0 {
			t.Fatalf("request %d breaks the tool-call pairing: %v", i, issues)
		}
	}

	// What each tool call is, for telling a listing result from anything else.
	toolOf := map[string]string{}
	for _, m := range hist[len(hist)-1] {
		for _, tc := range m.ToolCalls {
			toolOf[tc.ID] = tc.Name
		}
	}
	listing := map[string]bool{}
	for _, name := range config.ResultEvictionListingTools {
		listing[name] = true
	}

	for i := 1; i < len(hist); i++ {
		prev, cur := hist[i-1], hist[i]
		if len(cur) < len(prev) {
			t.Fatalf("request %d is shorter than request %d: a history only grows", i, i-1)
		}
		for j := range prev {
			if reflect.DeepEqual(prev[j], cur[j]) {
				continue
			}
			if i < gate {
				t.Fatalf("below the gate message %d changed between request %d and request %d:\nbefore: %q\nafter:  %q",
					j, i-1, i, truncateForError(prev[j].Content), truncateForError(cur[j].Content))
			}
			// Above it, the one allowed change is a listing result that became its
			// placeholder: same role, same call, a tool that is listed.
			ok := isPlaceholder(cur[j]) && !isPlaceholder(prev[j]) &&
				prev[j].Role == llm.RoleTool && prev[j].ToolCallID == cur[j].ToolCallID &&
				listing[toolOf[cur[j].ToolCallID]] &&
				len(prev[j].ImageParts) == 0
			if !ok {
				t.Fatalf("above the gate message %d changed between request %d and request %d in a way other than a listing result collapsing:\nbefore: %+v\nafter:  %+v",
					j, i-1, i, prev[j], cur[j])
			}
		}
	}

	// Everything before the first collapsed result of the run is byte for byte
	// what the last request below the gate sent: the cached prefix survives up
	// to that result.
	last := hist[gate-1]
	first := -1
	for j, m := range hist[gate] {
		if isPlaceholder(m) {
			first = j
			break
		}
	}
	if first < 0 || first > len(last) {
		t.Fatalf("the first collapsed result sits at %d of a history of %d", first, len(last))
	}
	if !reflect.DeepEqual(last[:first], hist[gate][:first]) {
		t.Fatal("the messages before the first collapsed result changed")
	}

	// The window keeps the latest steps whole: the last request still carries the
	// listings of the final pcEvictKeep steps verbatim and has collapsed the rest.
	final := hist[len(hist)-1]
	steps := 0
	for _, m := range final {
		if len(m.ToolCalls) > 0 {
			steps++
		}
	}
	if steps != pcEvictFolders {
		t.Fatalf("the last request carries %d steps, want %d", steps, pcEvictFolders)
	}
	stepOfCall := map[string]int{}
	n := 0
	for _, m := range final {
		if len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			stepOfCall[tc.ID] = n
		}
		n++
	}
	for _, m := range final {
		if m.Role != llm.RoleTool {
			continue
		}
		kept := stepOfCall[m.ToolCallID] >= pcEvictFolders-pcEvictKeep
		if kept == isPlaceholder(m) {
			t.Fatalf("result %s of step %d: collapsed=%v, want %v", m.ToolCallID, stepOfCall[m.ToolCallID], isPlaceholder(m), !kept)
		}
	}
}
