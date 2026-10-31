package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type latencyScriptProvider struct {
	calls       int
	seen        [][]llm.Message
	definitions [][]llm.ToolDefinition
}

func (*latencyScriptProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("unexpected Complete")
}
func (p *latencyScriptProvider) Stream(_ context.Context, messages []llm.Message, definitions []llm.ToolDefinition, emit func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	p.definitions = append(p.definitions, append([]llm.ToolDefinition(nil), definitions...))
	emit(llm.StreamChunk{}) // Metadata/usage-only callback is not meaningful output.
	if p.calls == 1 {
		emit(llm.StreamChunk{ReasoningDelta: "LATENCY_REASON_SENTINEL"})
		tc := llm.ToolCall{ID: "latency-tool", Name: "coddy_todo_plan_read", InputJSON: `{}`}
		emit(llm.StreamChunk{ToolCallNamed: &tc})
		emit(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use", InputTokens: 10}, nil
	}
	emit(llm.StreamChunk{TextDelta: "LATENCY_RESULT_SENTINEL"})
	return &llm.Response{Content: "LATENCY_RESULT_SENTINEL", StopReason: "end_turn", InputTokens: 20}, nil
}

func runLatencyScript(cwd string, level slog.Level) ([]map[string]any, error) {
	return runLatencyScriptWithProvider(cwd, level, &latencyScriptProvider{})
}

func runLatencyScriptWithProvider(cwd string, level slog.Level, provider *latencyScriptProvider) ([]map[string]any, error) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 4},
	}
	cfg.Prompts.ApplyDefaults()
	st := &session.State{ID: "sess_latency", CWD: cwd, Mode: session.ModeAgent}
	st.SetPlan([]acp.PlanEntry{{Content: "LATENCY_TOOL_RESULT_SENTINEL", Status: "pending"}})
	var buf bytes.Buffer
	ag := NewAgent(cfg, st, resumePermissionSender{}, slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})))
	ag.clock = func() time.Time { return time.Unix(100, 0) }
	ag.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return provider, nil })
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "LATENCY_PROMPT_SENTINEL"}}); err != nil {
		return nil, err
	}
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, nil
}

func checkLatencyRecords(records []map[string]any) error {
	counts := map[string]int{}
	ids := map[float64]bool{}
	for _, r := range records {
		name, _ := r["msg"].(string)
		switch name {
		case "llm request latency", "llm call finished", "llm call usage", "llm tool dispatch latency":
		default:
			continue
		}
		counts[name]++
		id, ok := r["call_id"].(float64)
		if !ok || id <= 0 || r["session"] != "sess_latency" {
			return fmt.Errorf("missing correlation on %s: %v", name, r)
		}
		if name == "llm request latency" {
			if ids[id] {
				return fmt.Errorf("duplicate call id %v", id)
			}
			ids[id] = true
			for _, key := range []string{"context_refresh_ms", "outbound_projection_ms", "pairing_ms", "model_call_ms", "first_meaningful_ms"} {
				if n, ok := r[key].(float64); !ok || n < 0 {
					return fmt.Errorf("missing numeric %s: %v", key, r)
				}
			}
			if r["http_requests"] != float64(0) {
				return fmt.Errorf("invented HTTP stages: %v", r)
			}
			if counts[name] == 1 {
				if r["first_reasoning_ms"] == nil || r["first_tool_ms"] == nil || r["first_text_ms"] != nil {
					return fmt.Errorf("incorrect first channel observations: %v", r)
				}
			}
		}
		if name == "llm tool dispatch latency" && r["completion_to_dispatch_ms"] == nil {
			return fmt.Errorf("missing dispatch duration")
		}
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), "SENTINEL") {
			return fmt.Errorf("content leaked in %s", name)
		}
	}
	for name, want := range map[string]int{"llm request latency": 2, "llm call finished": 2, "llm call usage": 2, "llm tool dispatch latency": 1} {
		if counts[name] != want {
			return fmt.Errorf("%s count = %d, want %d", name, counts[name], want)
		}
	}
	for _, r := range records {
		if id, ok := r["call_id"].(float64); ok && !ids[id] {
			return fmt.Errorf("uncorrelated call %v", id)
		}
	}
	return nil
}

