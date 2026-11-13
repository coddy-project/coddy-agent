package agent

// Prompt-cache checks of what rewrites the replayed history inside one long turn
// (issue #490). A provider caches a request by its prefix, so a placeholder or a
// summary written into the middle of the history costs it its cached copy of
// everything behind it. Below the gates every request repeats the previous one
// and adds to it; result eviction, once the conversation reaches
// compaction.result_eviction.start_percent of the window, only turns listing
// results into placeholders; and a fold, at the compaction threshold, is the
// only other place the history is rewritten. Every test here is named
// TestPromptCache*, so make test-cache runs them.

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

// --- result eviction ---------------------------------------------------------------

// One user prompt, a model that fans out parallel glob and print_tree calls step
// after step, and a context window sized so the gate is crossed partway through.
// Below it every request has to repeat the previous one and add to it; above it
// the only bytes that move are listing results turned into placeholders.

const (
	// pcEvictFolders is how many fan-out steps the turn makes, one folder each:
	// the step window plus a whole batch, so the batch falls due inside the run,
	// and two more so requests follow the collapse.
	pcEvictFolders = pcEvictKeep + resultEvictionBatch + 3
	pcEvictFiles   = 30 // files per folder, so a listing is a few KB
	// pcEvictKeep is the step window of the listings, fixed in the agent.
	pcEvictKeep = listingKeepRecentSteps
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
				Enabled: &enabled, MinResultBytes: &minBytes,
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
// listing gate is still due for msgs: due means the conversation is at least
// start_percent of the window, so the answer is the most window the
// conversation still fills that far.
func largestWindowForGate(ag *Agent, msgs []llm.Message) int {
	due := func(window int) bool {
		ag.cfg.Models[0].MaxContextTokens = window
		return ag.listingEvictionDue(msgs)
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
	// The first request that finds the gate open: the one after the first that
	// carries a step outside the window, so a gate that opened early shows up.
	const gateAt = pcEvictKeep + 2
	// Steps leave the window a batch at a time, so the first request that
	// collapses one is the first past the gate whose candidates fill a batch.
	collapseAt := gateAt
	for evictionWindowStart(collapseAt, pcEvictKeep, resultEvictionBatch) == 0 {
		collapseAt++
	}
	if collapseAt >= pcEvictFolders {
		t.Fatalf("the run of %d steps never fills a batch past the gate at %d", pcEvictFolders, gateAt)
	}
	below := largestWindowForGate(plain.ag, withoutTurnContext(plain.reqs[gateAt-1]))
	above := largestWindowForGate(plain.ag, withoutTurnContext(plain.reqs[gateAt]))
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
	// The window was sized so that request gateAt is the first to find the gate
	// open, and the batch decides which request past it actually collapses
	// anything. A gate that opened early, or a batch that let a step go on its
	// own, both show up here.
	if gate != collapseAt {
		t.Fatalf("the first collapse is at request %d; the gate was sized for request %d and the batch of %d makes it %d",
			gate, gateAt, resultEvictionBatch, collapseAt)
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
	listing := listingTools

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
	// What is still whole at the end is everything from the batch boundary on:
	// the window of pcEvictKeep steps and whatever has not yet filled a batch.
	collapsed := evictionWindowStart(pcEvictFolders, pcEvictKeep, resultEvictionBatch)
	if collapsed == 0 || pcEvictFolders-collapsed < pcEvictKeep {
		t.Fatalf("the run collapsed %d of %d steps: the fixture tests neither the window nor the batch", collapsed, pcEvictFolders)
	}
	for _, m := range final {
		if m.Role != llm.RoleTool {
			continue
		}
		kept := stepOfCall[m.ToolCallID] >= collapsed
		if kept == isPlaceholder(m) {
			t.Fatalf("result %s of step %d: collapsed=%v, want %v", m.ToolCallID, stepOfCall[m.ToolCallID], isPlaceholder(m), !kept)
		}
	}
}

// --- the fold inside the turn ---------------------------------------------------

// One prompt, a model that reads files in parallel step after step, and a window
// the turn outgrows several times. Between folds every request has to repeat the
// previous one and add to it; a fold is the only place the history is rewritten,
// and what it leaves is the prompt at the start of the summary row followed by
// the kept steps exactly as they were.
func TestPromptCacheInTurnFoldRewritesTheHistoryOnlyAtTheFold(t *testing.T) {
	// The window is sized so that a fold keeps two steps (the aim leaves room for
	// them beside the request's fixed overhead) and the trigger fires every few
	// steps: the run folds several times, which is what makes the check mean
	// something. The fixed overhead includes the definitions of every built-in
	// tool, so a new tool can leave room for the latest step only: grow the
	// window then, keeping at least two folds and multiple appends.
	dirs := []string{}
	tempDir := func() (string, error) {
		d, err := os.MkdirTemp("", "coddy-pcache-inturn-*")
		if err == nil {
			dirs = append(dirs, d)
		}
		return d, err
	}
	t.Cleanup(func() {
		for _, d := range dirs {
			_ = os.RemoveAll(d)
		}
	})
	// Include the larger Windows shell/tool instructions in the fixed overhead
	// and run enough steps to cross the larger window more than once.
	const steps = 20
	w, err := inTurnLongTurn{window: 72000, steps: steps, fanOut: 4, evictOff: true}.run(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if w.runErr != nil {
		t.Fatalf("the turn failed: %v", w.runErr)
	}
	reqs := w.provider.accepted
	hist := make([][]llm.Message, len(reqs))
	for i, r := range reqs {
		hist[i] = withoutTurnContext(r)
	}
	if got := len(hist); got != steps+1 {
		t.Fatalf("the run made %d requests, want %d: a request per step and the answer", got, steps+1)
	}

	// The system message is the same bytes in every request, folds included.
	for i, h := range hist {
		if h[0].Role != llm.RoleSystem || h[0].Content != hist[0][0].Content {
			t.Fatalf("request %d does not open with the system message of request 0", i)
		}
	}

	// Transcript content of every message by tool call id, for what the kept
	// steps must still read as.
	stored := map[string]llm.Message{}
	for _, m := range w.st.GetMessages() {
		if m.Role == llm.RoleTool {
			stored[m.ToolCallID] = m
		}
	}

	folds, appends, keptAsSent, chainKept := 0, 0, 0, 0
	for i := 1; i < len(hist); i++ {
		prev, cur := hist[i-1], hist[i]
		if len(cur) > len(prev) && reflect.DeepEqual(prev, cur[:len(prev)]) {
			// Between folds: the previous request, repeated, with the step that
			// came after it added.
			appends++
			continue
		}
		folds++
		// A fold is the one place the history changes other than at its end. It
		// is a smaller request that opens, after the system message, with the
		// chain of summary rows; the first begins with the prompt, verbatim, and
		// the later ones do not repeat it.
		if estimateMessagesTokens(cur) >= estimateMessagesTokens(prev) {
			t.Fatalf("request %d rewrote the history without shrinking it: %d tokens after %d", i, estimateMessagesTokens(cur), estimateMessagesTokens(prev))
		}
		rows := 0
		for rows+1 < len(cur) && cur[rows+1].CompactionSummary {
			rows++
		}
		if rows == 0 || !strings.HasPrefix(cur[1].Content, inTurnPrompt+"\n\n") {
			t.Fatalf("request %d: the message after the system message is not the summary row that begins with the prompt: %q", i, firstChars(cur[1].Content, 100))
		}
		if got := strings.Count(transcriptText(cur), inTurnPrompt); got != 1 {
			t.Fatalf("request %d holds the prompt %d times, want once", i, got)
		}
		// The fold only adds a row after the ones already written (issue #361):
		// the system message and the rows before the new one are the previous
		// request's opening messages, byte for byte, so the provider's cache
		// holds up to the end of the last of them.
		if !reflect.DeepEqual(cur[:rows], prev[:rows]) {
			t.Fatalf("request %d rewrote the summary rows before the new one:\nafter:  %s\nbefore: %s", i, firstChars(transcriptText(cur[:rows]), 300), firstChars(transcriptText(prev[:rows]), 300))
		}
		if rows > 1 {
			chainKept++
		}
		// What follows the rows is the latest steps of the turn, byte for byte
		// what the previous request carried (or, for the step that came after it,
		// what the transcript stored), and whole steps: it opens on an assistant
		// message.
		kept := cur[1+rows:]
		if len(kept) == 0 || kept[0].Role != llm.RoleAssistant {
			t.Fatalf("request %d: the kept steps do not open on an assistant message: %+v", i, kept)
		}
		before := map[string]llm.Message{}
		for _, m := range prev {
			if m.Role == llm.RoleTool {
				before[m.ToolCallID] = m
			}
		}
		for j, m := range kept {
			if m.Role != llm.RoleTool {
				continue
			}
			want, ok := before[m.ToolCallID]
			if ok {
				keptAsSent++
			} else {
				want = stored[m.ToolCallID]
			}
			if !reflect.DeepEqual(m, want) {
				t.Fatalf("request %d: kept message %d (%s) is not the one it was before the fold:\nafter:  %+v\nbefore: %+v", i, j, m.ToolCallID, m, want)
			}
		}
		if issues := session.ValidateToolPairing(cur); len(issues) != 0 {
			t.Fatalf("request %d breaks the tool-call pairing: %v", i, issues)
		}
	}

	// Both phases have to be exercised, or the test passes without testing
	// anything: the run folds more than once and spends most of its requests
	// appending.
	if folds < 2 || appends < 8 {
		t.Fatalf("the run folded %d time(s) and appended %d time(s): too little of either to mean anything", folds, appends)
	}
	// And a fold has to keep a step that an earlier request had already sent, or
	// "kept exactly as it was" compares nothing but the newest step to the
	// transcript.
	if keptAsSent == 0 {
		t.Fatal("no fold kept a step the previous request carried: the window leaves room for the latest step only")
	}
	// And a fold after the first has to find the rows already written and keep
	// them, or the check of the prefix compares nothing but the system message.
	if chainKept == 0 {
		t.Fatal("no fold kept a summary row an earlier fold wrote")
	}
	t.Logf("%d requests: %d folds, %d appends", len(hist), folds, appends)

	// Each fold left a row in the transcript; none was lost.
	rows := 0
	for _, m := range w.st.GetMessages() {
		if m.CompactionSummary {
			rows++
		}
	}
	if rows != folds {
		t.Fatalf("the transcript holds %d summary rows for %d folds", rows, folds)
	}
}

// --- the summarizer's own request ------------------------------------------

// A compaction asks the summarizer with the conversation as the turn sent it,
// so the provider reads the head back from the prompt cache it just wrote
// (compact_cached.go). Everything here is what makes that request, and what
// sends the fold back to the flattened transcript instead.

const pcTurnSystem = "SYSTEM MESSAGE OF THE TURN"

// pcRecordTurnSend seeds the record a request leaves behind, which is what a
// compaction repeats. A real turn writes it at the send boundary (react.go).
func pcRecordTurnSend(st *session.State, model string) {
	st.SetTurnSend(session.TurnSend{
		Model:  model,
		System: llm.Message{Role: llm.RoleSystem, Content: pcTurnSystem},
		Tools:  []llm.ToolDefinition{{Name: "read", Description: "read a file"}},
		Input:  llm.ProviderInput{Name: "fake", Type: "openai", Model: model},
	})
}

// pcCompactOnce folds a seeded session of exchanges turns, keeping one, and
// returns the requests the summarizer was sent.
func pcCompactOnce(t *testing.T, exchanges int, record bool, tune func(*Agent)) [][]llm.Message {
	t.Helper()
	st := seededCompactState(t, exchanges)
	if record {
		pcRecordTurnSend(st, "fake/model")
	}
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	if tune != nil {
		tune(ag)
	}
	if _, err := ag.CompactSession(context.Background(), CompactOptions{}); err != nil {
		t.Fatalf("the compaction failed: %v", err)
	}
	return provider.requests
}

func TestPromptCacheCompactionRepeatsTheRecordedRequest(t *testing.T) {
	reqs := pcCompactOnce(t, 4, true, nil)
	if len(reqs) != 1 {
		t.Fatalf("the summarizer was called %d times, want once", len(reqs))
	}
	req := reqs[0]
	if req[0].Role != llm.RoleSystem || req[0].Content != pcTurnSystem {
		t.Fatalf("the request does not open with the turn's system message: %+v", req[0])
	}
	last := req[len(req)-1]
	if !strings.Contains(last.Content, cachedCompactionMarker) || !last.Ephemeral {
		t.Fatalf("the request does not end with the request-only instruction: %+v", last)
	}
	// Between them, the folded conversation as the transcript holds it.
	body := req[1 : len(req)-1]
	if len(body) != 6 {
		t.Fatalf("the request carries %d message(s) of conversation, want the three folded exchanges", len(body))
	}
	for i, m := range body {
		want := "question"
		if i%2 == 1 {
			want = "answer"
		}
		if !strings.HasPrefix(m.Content, want) {
			t.Fatalf("message %d of the conversation is %q, want one beginning %q", i, m.Content, want)
		}
	}
	// The kept turn is not in it: the summary replaces what came before it.
	if strings.Contains(transcriptText(body), "question 4") {
		t.Fatalf("the request carries the turn the compaction keeps verbatim")
	}
}

func TestPromptCacheCompactionWithoutARecordedRequestFlattensTheTranscript(t *testing.T) {
	reqs := pcCompactOnce(t, 4, false, nil)
	if len(reqs) != 1 {
		t.Fatalf("the summarizer was called %d times, want once", len(reqs))
	}
	req := reqs[0]
	if len(req) != 2 {
		t.Fatalf("the request holds %d message(s), want the summarizer prompt and the transcript", len(req))
	}
	if !strings.Contains(req[0].Content, "compacting the conversation history") {
		t.Fatalf("the request does not open with the summarizer's own prompt: %q", firstChars(req[0].Content, 80))
	}
	if !strings.Contains(req[1].Content, "<transcript>") {
		t.Fatalf("the request carries no flattened transcript: %q", firstChars(req[1].Content, 80))
	}
}

// A summarizer that answers the conversation with a tool call wrote no summary:
// the fold falls back to the flattened transcript, which asks a model that is
// given no tools at all, and the compaction finishes.
func TestPromptCacheCompactionFallsBackWhenTheSummarizerCallsATool(t *testing.T) {
	st := seededCompactState(t, 4)
	pcRecordTurnSend(st, "fake/model")
	keep := 1
	provider := &pcToolCallThenSummary{summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	res, err := ag.CompactSession(context.Background(), CompactOptions{})
	if err != nil {
		t.Fatalf("the compaction failed: %v", err)
	}
	if res.Summary != "SUMMARY" {
		t.Fatalf("the compaction kept %q as the summary", res.Summary)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("the summarizer was called %d times, want the cached request and the flattened one", len(provider.requests))
	}
	if len(provider.requests[1]) != 2 || !strings.Contains(provider.requests[1][1].Content, "<transcript>") {
		t.Fatalf("the second request is not the flattened transcript: %d message(s)", len(provider.requests[1]))
	}
}

// pcToolCallThenSummary answers the first request with a tool call and every
// one after it with a summary.
type pcToolCallThenSummary struct {
	summary  string
	requests [][]llm.Message
}

func (p *pcToolCallThenSummary) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.requests = append(p.requests, append([]llm.Message(nil), messages...))
	if len(p.requests) == 1 {
		return &llm.Response{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", InputJSON: `{"path":"main.go"}`}}, StopReason: "tool_use"}, nil
	}
	return &llm.Response{Content: p.summary, StopReason: "end_turn"}, nil
}

func (p *pcToolCallThenSummary) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	return nil, fmt.Errorf("a compaction never streams")
}

// A window that no longer holds the conversation sends the fold to the passes
// of compact_fold.go, which is the one thing that can still shrink it.
func TestPromptCacheCompactionFallsBackWhenTheRequestNoLongerFits(t *testing.T) {
	reqs := pcCompactOnce(t, 4, true, func(ag *Agent) {
		ag.cfg.Models[0].MaxContextTokens = 200
	})
	if len(reqs) == 0 {
		t.Fatal("the summarizer was not called")
	}
	if got := reqs[0]; len(got) != 2 || !strings.Contains(got[1].Content, "<transcript>") {
		t.Fatalf("the fold did not flatten the transcript: %d message(s)", len(got))
	}
}

// The merge of the chain is the same cached prefix: the rows it retells are the
// first messages of the window, where the model already read them.
func TestPromptCacheMergeRepeatsTheCachedPrefix(t *testing.T) {
	var parts [][]llm.Message
	for i := 1; i <= 5; i++ {
		parts = append(parts,
			[]llm.Message{userText(fmt.Sprintf("question %d", i)), {Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)}},
			[]llm.Message{chainBlock(fmt.Sprintf("B%d", i), 2000)})
	}
	parts = append(parts,
		[]llm.Message{userText("question 6"), {Role: llm.RoleAssistant, Content: "answer 6"}},
		[]llm.Message{userText("question 7"), {Role: llm.RoleAssistant, Content: "answer 7"}})
	st := stateOf(t, parts...)
	pcRecordTurnSend(st, "fake/model")
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	ag.cfg.Models[0].MaxContextTokens = 30000

	res, err := ag.CompactSession(context.Background(), CompactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.MergedSummaries != 4 {
		t.Fatalf("merged %d rows, want 4", res.MergedSummaries)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("the summarizer was called %d times, want the fold and the merge", len(provider.requests))
	}
	merge := provider.requests[1]
	if merge[0].Content != pcTurnSystem {
		t.Fatalf("the merge does not open with the turn's system message: %q", firstChars(merge[0].Content, 80))
	}
	if !strings.Contains(merge[len(merge)-1].Content, cachedCompactionMarker) {
		t.Fatalf("the merge does not end with the summarization instruction")
	}
	rows := merge[1 : len(merge)-1]
	if len(rows) != 4 {
		t.Fatalf("the merge carries %d message(s) between them, want the four rows it retells", len(rows))
	}
	for i, m := range rows {
		if !m.CompactionSummary {
			t.Fatalf("message %d of the merge is no summary row: %+v", i, m)
		}
		if !strings.Contains(m.Content, fmt.Sprintf("B%d ", i+1)) {
			t.Fatalf("message %d of the merge is not B%d: %q", i, i+1, firstChars(m.Content, 60))
		}
	}
}

// The working window holds at least keep_recent candidates and lets them go a
// whole batch at a time, so the set of collapsed results moves once every
// resultEvictionBatch rather than at every step.
func TestPromptCacheEvictionWindowLeavesInBatches(t *testing.T) {
	const keep = 2
	starts := make([]int, 0, 16)
	for n := 0; n <= 14; n++ {
		starts = append(starts, evictionWindowStart(n, keep, resultEvictionBatch))
	}
	want := []int{0, 0, 0, 0, 0, 0, 4, 4, 4, 4, 8, 8, 8, 8, 12}
	if !reflect.DeepEqual(starts, want) {
		t.Fatalf("the window started at %v, want %v", starts, want)
	}
	for n := 0; n <= 14; n++ {
		if kept := n - starts[n]; kept < keep && n >= keep {
			t.Fatalf("%d candidates leave %d in the window, fewer than keep_recent %d", n, kept, keep)
		}
		if n >= keep && n-starts[n] > keep+resultEvictionBatch-1 {
			t.Fatalf("%d candidates leave %d in the window, more than a batch over keep_recent %d", n, n-starts[n], keep)
		}
	}
	// A batch below two is the window taken literally, which is what a test of
	// the window alone asks for.
	for n := 0; n <= 6; n++ {
		if got, want := evictionWindowStart(n, keep, 1), max(n-keep, 0); got != want {
			t.Fatalf("with no batch, %d candidates start the window at %d, want %d", n, got, want)
		}
	}
}
