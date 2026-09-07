package agent

// Live integration probe against a real OpenAI-compatible endpoint (neuraldeep).
// Skipped unless CODDY_LIVE_MODEL and NEURALDEEP_API_KEY are set, so the normal
// suite stays deterministic and offline.
//
//	CODDY_LIVE_MODEL=qwen3.6-35b-a3b-noreason NEURALDEEP_API_KEY=... \
//	  go test ./internal/agent -run TestLiveNeuralDeep -count=1 -v -timeout 900s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type liveRecord struct {
	messages []llm.Message
	err      error
}

type liveRecordingProvider struct {
	inner   llm.Provider
	records *[]liveRecord
}

func (p *liveRecordingProvider) Complete(ctx context.Context, m []llm.Message, t []llm.ToolDefinition) (*llm.Response, error) {
	resp, err := p.inner.Complete(ctx, m, t)
	*p.records = append(*p.records, liveRecord{messages: append([]llm.Message(nil), m...), err: err})
	return resp, err
}

func (p *liveRecordingProvider) Stream(ctx context.Context, m []llm.Message, t []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	resp, err := p.inner.Stream(ctx, m, t, onChunk)
	*p.records = append(*p.records, liveRecord{messages: append([]llm.Message(nil), m...), err: err})
	return resp, err
}

func liveMessagesTokens(msgs []llm.Message) int {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		for _, tc := range m.ToolCalls {
			b.WriteString(tc.InputJSON)
		}
		b.WriteByte('\n')
	}
	return session.EstimateTokens(b.String())
}

// liveBigFile writes a synthetic source file with a distinctive marker deep inside.
func liveBigFile(t *testing.T, dir string, lines, markerLine int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		switch i {
		case markerLine:
			b.WriteString("func secretHandler(payload string) error { // SENTINEL_MARKER_7788\n")
		case markerLine + 1:
			b.WriteString("\treturn validateChecksum(payload) // SENTINEL_DETAIL: checksum then dispatch\n")
		default:
			fmt.Fprintf(&b, "func helper%04d(v int) int { return v * %d } // filler line %d\n", i, i%7+1, i)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

type liveProbeOpts struct {
	evictionOn bool
	keepRecent int // 0 means "use the shipped default"
	readLimit  int
	grepLimit  int
	prompt     string
}

type liveProbeResult struct {
	records []liveRecord
	st      *session.State
	stop    string
}

func liveSkipUnlessConfigured(t *testing.T) (model, key string) {
	t.Helper()
	model = strings.TrimSpace(os.Getenv("CODDY_LIVE_MODEL"))
	key = strings.TrimSpace(os.Getenv("NEURALDEEP_API_KEY"))
	if model == "" || key == "" {
		t.Skip("set CODDY_LIVE_MODEL and NEURALDEEP_API_KEY to run the live probe")
	}
	return model, key
}

func runLiveProbe(t *testing.T, o liveProbeOpts) liveProbeResult {
	t.Helper()
	model, key := liveSkipUnlessConfigured(t)

	cwd := t.TempDir()
	store := t.TempDir()
	sessionDir := filepath.Join(store, "bundle")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	liveBigFile(t, cwd, 3000, 2410)

	keepRecent := o.keepRecent
	if keepRecent == 0 {
		keepRecent = config.ResultEvictionDefaultKeepRecent
	}
	minBytes := 500
	enabled := o.evictionOn
	readLimit, grepLimit := o.readLimit, o.grepLimit
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "neuraldeep", Type: "neuraldeep", APIKey: key}},
		Models: []config.ModelEntry{{
			Model: "neuraldeep/" + model, MaxTokens: 2048, MaxContextTokens: 128000, Temperature: 0.2,
		}},
		Agent: config.Agent{Model: "neuraldeep/" + model, MaxTurns: 14},
		Tools: config.Tools{
			PermissionMode: config.PermModeBypass,
			OutputLimits:   config.ToolOutputLimits{Read: &readLimit, Grep: &grepLimit},
		},
		Compaction: config.Compaction{ResultEviction: config.ResultEviction{
			Enabled: &enabled, KeepRecent: &keepRecent, MinResultBytes: &minBytes,
		}},
	}

	st := &session.State{ID: "sess_live", CWD: cwd, Mode: session.ModeAgent, SessionDir: sessionDir}
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)

	var records []liveRecord
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		inner, err := llm.NewProvider(in)
		if err != nil {
			return nil, err
		}
		return &liveRecordingProvider{inner: inner, records: &records}, nil
	}

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: o.prompt}})
	if err != nil {
		t.Fatalf("live run failed: %v (stop=%s)", err, stop)
	}
	for i, r := range records {
		if r.err != nil {
			t.Errorf("LLM call %d returned an error: %v", i+1, r.err)
		}
	}
	return liveProbeResult{records: records, st: st, stop: stop}
}

func liveToolCallCounts(st *session.State) map[string]int {
	out := map[string]int{}
	for _, m := range st.GetMessages() {
		for _, tc := range m.ToolCalls {
			out[tc.Name]++
		}
	}
	return out
}

