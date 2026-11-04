//go:build http

// Two-process end-to-end harness for the chained Coddy topology:
//
//	client -> A (Coddy agent, provider type openai) -> B (Coddy direct model
//	provider, type openai) -> scripted recording upstream.
//
// A runs a real ReAct loop and executes its own read tool in its own
// workspace; B only relays to the scripted upstream and never runs tools.
// The chain exercises reasoning replay (#701) and tool-call forwarding with
// no live LLM: the upstream is an httptest server that records every request
// and answers with a deterministic tool call then a final answer. B also
// answers direct /v1/chat/completions and /v1/responses calls, which the
// #580 contract tests drive separately; A never uses the Responses API.
//
// The binary is built once (tags=http) into a temp path and cached; every
// stand runs as a real `coddy serve` process with an isolated HOME,
// CODDY_HOME, workspace and config. Children are killed and waited on
// cleanup, and every wait is bounded.
package httpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"
)

// ---------------------------------------------------------------------------
// Binary build (once per test binary, cached).
// ---------------------------------------------------------------------------

var (
	tcBinOnce sync.Once
	tcBinPath string
	tcBinErr  error
)

// tcBinary builds the http-tagged coddy binary once into a temp dir owned by
// the calling test. TestTwoCoddy calls it once at the top level, so the
// parent test's t.TempDir keeps the binary alive for every subtest and
// removes it when the whole suite is done.
func tcBinary(t *testing.T) string {
	t.Helper()
	tcBinOnce.Do(func() {
		root, err := tcRepoRoot()
		if err != nil {
			tcBinErr = err
			return
		}
		dir := t.TempDir()
		name := "coddy"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		bin := filepath.Join(dir, name)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-tags=http", "-o", bin, "./cmd/coddy")
		cmd.Dir = root
		platform.AdaptCommand(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			tcBinErr = fmt.Errorf("build coddy binary: %w\n%s", err, out)
			return
		}
		tcBinPath = bin
	})
	if tcBinErr != nil {
		t.Fatalf("build shared coddy binary: %v", tcBinErr)
	}
	return tcBinPath
}

// tcRepoRoot walks up from the working directory to the module root (the
// folder holding go.mod and cmd/coddy).
func tcRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "cmd", "coddy")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod with cmd/coddy not found above %s", dir)
		}
		dir = parent
	}
}

// ---------------------------------------------------------------------------
// Scripted recording upstream.
// ---------------------------------------------------------------------------

// tcUpstreamScript is the deterministic answer plan the recording upstream
// follows for the tool chain.
type tcUpstreamScript struct {
	// reasoningContent is what the assistant tool-call message carries as
	// reasoning_content, the value #701 must replay on the follow-up.
	reasoningContent string
	// toolCallID is the id stamped on the read tool call.
	toolCallID string
	// toolPath is the path the read tool is asked to open.
	toolPath string
	// noteToken is the unique marker written into note.txt and echoed back in
	// the final answer so the test can prove the tool result flowed the whole
	// chain.
	noteToken string
}

// tcReq is one chat-completions body the upstream received.
type tcReq struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Messages []tcMsg         `json:"messages"`
	Tools    json.RawMessage `json:"tools,omitempty"`
	Raw      json.RawMessage `json:"-"`
}

type tcMsg struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Reasoning    string          `json:"reasoning_content"`
	ReasoningAlt string          `json:"reasoning"`
	ToolCallID   string          `json:"tool_call_id"`
	ToolCalls    json.RawMessage `json:"tool_calls"`
	Name         string          `json:"name"`
}

func (m tcMsg) text() string {
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	return string(m.Content)
}

// hasToolResult reports whether any message is a tool result, which marks the
// follow-up turn.
func (r tcReq) hasToolResult() bool {
	for _, m := range r.Messages {
		if m.Role == "tool" {
			return true
		}
	}
	return false
}

// tcUpstreamMode picks the stub's answer plan.
type tcUpstreamMode int

const (
	// tcModeToolchain answers a read tool call on the first request and a
	// final answer echoing the tool result on every later request.
	tcModeToolchain tcUpstreamMode = iota
	// tcModePlain answers a plain final text on every request; used for the
	// B-direct /v1/responses and /v1/chat/completions drivers.
	tcModePlain
	// tcModeError answers every request with the configured error status.
	tcModeError
)

// tcUpstream is the recording stub behind B. emitUsage controls whether
// answers carry a usage object (known vs unknown usage for #701).
type tcUpstream struct {
	srv       *httptest.Server
	mode      tcUpstreamMode
	script    tcUpstreamScript
	emitUsage bool
	// errStatus / errHeaders drive tcModeError; both are set before the server
	// starts, never afterwards.
	errStatus  int
	errHeaders map[string]string

	mu       sync.Mutex
	requests []tcReq
}

// tcUpstreamOpt tweaks the stub before it starts.
type tcUpstreamOpt func(*tcUpstream)

func withUsage(v bool) tcUpstreamOpt { return func(u *tcUpstream) { u.emitUsage = v } }
func withError(status int, headers map[string]string) tcUpstreamOpt {
	return func(u *tcUpstream) {
		u.mode = tcModeError
		u.errStatus = status
		u.errHeaders = headers
	}
}

