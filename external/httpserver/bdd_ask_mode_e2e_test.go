//go:build http

package httpserver

// Godog harness for features/ask_mode_http.feature: full turns over POST
// /v1/responses with the REAL agent runner and the REAL openai provider pointed
// at a streaming stub server that scripts one tool call and then an answer. The
// stub records every request, so the tool definitions each mode offers the
// model are observable on the wire, and the workspace shows whether the
// scripted call ran. The same stub drives agent and plan turns, which is what
// makes the suite a regression check for the modes that existed before ask.

import (
	"bytes"
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

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	askE2ENoteToken   = "ASK-MODE-E2E-NOTE"
	askE2EWriteToken  = "ASK-MODE-E2E-WRITTEN"
	askE2ERefusalText = "not available in Ask mode"
)

// askE2EReadOnlyTools is the read-only set spelled out independently of the
// agent package, so a mutating tool slipping into the allowlist fails here.
var askE2EReadOnlyTools = map[string]bool{
	"read": true, "keep_result": true, "glob": true, "grep": true, "print_tree": true,
	"websearch": true, "webfetch": true, "question": true, "load_skill": true,
}

// askModeStubBackend is an OpenAI-compatible chat completions server that
// streams: the first request of a turn gets the scripted tool call (read or
// write), the second gets the answer. Blocking requests are rejected so the
// streamed transport, the production default, is the one under test.
type askModeStubBackend struct {
	script    string // "reads" or "writes"
	readPath  string
	writePath string

	mu       sync.Mutex
	requests []string
}