func liveLogCalls(t *testing.T, res liveProbeResult) (peakTokens int) {
	t.Helper()
	for i, r := range res.records {
		placeholders, truncations := 0, 0
		for _, m := range r.messages {
			if strings.HasPrefix(m.Content, "[evicted:") {
				placeholders++
			}
			if strings.Contains(m.Content, "[output truncated:") {
				truncations++
			}
		}
		tok := liveMessagesTokens(r.messages)
		if tok > peakTokens {
			peakTokens = tok
		}
		t.Logf("  call %d: %d msgs, ~%d tokens, %d evicted, %d truncated", i+1, len(r.messages), tok, placeholders, truncations)
	}
	return peakTokens
}

const livePagingPrompt = "The file big.go is large. Page through it with read using offset and limit " +
	"(400 lines at a time) until you find the function named secretHandler. " +
	"Mark the page that contains it as useful, then tell me the sentinel marker comment on that line."

// TestLiveNeuralDeepContextProtection drives a real model through a paging
// session and verifies the pruned projection is accepted by the endpoint, the
// context stays flat, and the persisted transcript stays complete.
func TestLiveNeuralDeepContextProtection(t *testing.T) {
	res := runLiveProbe(t, liveProbeOpts{evictionOn: true, readLimit: 400, grepLimit: 40, prompt: livePagingPrompt})

	t.Logf("stop reason: %s", res.stop)
	t.Logf("tool calls: %v", liveToolCallCounts(res.st))
	peak := liveLogCalls(t, res)
	t.Logf("peak projection: ~%d tokens", peak)

	// The persisted transcript must never contain eviction placeholders.
	for _, m := range res.st.GetMessages() {
		if strings.HasPrefix(m.Content, "[evicted:") {
			t.Error("persisted transcript contains an eviction placeholder; it must stay complete")
			break
		}
	}

	// The final projection must be materially smaller than the raw transcript it
	// was derived from — that is the whole point of the feature.
	raw := liveMessagesTokens(session.MessagesForLLM(res.st.GetMessages()))
	final := liveMessagesTokens(res.records[len(res.records)-1].messages)
	t.Logf("raw transcript ~%d tokens vs final projection ~%d tokens (saved ~%d)", raw, final, raw-final)
	if raw <= final {
		t.Errorf("projection (%d) is not smaller than the raw transcript (%d)", final, raw)
	}

	last := res.st.GetMessages()[len(res.st.GetMessages())-1]
	t.Logf("final answer: %s", truncateForLog(last.Content, 300))
	if !strings.Contains(last.Content, "SENTINEL_MARKER_7788") {
		t.Errorf("model did not report the sentinel marker; answer=%q", truncateForLog(last.Content, 300))
	}
}

// TestLiveNeuralDeepEvictionOffBaseline runs the same paging session with
// eviction disabled, for an A/B comparison of context growth.
func TestLiveNeuralDeepEvictionOffBaseline(t *testing.T) {
	res := runLiveProbe(t, liveProbeOpts{evictionOn: false, readLimit: 400, grepLimit: 40, prompt: livePagingPrompt})

	t.Logf("stop reason: %s", res.stop)
	t.Logf("tool calls: %v", liveToolCallCounts(res.st))
	peak := liveLogCalls(t, res)
	t.Logf("peak projection WITHOUT eviction: ~%d tokens", peak)
}

// TestLiveNeuralDeepOutputLimits forces both tool output ceilings to fire and
// checks the model still completes the task from the truncated view.
func TestLiveNeuralDeepOutputLimits(t *testing.T) {
	const prompt = "First call read on big.go with no offset and no limit. " +
		"Then call grep for the pattern helper0 across the working directory. " +
		"Then tell me, in one sentence each, whether each result was complete or truncated."

	res := runLiveProbe(t, liveProbeOpts{evictionOn: true, readLimit: 120, grepLimit: 15, prompt: prompt})

	t.Logf("stop reason: %s", res.stop)
	t.Logf("tool calls: %v", liveToolCallCounts(res.st))
	liveLogCalls(t, res)

	// Truncation happens at source, so the persisted results carry the marker.
	var readTrunc, grepTrunc int
	for _, m := range res.st.GetMessages() {
		if m.Role != llm.RoleTool || !strings.Contains(m.Content, "[output truncated:") {
			continue
		}
		body := strings.SplitN(m.Content, "\n[output truncated:", 2)[0]
		lines := strings.Count(body, "\n") + 1
		switch {
		case strings.Contains(m.Content, "output_limits.read"):
			readTrunc++
			if lines > 120 {
				t.Errorf("read result kept %d lines, want <= 120", lines)
			}
		case strings.Contains(m.Content, "output_limits.grep"):
			grepTrunc++
			if lines > 15 {
				t.Errorf("grep result kept %d lines, want <= 15", lines)
			}
		}
		t.Logf("truncated result: %d lines kept, marker=%q", lines,
			truncateForLog(m.Content[strings.Index(m.Content, "[output truncated:"):], 160))
	}
	if readTrunc == 0 {
		t.Error("expected the whole-file read to be truncated by tools.output_limits.read")
	}
	if grepTrunc == 0 {
		t.Error("expected the wide grep to be truncated by tools.output_limits.grep")
	}

	last := res.st.GetMessages()[len(res.st.GetMessages())-1]
	t.Logf("final answer: %s", truncateForLog(last.Content, 400))
}

func truncateForLog(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