func newTCUpstream(t *testing.T, mode tcUpstreamMode, script tcUpstreamScript, opts ...tcUpstreamOpt) *tcUpstream {
	t.Helper()
	u := &tcUpstream{mode: mode, script: script, emitUsage: true}
	for _, o := range opts {
		o(u)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"test-model","object":"model","owned_by":"stub"}]}`))
	})
	mux.HandleFunc("/v1/chat/completions", u.handleChat)
	u.srv = httptest.NewServer(mux)
	t.Cleanup(u.srv.Close)
	return u
}

func (u *tcUpstream) baseURL() string { return u.srv.URL + "/v1" }

func (u *tcUpstream) record(req tcReq) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.requests = append(u.requests, req)
}

func (u *tcUpstream) all() []tcReq {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]tcReq, len(u.requests))
	copy(out, u.requests)
	return out
}

func (u *tcUpstream) handleChat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var req tcReq
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Raw = json.RawMessage(append([]byte(nil), body...))
	u.record(req)

	if u.mode == tcModeError {
		for k, v := range u.errHeaders {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(u.errStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": fmt.Sprintf("stub upstream %d", u.errStatus), "type": "stub_error"},
		})
		return
	}

	switch {
	case u.mode == tcModePlain:
		u.answerFinal(w, req, "DIRECT-ANSWER "+u.script.noteToken)
	case req.hasToolResult():
		u.answerFinal(w, req, u.finalText(req))
	default:
		u.answerToolCall(w, req)
	}
}

// finalText builds the follow-up answer, echoing the tool result so the test
// can prove the read output travelled A -> B -> upstream and back.
func (u *tcUpstream) finalText(req tcReq) string {
	var toolText string
	for _, m := range req.Messages {
		if m.Role == "tool" {
			toolText = m.text()
		}
	}
	if !strings.Contains(toolText, u.script.noteToken) {
		return "TOOL-MISSING:" + toolText
	}
	return "FINAL-ANSWER " + u.script.noteToken
}

func (u *tcUpstream) toolCallMessage() map[string]any {
	args, _ := json.Marshal(map[string]string{"path": u.script.toolPath})
	return map[string]any{
		"role":              "assistant",
		"content":           "",
		"reasoning_content": u.script.reasoningContent,
		"tool_calls": []map[string]any{{
			"id":   u.script.toolCallID,
			"type": "function",
			"function": map[string]any{
				"name":      "read",
				"arguments": string(args),
			},
		}},
	}
}

func (u *tcUpstream) usage() map[string]any {
	if !u.emitUsage {
		return nil
	}
	return map[string]any{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}
}

func (u *tcUpstream) answerToolCall(w http.ResponseWriter, req tcReq) {
	msg := u.toolCallMessage()
	if req.Stream {
		u.writeSSE(w, msg, "")
		return
	}
	resp := map[string]any{
		"id":      "chatcmpl-stub-1",
		"object":  "chat.completion",
		"model":   req.Model,
		"created": 1,
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": "tool_calls",
		}},
	}
	if us := u.usage(); us != nil {
		resp["usage"] = us
	}
	tcWriteJSON(w, resp)
}

func (u *tcUpstream) answerFinal(w http.ResponseWriter, req tcReq, text string) {
	msg := map[string]any{"role": "assistant", "content": text}
	if req.Stream {
		u.writeSSE(w, msg, text)
		return
	}
	resp := map[string]any{
		"id":      "chatcmpl-stub-2",
		"object":  "chat.completion",
		"model":   req.Model,
		"created": 1,
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": "stop",
		}},
	}
	if us := u.usage(); us != nil {
		resp["usage"] = us
	}
	tcWriteJSON(w, resp)
}

// writeSSE streams an assistant message as OpenAI chat-completion chunks.
// When text is non-empty the message is a plain text answer; otherwise it is
// the tool-call message.
func (u *tcUpstream) writeSSE(w http.ResponseWriter, msg map[string]any, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	chunk := func(delta map[string]any, finish any, withUsage bool) {
		payload := map[string]any{
			"id":      "chatcmpl-stub-sse",
			"object":  "chat.completion.chunk",
			"created": 1,
			"model":   "test-model",
			"choices": []map[string]any{{
				"index":         0,
				"delta":         delta,
				"finish_reason": finish,
			}},
		}
		if withUsage {
			if us := u.usage(); us != nil {
				payload["usage"] = us
			}
		}
		raw, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		if flusher != nil {
			flusher.Flush()
		}
	}

	if text != "" {
		chunk(map[string]any{"role": "assistant", "content": text}, nil, false)
		chunk(map[string]any{}, "stop", true)
	} else {
		args, _ := json.Marshal(map[string]string{"path": u.script.toolPath})
		chunk(map[string]any{
			"role":              "assistant",
			"reasoning_content": u.script.reasoningContent,
			"tool_calls": []map[string]any{{
				"index": 0,
				"id":    u.script.toolCallID,
				"type":  "function",
				"function": map[string]any{
					"name":      "read",
					"arguments": string(args),
				},
			}},
		}, nil, false)
		chunk(map[string]any{}, "tool_calls", true)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func tcWriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ---------------------------------------------------------------------------
// Coddy process stand.
// ---------------------------------------------------------------------------

// tcStand is a running `coddy serve` process with its isolated state.
type tcStand struct {
	name string
	port int
	home string
	cwd  string
	cfg  string
	cmd  *exec.Cmd
	logF *os.File
	base string
	done chan error // cmd.Wait result, for early-exit detection
}

// tcFreePort reserves a free loopback port.
func tcFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// tcWriteConfigYAML writes a minimal serve config into dir and returns its
// path. Paths are quoted with %q so Windows paths survive YAML.
func tcWriteConfigYAML(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// tcStandSpec describes one coddy serve process to start.
type tcStandSpec struct {
	name        string
	provider    string // providers[].name
	apiBase     string // providers[].api_base
	apiKey      string // providers[].api_key
	modelRef    string // models[].model ("<provider>/<model id>")
	modelStream bool   // models[].stream
	retryMax    int    // agent.llm_retry_max, zero in ordinary cases
	authToken   string // httpserver.auth_token
	bypass      bool   // write permission-mode.json bypass into home
	noteToken   string // when set, write workspace/note.txt containing it
}

// startTCStand creates an isolated home + workspace + config and launches
// `coddy serve` as a real subprocess. The process is killed and waited on
// cleanup.
func startTCStand(t *testing.T, bin string, spec tcStandSpec) *tcStand {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	ws := filepath.Join(root, "ws")
	for _, d := range []string{home, ws} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if spec.bypass {
		mode := `{"permissionMode":"bypass","updatedAt":"2026-01-01T00:00:00Z"}`
		if err := os.WriteFile(filepath.Join(home, "permission-mode.json"), []byte(mode), 0o600); err != nil {
			t.Fatalf("write permission-mode.json: %v", err)
		}
	}
	if spec.noteToken != "" {
		if err := os.WriteFile(filepath.Join(ws, "note.txt"), []byte("contents: "+spec.noteToken+"\n"), 0o600); err != nil {
			t.Fatalf("write note.txt: %v", err)
		}
	}
	port := tcFreePort(t)
	cfg := fmt.Sprintf(`providers:
  - name: %q
    type: "openai"
    api_base: %q
    api_key: %q
models:
  - model: %q
    stream: %v
agent:
  model: %q
  llm_retry_max: %d
  llm_retry_base_ms: 100
httpserver:
  enable: true
  host: "127.0.0.1"
  port: %d
  auth_token: %q
skills:
  auto_discovery: false