func (b *askModeStubBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	b.mu.Lock()
	b.requests = append(b.requests, string(raw))
	turn := len(b.requests)
	b.mu.Unlock()

	if !gjson.GetBytes(raw, "stream").Bool() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"this stub only serves SSE","code":400}}`)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	send := func(delta map[string]any, finish any) {
		payload := map[string]any{
			"id": "chatcmpl-ask-e2e", "object": "chat.completion.chunk", "model": "stub",
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		data, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	if turn == 1 {
		name, args := b.scriptedCall()
		send(map[string]any{
			"role": "assistant",
			"tool_calls": []map[string]any{{
				"index": 0, "id": "call_ask_e2e_1", "type": "function",
				"function": map[string]string{"name": name, "arguments": args},
			}},
		}, nil)
		send(map[string]any{}, "tool_calls")
	} else {
		answer := "The note says " + askE2ENoteToken + "."
		if b.script == "writes" {
			answer = "Handled the write request."
		}
		send(map[string]any{"role": "assistant", "content": answer}, nil)
		send(map[string]any{}, "stop")
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func (b *askModeStubBackend) scriptedCall() (string, string) {
	if b.script == "writes" {
		args, _ := json.Marshal(map[string]string{"path": b.writePath, "content": askE2EWriteToken + "\n"})
		return "write", string(args)
	}
	args, _ := json.Marshal(map[string]string{"path": b.readPath})
	return "read", string(args)
}

func (b *askModeStubBackend) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.requests...)
}

type askModeE2EState struct {
	root      string
	cwd       string
	backend   *askModeStubBackend
	backendTS *httptest.Server
	srv       *Server
	ts        *httptest.Server
	sid       string
	sseBody   string
}

func (s *askModeE2EState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-ask-e2e-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sid = ""
	s.sseBody = ""
	return nil
}

func (s *askModeE2EState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.backendTS != nil {
		s.backendTS.Close()
		s.backendTS = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

// startServer boots the gateway with the REAL agent runner and the REAL provider
// factory, so the tool sets and the transport come from the production code path.
func (s *askModeE2EState) startServer(script string) error {
	home := filepath.Join(s.root, "home")
	s.cwd = filepath.Join(s.root, "workspace")
	for _, dir := range []string{home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	readPath := filepath.Join(s.cwd, "ask-e2e-note.txt")
	if err := os.WriteFile(readPath, []byte(askE2ENoteToken+"\n"), 0o644); err != nil {
		return err
	}
	s.backend = &askModeStubBackend{
		script:    script,
		readPath:  readPath,
		writePath: filepath.Join(s.cwd, "ask-e2e-written.txt"),
	}
	s.backendTS = httptest.NewServer(s.backend)

	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: s.backendTS.URL, APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: "local/stub"}},
		Agent:     config.Agent{Model: "local/stub"},
	}
	cfg.Tools.PermissionMode = config.PermModeBypass
	log := slog.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	store := &session.FileStore{Root: filepath.Join(s.root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, log, s.cwd, store)
	s.srv = New(cfg, mgr, log, s.cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *askModeE2EState) sendStreamingPrompt(mode string) error {
	s.sid = "sess_ask_e2e_" + mode
	body := fmt.Sprintf(`{"model":%q,"input":"read the note file and tell me what it says","stream":true}`, mode)
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d: %s", res.StatusCode, raw)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		return fmt.Errorf("Content-Type = %q, want an SSE stream", ct)
	}
	s.sseBody = string(raw)
	if !strings.Contains(s.sseBody, "data: [DONE]") {
		return fmt.Errorf("SSE body was not terminated: %s", s.sseBody)
	}
	return nil
}

// offeredTools returns the tool names the first request of the turn carried.
func (s *askModeE2EState) offeredTools() ([]string, error) {
	requests := s.backend.snapshot()
	if len(requests) == 0 {
		return nil, fmt.Errorf("the stub model was never called")
	}
	var names []string
	for _, n := range gjson.Get(requests[0], "tools.#.function.name").Array() {
		names = append(names, n.String())
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("the first request offered no tools: %s", requests[0])
	}
	return names, nil
}

func (s *askModeE2EState) offeredOnlyReadOnlyTools() error {
	names, err := s.offeredTools()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
		if !askE2EReadOnlyTools[n] {
			return fmt.Errorf("ask mode offered %q to the model; offered set: %v", n, names)
		}
	}
	for _, want := range []string{"read", "glob", "grep", "print_tree"} {
		if !seen[want] {
			return fmt.Errorf("ask mode did not offer %q: %v", want, names)
		}
	}
	return nil
}

func (s *askModeE2EState) offeredFullAgentToolSet() error {
	names, err := s.offeredTools()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	for _, want := range []string{"read", "write", "edit", "run_command"} {
		if !seen[want] {
			return fmt.Errorf("agent mode lost %q: %v", want, names)
		}
	}
	return nil
}

func (s *askModeE2EState) offeredPlanToolSet() error {
	names, err := s.offeredTools()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	for _, want := range []string{"read", "plan_write", "run_command"} {
		if !seen[want] {
			return fmt.Errorf("plan mode lost %q: %v", want, names)
		}
	}
	for _, forbid := range []string{"write", "edit"} {
		if seen[forbid] {
			return fmt.Errorf("plan mode now offers %q: %v", forbid, names)
		}
	}
	return nil
}

func (s *askModeE2EState) streamedAnswer() string {
	var answer strings.Builder
	for _, block := range strings.Split(s.sseBody, "\n\n") {
		for _, line := range strings.Split(block, "\n") {
			payload, ok := strings.CutPrefix(line, "data: ")
			if !ok || payload == "[DONE]" {
				continue
			}
			answer.WriteString(gjson.Get(payload, "choices.0.delta.content").String())
		}
	}
	return answer.String()
}

func (s *askModeE2EState) answerOverSSEWithFileContents() error {
	if !strings.Contains(s.sseBody, `"read"`) {
		return fmt.Errorf("no read tool call was announced on the stream: %s", s.sseBody)
	}
	if got := s.streamedAnswer(); !strings.Contains(got, askE2ENoteToken) {
		return fmt.Errorf("streamed answer %q lacks the file contents", got)
	}
	requests := s.backend.snapshot()
	if len(requests) < 2 {
		return fmt.Errorf("backend received %d requests, want the two turns of the ReAct loop", len(requests))
	}
	if !strings.Contains(requests[len(requests)-1], askE2ENoteToken) {
		return fmt.Errorf("the read result never reached the model: %s", requests[len(requests)-1])
	}
	return nil
}

func (s *askModeE2EState) transcriptReportsMode(mode string) error {
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + s.sid + "/messages")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("messages status %d: %s", res.StatusCode, raw)
	}
	if got := gjson.GetBytes(raw, "mode").String(); got != mode {
		return fmt.Errorf("transcript mode = %q, want %q", got, mode)
	}
	if n := gjson.GetBytes(raw, "messages.#").Int(); n == 0 {
		return fmt.Errorf("transcript is empty: %s", raw)
	}
	return nil
}

func (s *askModeE2EState) fileNotWritten() error {
	if _, err := os.Stat(s.backend.writePath); err == nil {
		return fmt.Errorf("%s exists: the write ran in ask mode", s.backend.writePath)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *askModeE2EState) fileWritten() error {
	body, err := os.ReadFile(s.backend.writePath)
	if err != nil {
		return fmt.Errorf("the write did not happen: %v", err)
	}
	if !strings.Contains(string(body), askE2EWriteToken) {
		return fmt.Errorf("unexpected file body %q", body)
	}
	return nil
}

func (s *askModeE2EState) streamCarriesCancelledRefusal() error {
	if !strings.Contains(s.sseBody, "event: tool_call_update") {
		return fmt.Errorf("no tool_call_update on the stream: %s", s.sseBody)
	}
	if !strings.Contains(s.sseBody, `"status":"cancelled"`) {
		return fmt.Errorf("no cancelled tool call on the stream: %s", s.sseBody)
	}
	if !strings.Contains(s.sseBody, askE2ERefusalText) {
		return fmt.Errorf("the refusal text never reached the client: %s", s.sseBody)
	}
	return nil
}

func (s *askModeE2EState) modelRepromptedWithRefusal() error {
	requests := s.backend.snapshot()
	if len(requests) < 2 {
		return fmt.Errorf("backend received %d requests, want a re-prompt after the refusal", len(requests))
	}
	last := requests[len(requests)-1]
	for _, m := range gjson.Get(last, "messages").Array() {
		if m.Get("role").String() == "tool" && strings.Contains(m.Get("content").String(), askE2ERefusalText) {
			return nil
		}
	}
	return fmt.Errorf("the refusal was not replayed to the model as a tool result: %s", last)
}

func initializeAskModeE2EScenario(sc *godog.ScenarioContext) {
	s := &askModeE2EState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy gateway backed by a streaming stub model that (reads|writes) a file, then answers$`, s.startServer)
	sc.Step(`^a client sends a streaming "([^"]+)" prompt over POST /v1/responses$`, s.sendStreamingPrompt)
	sc.Step(`^the model was offered only read-only tools$`, s.offeredOnlyReadOnlyTools)
	sc.Step(`^the model was offered the full agent tool set$`, s.offeredFullAgentToolSet)
	sc.Step(`^the model was offered the plan tool set$`, s.offeredPlanToolSet)
	sc.Step(`^the client received the answer over SSE with the file contents$`, s.answerOverSSEWithFileContents)
	sc.Step(`^the transcript reports the session in "([^"]+)" mode$`, s.transcriptReportsMode)
	sc.Step(`^the workspace file was not written$`, s.fileNotWritten)
	sc.Step(`^the workspace file was written$`, s.fileWritten)
	sc.Step(`^the stream carries a cancelled tool call with the ask-mode refusal$`, s.streamCarriesCancelledRefusal)
	sc.Step(`^the model was re-prompted with that refusal as the tool result$`, s.modelRepromptedWithRefusal)
}

func TestAskModeHTTPE2E(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "ask-mode-http",
		ScenarioInitializer: initializeAskModeE2EScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/ask_mode_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("ask mode HTTP feature suite failed")
	}
}