func TestRequestLatencyAgentDebug(t *testing.T) {
	records, err := runLatencyScript(t.TempDir(), slog.LevelDebug)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkLatencyRecords(records); err != nil {
		t.Fatal(err)
	}
}
func TestRequestLatencyAgentDisabled(t *testing.T) {
	records, err := runLatencyScript(t.TempDir(), slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if strings.Contains(fmt.Sprint(r["msg"]), "latency") {
			t.Fatalf("diagnostic with debug disabled: %v", r)
		}
	}
}

func TestRequestLatencyMeaningfulCallbacks(t *testing.T) {
	start := time.Unix(100, 0)
	cases := []struct {
		name  string
		chunk llm.StreamChunk
		field string
	}{
		{"usage", llm.StreamChunk{InputTokens: 99}, ""},
		{"stop", llm.StreamChunk{StopReason: "end_turn"}, ""},
		{"text", llm.StreamChunk{TextDelta: " "}, "text"},
		{"reasoning", llm.StreamChunk{ReasoningDelta: "x"}, "reasoning"},
		{"named", llm.StreamChunk{ToolCallNamed: &llm.ToolCall{Name: "read"}}, "tool"},
		{"arguments", llm.StreamChunk{ToolCallDelta: &llm.ToolCall{InputJSON: "{"}}, "tool"},
		{"full", llm.StreamChunk{ToolCall: &llm.ToolCall{Name: "read"}}, "tool"},
		{"agent tool", llm.StreamChunk{AgentTool: &llm.AgentToolCall{}}, "tool"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &requestLatency{start: start}
			d.chunk(llm.StreamChunk{InputTokens: 1}, start.Add(time.Millisecond))
			at := start.Add(3 * time.Millisecond)
			d.chunk(tc.chunk, at)
			d.chunk(tc.chunk, start.Add(4*time.Millisecond))
			if tc.field == "" {
				if !d.meaningful.IsZero() {
					t.Fatal("metadata became meaningful output")
				}
				return
			}
			if d.meaningful != at {
				t.Fatalf("meaningful = %v, want %v", d.meaningful, at)
			}
			got := map[string]time.Time{"text": d.text, "reasoning": d.reasoning, "tool": d.tool}
			for field, stamp := range got {
				if field == tc.field {
					if stamp != at {
						t.Fatalf("%s = %v", field, stamp)
					}
				} else if !stamp.IsZero() {
					t.Fatalf("invented %s observation", field)
				}
			}
		})
	}
}

func TestRequestLatencyDisabledHasNoCollector(t *testing.T) {
	ag := &Agent{log: slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))}
	ctx := context.Background()
	d := ag.newRequestLatency(ctx)
	if d != nil {
		t.Fatal("allocated diagnostics with debug disabled")
	}
	if d.begin(ctx, false) != ctx || d.dispatchContext(ctx) != ctx || !d.clock().IsZero() {
		t.Fatal("disabled diagnostics changed context or read clock")
	}
}

func TestRequestLatencyUnobservedAndDispatch(t *testing.T) {
	var buf bytes.Buffer
	d := &requestLatency{log: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), session: "sess_latency", id: 1, start: time.Now()}
	ctx := context.Background()
	d.finish(ctx)
	dispatchCtx := d.dispatchContext(ctx)
	noteRequestToolDispatch(dispatchCtx)
	noteRequestToolDispatch(dispatchCtx)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d records, want summary and first dispatch only", len(lines))
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &summary); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"first_meaningful_ms", "first_reasoning_ms", "first_text_ms", "first_tool_ms"} {
		if _, ok := summary[field]; ok {
			t.Fatalf("invented observation %s", field)
		}
	}
}

func TestRequestLatencyRealHTTPProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"private-id","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"LATENCY_RESULT_SENTINEL"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
	}))
	defer srv.Close()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "LATENCY_KEY_SENTINEL", APIBase: srv.URL + "/v1", Proxy: "none"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000, Stream: new(bool)}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 2},
	}
	cfg.Prompts.ApplyDefaults()
	var buf bytes.Buffer
	st := &session.State{ID: "sess_latency", CWD: t.TempDir(), Mode: session.ModeAgent}
	ag := NewAgent(cfg, st, resumePermissionSender{}, slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "LATENCY_PROMPT_SENTINEL"}}); err != nil {
		t.Fatal(err)
	}
	var summary, httpRecord map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		switch r["msg"] {
		case "llm request latency":
			summary = r
		case "llm http request latency":
			httpRecord = r
		default:
			continue
		}
		for _, secret := range []string{"SENTINEL", srv.URL, "private-id", "Authorization"} {
			if strings.Contains(line, secret) {
				t.Fatalf("diagnostic leaked %s: %s", secret, line)
			}
		}
	}
	if summary == nil || httpRecord == nil {
		t.Fatalf("missing transport diagnostics: %s", buf.String())
	}
	if summary["http_requests"] != float64(1) || httpRecord["session"] != "sess_latency" || httpRecord["call_id"] != summary["call_id"] {
		t.Fatalf("incorrect HTTP correlation: %v / %v", summary, httpRecord)
	}
	for _, key := range []string{"start_ms", "request_bytes", "round_trip_ms", "connection_ms", "wrote_request_ms", "first_byte_ms"} {
		if n, ok := httpRecord[key].(float64); !ok || n < 0 {
			t.Fatalf("missing numeric transport stage %s: %v", key, httpRecord)
		}
	}
	if _, ok := httpRecord["sdk_prepare_ms"]; ok {
		t.Fatal("invented Codex SDK timing on OpenAI transport")
	}
	if httpRecord["status_code"] != float64(200) || httpRecord["protocol"] != "HTTP/1.1" {
		t.Fatalf("HTTP response metadata: %v", httpRecord)
	}
}

func TestRequestLatencyACPHasNoHTTPCollector(t *testing.T) {
	d := &requestLatency{}
	d.begin(context.Background(), true)
	if d.http != nil {
		t.Fatal("ACP acquired an HTTP collector")
	}
}

func TestRequestLatencyRespectsAgentComponent(t *testing.T) {
	for _, tc := range []struct {
		root, agent string
		enabled     bool
	}{
		{"debug", "info", false}, {"info", "debug", true},
	} {
		t.Run(tc.root+"_agent_"+tc.agent, func(t *testing.T) {
			log, closer, err := logger.New(config.Logger{Level: tc.root, Levels: []config.LoggerComponentLevel{{Component: "agent", Level: tc.agent}}, Outputs: []string{"file"}, File: filepath.Join(t.TempDir(), "agent.log")})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := closer.Close(); err != nil {
					t.Errorf("close test logger: %v", err)
				}
			}()
			ag := NewAgent(&config.Config{}, &session.State{ID: "sess_latency"}, resumePermissionSender{}, log)
			d := ag.newRequestLatency(context.Background())
			if (d != nil) != tc.enabled {
				t.Fatalf("collector enabled=%v, want %v", d != nil, tc.enabled)
			}
		})
	}
}

func TestPromptCacheRequestLatencyDoesNotChangeRequests(t *testing.T) {
	cwd := t.TempDir()
	debug, quiet := &latencyScriptProvider{}, &latencyScriptProvider{}
	if _, err := runLatencyScriptWithProvider(cwd, slog.LevelDebug, debug); err != nil {
		t.Fatal(err)
	}
	if _, err := runLatencyScriptWithProvider(cwd, slog.LevelInfo, quiet); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(debug.seen, quiet.seen) {
		t.Fatal("diagnostics changed outbound messages")
	}
	if !reflect.DeepEqual(debug.definitions, quiet.definitions) {
		t.Fatal("diagnostics changed tool definitions")
	}
}

func TestRequestLatencyHTTPErrorOmitsUnobservedStages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	client, err := llm.HTTPClientForProviderProxy("none")
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	var buf bytes.Buffer
	d := &requestLatency{log: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), id: 1, session: "sess_latency"}
	ctx := d.begin(context.Background(), false)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/PRIVATE_PATH_SENTINEL", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected refused connection")
	}
	d.finish(context.Background())
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, url) || strings.Contains(line, "SENTINEL") {
			t.Fatalf("error context leaked: %s", line)
		}
		if r["msg"] != "llm http request latency" {
			continue
		}
		found = true
		for _, field := range []string{"status_code", "protocol", "connection_ms", "wrote_request_ms", "first_byte_ms", "reused"} {
			if _, ok := r[field]; ok {
				t.Fatalf("invented observation %s: %v", field, r)
			}
		}
	}
	if !found {
		t.Fatal("missing failed HTTP timing")
	}
}