`,
		spec.provider, spec.apiBase, spec.apiKey,
		spec.modelRef, spec.modelStream,
		spec.modelRef, spec.retryMax,
		port, spec.authToken,
	)
	cfgPath := tcWriteConfigYAML(t, root, cfg)

	logF, err := os.Create(filepath.Join(root, "serve.log"))
	if err != nil {
		t.Fatalf("create serve log: %v", err)
	}
	cmd := exec.Command(bin, "serve",
		"--home", home,
		"--cwd", ws,
		"--config", cfgPath,
	)
	platform.AdaptCommand(cmd)
	cmd.Stdout = logF
	cmd.Stderr = logF
	// Isolated environment: strip every CODDY_* variable and HOME so nothing
	// leaks from the developer machine, then set HOME to the temp home.
	env := os.Environ()
	keep := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, "CODDY_") || strings.HasPrefix(e, "HOME=") {
			continue
		}
		keep = append(keep, e)
	}
	keep = append(keep, "HOME="+home)
	cmd.Env = keep

	if err := cmd.Start(); err != nil {
		_ = logF.Close()
		t.Fatalf("start coddy serve %s: %v", spec.name, err)
	}
	st := &tcStand{
		name: spec.name, port: port, home: home, cwd: ws,
		cfg: cfgPath, cmd: cmd, logF: logF,
		base: fmt.Sprintf("http://127.0.0.1:%d", port),
		done: make(chan error, 1),
	}
	go func() { st.done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case err := <-st.done:
			if err != nil {
				t.Logf("%s process exit after cleanup: %v", spec.name, err)
			}
		case <-time.After(10 * time.Second):
			t.Logf("%s: process did not exit within 10s of kill", spec.name)
		}
		_ = logF.Close()
		if t.Failed() {
			if tail, err := os.ReadFile(logF.Name()); err == nil {
				const max = 8000
				if len(tail) > max {
					tail = tail[len(tail)-max:]
				}
				t.Logf("%s serve.log tail:\n%s", spec.name, tail)
			}
		}
	})
	return st
}

// waitReady polls /v1/models until the stand answers, bounded; an early
// process exit fails fast with the log tail.
func (s *tcStand) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	url := s.base + "/v1/models"
	for time.Now().Before(deadline) {
		select {
		case err := <-s.done:
			t.Fatalf("coddy serve %s exited before ready: %v", s.name, err)
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("coddy serve %s not ready within 45s", s.name)
}

// ---------------------------------------------------------------------------
// Client driver.
// ---------------------------------------------------------------------------

// tcChatResult is what a client turn yields.
type tcChatResult struct {
	Status    int
	SessionID string
	Text      string // assistant text (non-stream or accumulated SSE)
	Usage     map[string]any
	Raw       []byte
	Headers   http.Header
	Events    []string // SSE event names seen (stream only)
}

// tcPostChat issues POST /v1/chat/completions against a stand.
func tcPostChat(t *testing.T, s *tcStand, token, model string, messages []map[string]any, stream bool, sessionID string) tcChatResult {
	t.Helper()
	body := map[string]any{"model": model, "messages": messages, "stream": stream}
	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, s.base+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if sessionID != "" {
		req.Header.Set("X-Coddy-Session-ID", sessionID)
	}
	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST chat on %s: %v", s.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	res := tcChatResult{Status: resp.StatusCode, Headers: resp.Header.Clone(), SessionID: resp.Header.Get("X-Coddy-Session-ID")}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body on %s: %v", s.name, err)
	}
	res.Raw = data
	if resp.StatusCode != http.StatusOK {
		return res
	}
	if stream {
		res.Text, res.Events = tcCollectSSEChat(t, data)
		return res
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(data, &parsed); err == nil && len(parsed.Choices) > 0 {
		res.Text = parsed.Choices[0].Message.Content
		res.Usage = parsed.Usage
	}
	return res
}

// tcCollectSSEChat parses an SSE chat-completions stream and returns the
// accumulated assistant text plus the data payloads seen.
func tcCollectSSEChat(t *testing.T, data []byte) (string, []string) {
	var text strings.Builder
	var events []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		events = append(events, payload)
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) == nil {
			for _, c := range chunk.Choices {
				text.WriteString(c.Delta.Content)
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan SSE: %v", err)
	}
	return text.String(), events
}

var tcSessionIDRe = regexp.MustCompile(`sess_[A-Za-z0-9]+`)

// tcSessionFallback finds a session id when the response header carries none:
// it scans the SSE payloads and, failing that, asks /coddy/sessions for the
// newest id.
func tcSessionFallback(t *testing.T, s *tcStand, token string, res tcChatResult) string {
	t.Helper()
	if res.SessionID != "" {
		return res.SessionID
	}
	if id := tcSessionIDRe.Find(res.Raw); id != nil {
		return string(id)
	}
	req, err := http.NewRequest(http.MethodGet, s.base+"/coddy/sessions", nil)
	if err != nil {
		t.Fatalf("sessions request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET sessions on %s: %v", s.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read sessions body on %s: %v", s.name, err)
	}
	if id := tcSessionIDRe.Find(body); id != nil {
		return string(id)
	}
	t.Fatalf("no session id discoverable on %s", s.name)
	return ""
}

// ---------------------------------------------------------------------------
// Chain construction shared by the matrix and the godog suite.
// ---------------------------------------------------------------------------

// tcChain is one running A -> B -> upstream chain.
type tcChain struct {
	upstream *tcUpstream
	B        *tcStand
	A        *tcStand
	tokenA   string
	tokenB   string
	script   tcUpstreamScript
}

// startTCChain builds the full A -> B -> upstream chain with the given
// stream flags. bModelStream is models[].stream on B (the B -> upstream
// transport); clientStream only varies the client's request to A.
func startTCChain(t *testing.T, bModelStream bool, opts ...tcUpstreamOpt) *tcChain {
	t.Helper()
	bin := tcBinary(t)
	script := tcUpstreamScript{
		reasoningContent: "REASON-TRACE-701 abc123",
		toolCallID:       "call_701",
		toolPath:         "note.txt",
		noteToken:        "NOTE-TOKEN-8842",
	}
	up := newTCUpstream(t, tcModeToolchain, script, opts...)
	b := startTCStand(t, bin, tcStandSpec{
		name: "B", provider: "bstub",
		apiBase: up.baseURL(), apiKey: "up-key",
		modelRef: "bstub/test-model", modelStream: bModelStream,
		authToken: "b-secret",
	})
	b.waitReady(t)
	a := startTCStand(t, bin, tcStandSpec{
		name: "A", provider: "bchain",
		apiBase: b.base + "/v1", apiKey: "b-secret",
		modelRef: "bchain/bstub/test-model", modelStream: true,
		authToken: "a-secret",
		bypass:    true,
		noteToken: script.noteToken,
	})
	a.waitReady(t)
	return &tcChain{upstream: up, B: b, A: a, tokenA: "a-secret", tokenB: "b-secret", script: script}
}

// tcAssertTurn runs one agent turn on A and checks the whole chain: the tool
// executed only in A's workspace, the upstream saw the reasoning replay and
// the tool result, and the final answer confirms the tool content. Returns
// the session id for follow-up turns.
func tcAssertTurn(t *testing.T, ch *tcChain, clientStream bool) string {
	t.Helper()
	msgs := []map[string]any{{
		"role":    "user",
		"content": "Please read the file note.txt using the read tool.",
	}}
	res := tcPostChat(t, ch.A, ch.tokenA, "agent", msgs, clientStream, "")
	if res.Status != http.StatusOK {
		t.Fatalf("A agent turn: status %d, body: %s", res.Status, truncate(string(res.Raw), 2000))
	}
	sessionID := tcSessionFallback(t, ch.A, ch.tokenA, res)

	want := "FINAL-ANSWER " + ch.script.noteToken
	if !strings.Contains(res.Text, want) {
		t.Fatalf("final answer missing %q, got %q", want, truncate(res.Text, 500))
	}

	tcAssertUpstreamTurn(t, ch, clientStream)
	return sessionID
}

// tcAssertUpstreamTurn checks the recorded upstream requests of the first
// agent turn: request 1 asked for the read, request 2 replayed the assistant
// reasoning and the tool result exactly once.
func tcAssertUpstreamTurn(t *testing.T, ch *tcChain, clientStream bool) {
	t.Helper()
	reqs := ch.upstream.all()
	if len(reqs) < 2 {
		t.Fatalf("upstream saw %d requests, want at least 2", len(reqs))
	}
	first, second := reqs[0], reqs[1]
	if first.hasToolResult() {
		t.Fatalf("first upstream request already carried a tool result")
	}
	tcAssertReplayedAssistant(t, ch, second)
	tcAssertToolResult(t, ch, second)
}

// tcAssertReplayedAssistant checks that the follow-up request carries the
// assistant tool-call message with its reasoning_content replayed verbatim.
func tcAssertReplayedAssistant(t *testing.T, ch *tcChain, req tcReq) {
	t.Helper()
	found := 0
	for _, m := range req.Messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		found++
		var calls []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		if err := json.Unmarshal(m.ToolCalls, &calls); err != nil || len(calls) != 1 {
			t.Fatalf("invalid calls: %s", m.ToolCalls)
		}
		if calls[0].ID == "" {
			t.Fatal("replayed tool call has no ID")
		}
		var args map[string]any
		if calls[0].Function.Name != "read" || json.Unmarshal([]byte(calls[0].Function.Arguments), &args) != nil || args["path"] != ch.script.toolPath {
			t.Fatalf("unexpected call: %s", m.ToolCalls)
		}
		reasoning := m.Reasoning
		if reasoning == "" {
			reasoning = m.ReasoningAlt
		}
		if reasoning != ch.script.reasoningContent {
			t.Fatalf("assistant reasoning not replayed: got %q, want %q (message: %s)",
				reasoning, ch.script.reasoningContent, rawMsg(m))
		}
	}
	if found != 1 {
		t.Fatalf("want exactly one assistant tool-call message, got %d in %s", found, rawReq(req))
	}
}

// tcAssertToolResult checks that the follow-up request carries the tool
// result bound to the call id of the assistant tool call in the same request
// (the id may be rewritten through the chain; call and result must agree) and
// containing the note token, which proves read ran in A's workspace, the only
// place note.txt exists.
func tcAssertToolResult(t *testing.T, ch *tcChain, req tcReq) {
	t.Helper()
	callID := tcFirstToolCallID(t, req)
	found := 0
	for _, m := range req.Messages {
		if m.Role != "tool" {
			continue
		}
		found++
		if m.ToolCallID != callID {
			t.Fatalf("tool result bound to %q, want the assistant call id %q", m.ToolCallID, callID)
		}
		if !strings.Contains(m.text(), ch.script.noteToken) {
			t.Fatalf("tool result missing note token %q: %s", ch.script.noteToken, m.text())
		}
	}
	if found != 1 {
		t.Fatalf("want exactly one tool result message, got %d in %s", found, rawReq(req))
	}
}

// tcFirstToolCallID extracts the id of the first tool call of the assistant
// message in the request.
func tcFirstToolCallID(t *testing.T, req tcReq) string {
	t.Helper()
	for _, m := range req.Messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		var calls []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(m.ToolCalls, &calls); err != nil || len(calls) == 0 {
			t.Fatalf("assistant tool_calls unparseable: %s", m.ToolCalls)
		}
		return calls[0].ID
	}
	t.Fatalf("no assistant tool-call message in %s", rawReq(req))
	return ""
}

// tcAssertFollowUpTurn sends a second user turn on the same session and
// verifies the earlier assistant tool call, its reasoning and the tool
// result are replayed exactly once (ordinary multi-turn, no duplication).
// The follow-up request is located by its user text rather than by request
// count: an asynchronous session-naming call of the agent may add upstream
// requests of its own after the first turn.
func tcAssertFollowUpTurn(t *testing.T, ch *tcChain, clientStream bool, sessionID string) {
	t.Helper()
	const marker = "What did the file say? Summarize briefly."
	msgs := []map[string]any{{
		"role":    "user",
		"content": marker,
	}}
	res := tcPostChat(t, ch.A, ch.tokenA, "agent", msgs, clientStream, sessionID)
	if res.Status != http.StatusOK {
		t.Fatalf("A follow-up turn: status %d, body: %s", res.Status, truncate(string(res.Raw), 2000))
	}
	var followUp *tcReq
	for i := range ch.upstream.all() {
		req := ch.upstream.all()[i]
		for _, m := range req.Messages {
			if m.Role == "user" && strings.Contains(m.text(), marker) {
				r := req
				followUp = &r
			}
		}
	}
	if followUp == nil {
		t.Fatalf("upstream never saw the follow-up turn; requests: %d", len(ch.upstream.all()))
	}
	tcAssertReplayedAssistant(t, ch, *followUp)
	tcAssertToolResult(t, ch, *followUp)
}

func rawMsg(m tcMsg) string {
	raw, _ := json.Marshal(m)
	return string(raw)
}

func rawReq(r tcReq) string {
	if len(r.Raw) > 0 {
		return string(r.Raw)
	}
	raw, _ := json.Marshal(r)
	return string(raw)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------------------------------------------------------------------------
// Top-level suite: one shared binary build, one parent TempDir.
// ---------------------------------------------------------------------------

// TestTwoCoddy is the single top-level entry for the two-process suite. The
// binary is built once into this test's TempDir, which stays alive for every
// subtest and is removed when the suite ends; every stand gets its own
// t.TempDir under its subtest.
func TestTwoCoddy(t *testing.T) {
	tcBinary(t) // build once; fails fast here on a build error
	t.Cleanup(func() { tcBinOnce = sync.Once{}; tcBinPath = ""; tcBinErr = nil })
	t.Run("chain_matrix", testTCChainMatrix)
	t.Run("feature", testTCFeature)
	t.Run("direct_responses", testTCDirectResponses)
	t.Run("direct_errors", testTCDirectErrors)
	t.Run("session_isolation", testTCIsolation)
	t.Run("agent_session_isolation", testTCAgentIsolation)
	t.Run("agent_errors", testTCAgentErrors)
	t.Run("agent_retry", testTCAgentRetry)
	t.Run("usage", testTCUsage)
	t.Run("auth_and_validation", testTCAuthValidation)
}

// ---------------------------------------------------------------------------
// Matrix: 4 combinations of client stream x B upstream stream.
// ---------------------------------------------------------------------------

// testTCChainMatrix runs the real two-process chain in all four
// combinations of the client's stream flag and B's configured model stream
// to the upstream.
func testTCChainMatrix(t *testing.T) {
	for _, clientStream := range []bool{true, false} {
		for _, bStream := range []bool{true, false} {
			name := fmt.Sprintf("client_stream=%v/upstream_stream=%v", clientStream, bStream)
			t.Run(name, func(t *testing.T) {
				ch := startTCChain(t, bStream)
				sessionID := tcAssertTurn(t, ch, clientStream)
				tcAssertFollowUpTurn(t, ch, clientStream, sessionID)
				// B's upstream transport must match the configured model
				// stream for this combo.
				for i, r := range ch.upstream.all() {
					if r.Stream != bStream {
						t.Fatalf("upstream request %d stream=%v, want %v", i, r.Stream, bStream)
					}
				}
				// note.txt lives only in A's workspace: if it ever appeared
				// in B's, B ran the tool instead of relaying.
				if _, err := os.Stat(filepath.Join(ch.B.cwd, "note.txt")); !os.IsNotExist(err) {
					t.Fatalf("note.txt must exist only in A's workspace; B cwd: %v", err)
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Godog suite over the same chain.
// ---------------------------------------------------------------------------

type tcSuite struct {
	t       *testing.T
	pending tcPendingChain
}

// tcPendingChain holds partially built chain state between godog steps. It is
// per-suite state on tcSuite, never a package global.
type tcPendingChain struct {
	script tcUpstreamScript
	up     *tcUpstream
	b      *tcStand
	a      *tcStand
	result tcChatResult
}

func testTCFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "two coddy provider chain",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &tcSuite{t: t}
			sc.Step(`^a scripted upstream that answers a read tool call then a final answer$`, s.stepUpstream)
			sc.Step(`^a Coddy server B whose provider points at that upstream$`, s.stepB)
			sc.Step(`^a Coddy server A whose provider points at B$`, s.stepA)
			sc.Step(`^A runs an agent turn asking to read note\.txt in its workspace$`, s.stepRunTurn)
			sc.Step(`^the read tool executes in the workspace of A only$`, s.stepToolInAOnly)
			sc.Step(`^the upstream sees the assistant reasoning and the tool result on the follow-up$`, s.stepReasoningAndResult)
			sc.Step(`^the follow-up answer confirms the tool result$`, s.stepAnswerConfirms)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/two_coddy_provider.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}

func (s *tcSuite) stepUpstream() error {
	s.pending.script = tcUpstreamScript{
		reasoningContent: "REASON-TRACE-701 abc123",
		toolCallID:       "call_701",
		toolPath:         "note.txt",
		noteToken:        "NOTE-TOKEN-8842",
	}
	s.pending.up = newTCUpstream(s.t, tcModeToolchain, s.pending.script)
	return nil
}

func (s *tcSuite) stepB() error {
	if s.pending.up == nil {
		return fmt.Errorf("upstream step did not run")
	}
	s.pending.b = startTCStand(s.t, tcBinary(s.t), tcStandSpec{
		name: "B", provider: "bstub",
		apiBase: s.pending.up.baseURL(), apiKey: "up-key",
		modelRef: "bstub/test-model", modelStream: true,
		authToken: "b-secret",
	})
	s.pending.b.waitReady(s.t)
	return nil
}

func (s *tcSuite) stepA() error {
	if s.pending.b == nil {
		return fmt.Errorf("B step did not run")
	}
	s.pending.a = startTCStand(s.t, tcBinary(s.t), tcStandSpec{
		name: "A", provider: "bchain",
		apiBase: s.pending.b.base + "/v1", apiKey: "b-secret",
		modelRef: "bchain/bstub/test-model", modelStream: true,
		authToken: "a-secret",
		bypass:    true,
		noteToken: s.pending.script.noteToken,
	})
	s.pending.a.waitReady(s.t)
	return nil
}

func (s *tcSuite) stepRunTurn() error {
	if s.pending.a == nil {
		return fmt.Errorf("A step did not run")
	}
	msgs := []map[string]any{{
		"role":    "user",
		"content": "Please read the file note.txt using the read tool.",
	}}
	res := tcPostChat(s.t, s.pending.a, "a-secret", "agent", msgs, true, "")
	if res.Status != http.StatusOK {
		return fmt.Errorf("agent turn status %d: %s", res.Status, truncate(string(res.Raw), 2000))
	}
	s.pending.result = res
	return nil
}

func (s *tcSuite) stepToolInAOnly() error {
	reqs := s.pending.up.all()
	if len(reqs) < 2 {
		return fmt.Errorf("upstream saw %d requests, want at least 2", len(reqs))
	}
	// The final answer echoing the token only happens when the tool result
	// carried note.txt's content, which exists only in A's workspace.
	want := "FINAL-ANSWER " + s.pending.script.noteToken
	if !strings.Contains(s.pending.result.Text, want) {
		return fmt.Errorf("final answer %q lacks %q; tool likely did not read A's note.txt", truncate(s.pending.result.Text, 300), want)
	}
	return nil
}

func (s *tcSuite) stepReasoningAndResult() error {
	ch := &tcChain{upstream: s.pending.up, script: s.pending.script}
	tcAssertUpstreamTurn(s.t, ch, true)
	return nil
}

func (s *tcSuite) stepAnswerConfirms() error {
	want := s.pending.script.noteToken
	if !strings.Contains(s.pending.result.Text, want) {
		return fmt.Errorf("final answer does not echo tool result token %q", want)
	}
	return nil
}

// ---------------------------------------------------------------------------
// B-direct drivers: Responses API (#580) and chat/completions extras.
// ---------------------------------------------------------------------------

// startTCDirectPair builds one B stand over a plain scripted upstream for
// direct (non-agent) drivers.
func startTCDirectPair(t *testing.T, mode tcUpstreamMode, opts ...tcUpstreamOpt) (*tcUpstream, *tcStand) {
	t.Helper()
	bin := tcBinary(t)
	up := newTCUpstream(t, mode, tcUpstreamScript{noteToken: "DIRECT-TOKEN-77"}, opts...)
	b := startTCStand(t, bin, tcStandSpec{
		name: "B", provider: "bstub",
		apiBase: up.baseURL(), apiKey: "up-key",
		modelRef: "bstub/test-model", modelStream: true,
		authToken: "b-secret",
	})
	b.waitReady(t)
	return up, b
}

// TestTwoCoddyDirectResponses drives B's /v1/responses directly (the #580
// contract): typed input array, resp_ id, typed output and, streaming, a
// response.completed event. A is never involved; A's provider speaks
// chat/completions only.
func testTCDirectResponses(t *testing.T) {
	up, b := startTCDirectPair(t, tcModePlain)
	model := "bstub/test-model"

	t.Run("json_input_array", func(t *testing.T) {
		body := map[string]any{
			"model": model,
			"input": []map[string]any{{
				"type": "message", "role": "user",
				"content": []map[string]any{{"type": "input_text", "text": "hello"}},
			}},
			"stream": false,
		}
		res := tcPost(t, b, "b-secret", "/v1/responses", body)
		if res.Status != http.StatusOK {
			t.Fatalf("status %d: %s", res.Status, truncate(string(res.Raw), 2000))
		}
		var resp struct {
			ID     string          `json:"id"`
			Object string          `json:"object"`
			Status string          `json:"status"`
			Output json.RawMessage `json:"output"`
		}
		if err := json.Unmarshal(res.Raw, &resp); err != nil {
			t.Fatalf("parse response: %v\n%s", err, truncate(string(res.Raw), 1000))
		}
		if !strings.HasPrefix(resp.ID, "resp_") {
			t.Fatalf("response id %q lacks resp_ prefix", resp.ID)
		}
		if len(resp.Output) == 0 || string(resp.Output) == "null" {
			t.Fatalf("typed output missing: %s", truncate(string(res.Raw), 1000))
		}
	})

	t.Run("sse_completed_event", func(t *testing.T) {
		body := map[string]any{
			"model": model,
			"input": []map[string]any{{
				"type": "message", "role": "user",
				"content": []map[string]any{{"type": "input_text", "text": "hello"}},
			}},
			"stream": true,
		}
		res := tcPost(t, b, "b-secret", "/v1/responses", body)
		if res.Status != http.StatusOK {
			t.Fatalf("status %d: %s", res.Status, truncate(string(res.Raw), 2000))
		}
		if !bytes.Contains(res.Raw, []byte("response.completed")) {
			t.Fatalf("SSE stream lacks response.completed event:\n%s", truncate(string(res.Raw), 3000))
		}
	})

	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("tool_and_reasoning_replay/stream=%v", stream), func(t *testing.T) {
			// A multi-item input carrying an assistant function_call, a
			// function_call_output and reasoning must reach the upstream as chat
			// history (typed tools + reasoning replay on the direct surface).
			body := map[string]any{
				"model": model,
				"input": []map[string]any{
					{"type": "message", "role": "user",
						"content": []map[string]any{{"type": "input_text", "text": "read it"}}},
					{"type": "reasoning", "summary": []map[string]any{
						{"type": "summary_text", "text": "REASON-DIRECT-55"}}},
					{"type": "function_call", "call_id": "call_d1", "name": "read",
						"arguments": `{"path":"note.txt"}`},
					{"type": "function_call_output", "call_id": "call_d1",
						"output": "DIRECT-TOKEN-77 contents"},
					{"type": "message", "role": "user",
						"content": []map[string]any{{"type": "input_text", "text": "and?"}}},
				},
				"stream": stream,
				"tools":  []map[string]any{{"type": "function", "name": "read", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}},
			}
			res := tcPost(t, b, "b-secret", "/v1/responses", body)
			if res.Status != http.StatusOK {
				t.Fatalf("status %d: %s", res.Status, truncate(string(res.Raw), 2000))
			}
			reqs := up.all()
			if len(reqs) == 0 {
				t.Fatalf("upstream saw no request")
			}
			last := reqs[len(reqs)-1]
			if len(last.Messages) != 4 {
				t.Fatalf("wrong replay length: %s", rawReq(last))
			}
			assistant, tool := last.Messages[1], last.Messages[2]
			if assistant.Role != "assistant" || assistant.Reasoning != "REASON-DIRECT-55" || tool.Role != "tool" || tool.ToolCallID != "call_d1" || tool.text() != "DIRECT-TOKEN-77 contents" {
				t.Fatalf("wrong replay fields: %s", rawReq(last))
			}
			calls := gjson.ParseBytes(assistant.ToolCalls).Array()
			if len(calls) != 1 || calls[0].Get("id").String() != "call_d1" || calls[0].Get("function.name").String() != "read" || calls[0].Get("function.arguments").String() != `{"path":"note.txt"}` {
				t.Fatalf("wrong function call: %s", assistant.ToolCalls)
			}
			if gjson.GetBytes(last.Tools, "0.function.name").String() != "read" {
				t.Fatalf("offered tool lost: %s", last.Tools)
			}
			answer := gjson.ParseBytes(res.Raw)
			if stream {
				frames := parseSSEFrames(string(res.Raw))
				if len(frames) == 0 {
					t.Fatal("no Responses events")
				}
				for i, frame := range frames {
					if !strings.HasPrefix(frame.event, "response.") || gjson.Get(frame.data, "type").String() != frame.event || gjson.Get(frame.data, "sequence_number").Int() != int64(i) {
						t.Fatalf("invalid event %d: %+v", i, frame)
					}
				}
				if frames[len(frames)-1].event != "response.completed" || bytes.Contains(res.Raw, []byte("[DONE]")) || bytes.Contains(res.Raw, []byte("chat.completion.chunk")) {
					t.Fatalf("wrong Responses terminal: %s", res.Raw)
				}
				answer = gjson.Get(frames[len(frames)-1].data, "response")
			}
			if answer.Get("object").String() != "response" || answer.Get("status").String() != "completed" || answer.Get("output.0.type").String() != "message" || answer.Get("output.0.content.0.type").String() != "output_text" || answer.Get("output.0.content.0.text").String() != "DIRECT-ANSWER DIRECT-TOKEN-77" || answer.Get("usage.total_tokens").Int() != 18 {
				t.Fatalf("invalid typed response: %s", answer.Raw)
			}
		})
	}

}

// tcPost issues a JSON POST against a stand path.
func tcPost(t *testing.T, s *tcStand, token, path string, body map[string]any) tcChatResult {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, s.base+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s on %s: %v", path, s.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body on %s %s: %v", s.name, path, err)
	}
	return tcChatResult{Status: resp.StatusCode, Raw: data, Headers: resp.Header.Clone(), SessionID: resp.Header.Get("X-Coddy-Session-ID")}
}

// TestTwoCoddyDirectErrors checks upstream error propagation through B:
// 429 with Retry-After, 5xx and 4xx map to the structured upstream_status
// error shape on /v1/chat/completions.
func testTCDirectErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		retryAfter string
		wantStatus int
	}{
		{"upstream_429", 429, "1", http.StatusTooManyRequests},
		{"upstream_500", 500, "", http.StatusBadGateway},
		// An upstream 4xx other than 429 is passed through with its status.
		{"upstream_400", 400, "", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			up, b := startTCDirectPair(t, tcModeError, withError(c.status, map[string]string{"Retry-After": c.retryAfter}))
			_ = up
			res := tcPostChat(t, b, "b-secret", "bstub/test-model",
				[]map[string]any{{"role": "user", "content": "hi"}}, false, "")
			if res.Status != c.wantStatus {
				t.Fatalf("status %d, want %d: %s", res.Status, c.wantStatus, truncate(string(res.Raw), 1000))
			}
			if c.retryAfter != "" && res.Headers.Get("Retry-After") != c.retryAfter {
				t.Fatalf("Retry-After %q, want %q", res.Headers.Get("Retry-After"), c.retryAfter)
			}
			if !bytes.Contains(res.Raw, []byte("upstream_status")) {
				t.Fatalf("error body lacks upstream_status: %s", truncate(string(res.Raw), 1000))
			}
		})
	}
}

// TestTwoCoddyDirectUsageAndIsolation checks that usage flows through the
// direct non-streaming answer and that sessions are isolated: the session
// header pins the session id, a headerless call gets a fresh one, and the
// upstream only ever sees the messages the client sent - a provider-style
// call is stateless, clients resend their own history.
func testTCIsolation(t *testing.T) {
	up, b := startTCDirectPair(t, tcModePlain)

	res := tcPostChat(t, b, "b-secret", "bstub/test-model",
		[]map[string]any{{"role": "user", "content": "first-marker"}}, false, "")
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, truncate(string(res.Raw), 1000))
	}
	if res.Usage == nil {
		t.Fatalf("non-stream direct answer lacks usage: %s", truncate(string(res.Raw), 1000))
	}
	if tot, _ := res.Usage["total_tokens"].(float64); tot != 18 {
		t.Fatalf("usage.total_tokens %v, want 18", res.Usage["total_tokens"])
	}
	// A headerless direct call is stateless: it names no session (issue #585).
	if res.SessionID != "" {
		t.Fatalf("headerless direct call named session %s", res.SessionID)
	}

	// The session header pins the session: no other id comes back.
	const pinned = "sess_two_coddy_pinned"
	res2 := tcPostChat(t, b, "b-secret", "bstub/test-model",
		[]map[string]any{{"role": "user", "content": "second-marker"}}, false, pinned)
	if res2.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res2.Status, truncate(string(res2.Raw), 1000))
	}
	if res2.SessionID != "" && res2.SessionID != pinned {
		t.Fatalf("pinned session returned id %s, want %s", res2.SessionID, pinned)
	}

	// Headerless again, after a session exists: the upstream sees only what
	// this client sent - no leak of either earlier turn.
	res3 := tcPostChat(t, b, "b-secret", "bstub/test-model",
		[]map[string]any{{"role": "user", "content": "fresh-marker"}}, false, "")
	if res3.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res3.Status, truncate(string(res3.Raw), 1000))
	}
	if res3.SessionID != "" {
		t.Fatalf("headerless direct call named session %s", res3.SessionID)
	}
	reqs := up.all()
	fresh := reqs[len(reqs)-1]
	for _, m := range fresh.Messages {
		if m.Role == "user" && strings.Contains(m.text(), "-marker") && !strings.Contains(m.text(), "fresh-marker") {
			t.Fatalf("headerless call leaked a prior turn: %s", rawReq(fresh))
		}
	}
}

func testTCAgentErrors(t *testing.T) {
	for _, status := range []int{400, 429, 500} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("status=%d/stream=%v", status, stream), func(t *testing.T) {
				ch := startTCChain(t, false, withError(status, map[string]string{"Retry-After": "1"}))
				r := tcPostChat(t, ch.A, ch.tokenA, "agent", []map[string]any{{"role": "user", "content": "error-test"}}, stream, "")
				if len(ch.upstream.all()) == 0 {
					t.Fatal("upstream was not called")
				}
				if bytes.Contains(r.Raw, []byte("FINAL-ANSWER")) {
					t.Fatalf("false successful answer: %s", r.Raw)
				}
				wantStatus := status
				if status >= 500 {
					wantStatus = http.StatusBadGateway
				}
				var errorObject gjson.Result
				if stream {
					if r.Status != http.StatusOK {
						t.Fatalf("stream status=%d: %s", r.Status, r.Raw)
					}
					for _, event := range r.Events {
						if candidate := gjson.Get(event, "error"); candidate.Exists() {
							errorObject = candidate
						}
						for _, choice := range gjson.Get(event, "choices").Array() {
							if finish := choice.Get("finish_reason"); finish.Exists() && finish.Type != gjson.Null {
								t.Fatalf("success finish on failed stream: %s", event)
							}
						}
					}
				} else {
					if r.Status != wantStatus {
						t.Fatalf("status=%d want=%d: %s", r.Status, wantStatus, r.Raw)
					}
					errorObject = gjson.GetBytes(r.Raw, "error")
					if status == 429 && r.Headers.Get("Retry-After") != "1" {
						t.Fatalf("Retry-After lost: %v", r.Headers)
					}
				}
				if errorObject.Get("type").String() != "upstream_error" || errorObject.Get("upstream_status").Int() != int64(status) || errorObject.Get("message").String() == "" {
					t.Fatalf("wrong structured error: %s", r.Raw)
				}
			})
		}
	}
}

func testTCUsage(t *testing.T) {
	for _, known := range []bool{true, false} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("known=%v/stream=%v", known, stream), func(t *testing.T) {
				ch := startTCChain(t, false, withUsage(known))
				r := tcPostChat(t, ch.A, ch.tokenA, "agent", []map[string]any{{"role": "user", "content": "read note.txt"}}, stream, "")
				if r.Status != 200 || !strings.Contains(r.Text, ch.script.noteToken) {
					t.Fatalf("turn failed status=%d body=%s", r.Status, r.Raw)
				}
				var usage map[string]any
				if stream {
					for _, event := range r.Events {
						var c struct {
							Usage map[string]any `json:"usage"`
						}
						if json.Unmarshal([]byte(event), &c) == nil && c.Usage != nil {
							usage = c.Usage
						}
					}
				} else {
					usage = r.Usage
				}
				if known {
					if usage == nil || usage["prompt_tokens"] != float64(22) || usage["completion_tokens"] != float64(14) || usage["total_tokens"] != float64(36) {
						t.Fatalf("wrong known usage: %#v", usage)
					}
				} else if usage != nil {
					t.Fatalf("unknown usage reported as known: %#v", usage)
				}
			})
		}
	}
}

func testTCAuthValidation(t *testing.T) {
	up, b := startTCDirectPair(t, tcModePlain)
	before := len(up.all())
	body := map[string]any{"model": "bstub/test-model", "messages": []map[string]any{{"role": "user", "content": "hello"}}}
	r := tcPost(t, b, "wrong-token", "/v1/chat/completions", body)
	if r.Status != http.StatusUnauthorized {
		t.Fatalf("unauthorized status %d: %s", r.Status, r.Raw)
	}
	body["messages"] = []map[string]any{{"role": "assistant", "content": "x", "reasoning_content": 123}, {"role": "user", "content": "hello"}}
	r = tcPost(t, b, "b-secret", "/v1/chat/completions", body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("invalid reasoning status %d: %s", r.Status, r.Raw)
	}
	if len(up.all()) != before {
		t.Fatal("rejected request reached upstream")
	}
}

// Agent history belongs to A's session; B must never merge separate callers.
func testTCAgentIsolation(t *testing.T) {
	ch := startTCChain(t, false)
	const oldMarker = "isolation-old-701"
	const freshMarker = "isolation-fresh-701"
	first := tcPostChat(t, ch.A, ch.tokenA, "agent", []map[string]any{{"role": "user", "content": "read note.txt " + oldMarker}}, false, "")
	if first.Status != 200 {
		t.Fatalf("first turn: %s", first.Raw)
	}
	id := tcSessionFallback(t, ch.A, ch.tokenA, first)
	tcAssertFollowUpTurn(t, ch, false, id)
	fresh := tcPostChat(t, ch.A, ch.tokenA, "agent", []map[string]any{{"role": "user", "content": "read note.txt " + freshMarker}}, false, "")
	if fresh.Status != 200 || tcSessionFallback(t, ch.A, ch.tokenA, fresh) == id {
		t.Fatalf("fresh turn reused session or failed: %s", fresh.Raw)
	}
	seen := 0
	for _, request := range ch.upstream.all() {
		if !bytes.Contains(request.Raw, []byte(freshMarker)) || len(request.Tools) == 0 {
			continue
		}
		seen++
		if bytes.Contains(request.Raw, []byte(oldMarker)) || bytes.Contains(request.Raw, []byte("What did the file say?")) {
			t.Fatalf("A history leaked: %s", request.Raw)
		}
	}
	if seen < 2 {
		t.Fatalf("fresh agent turn did not traverse B twice: %d", seen)
	}
}

// A retries an in-band error from B, retaining the upstream pause even though
// B has already committed HTTP 200 for the SSE response.
func testTCAgentRetry(t *testing.T) {
	var mu sync.Mutex
	var attempts []time.Time
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"test-model","object":"model"}]}`))
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		attempts = append(attempts, time.Now())
		n := len(attempts)
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"retry fixture","type":"rate_limit_error"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"retry-ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"RETRY-OK"},"finish_reason":"stop"}]}`))
	}))
	defer up.Close()
	bin := tcBinary(t)
	b := startTCStand(t, bin, tcStandSpec{name: "B", provider: "bstub", apiBase: up.URL + "/v1", apiKey: "up-key", modelRef: "bstub/test-model", authToken: "b-secret"})
	b.waitReady(t)
	a := startTCStand(t, bin, tcStandSpec{name: "A", provider: "bchain", apiBase: b.base + "/v1", apiKey: "b-secret", modelRef: "bchain/bstub/test-model", modelStream: true, authToken: "a-secret", bypass: true, retryMax: 1})
	a.waitReady(t)
	result := tcPostChat(t, a, "a-secret", "agent", []map[string]any{{"role": "user", "content": "retry once"}}, false, "")
	if result.Status != http.StatusOK || result.Text != "RETRY-OK" {
		t.Fatalf("retry failed status=%d: %s", result.Status, result.Raw)
	}
	mu.Lock()
	times := append([]time.Time(nil), attempts...)
	mu.Unlock()
	if len(times) != 2 {
		t.Fatalf("attempts=%d, want exactly two", len(times))
	}
	if delay := times[1].Sub(times[0]); delay < time.Second {
		t.Fatalf("provider pause not respected: %v", delay)
	}
}
