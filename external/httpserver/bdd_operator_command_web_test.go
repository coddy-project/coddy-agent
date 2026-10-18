//go:build http

package httpserver

// Godog harness for features/web_operator_command.feature: a real server, the
// real agent runner and the real openai provider pointed at a streaming stub
// that asks for operator_command first and answers after; the scenario plays
// the web UI, reading the SSE stream and answering the event over HTTP.

import (
	"bufio"
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
	"time"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type operatorStubBackend struct {
	command  string
	mu       sync.Mutex
	requests []string
}

func (b *operatorStubBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	b.mu.Lock()
	b.requests = append(b.requests, string(raw))
	turn := len(b.requests)
	b.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	send := func(delta map[string]any, finish any) {
		data, _ := json.Marshal(map[string]any{
			"id": "chatcmpl-op", "object": "chat.completion.chunk", "model": "stub",
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	offered := false
	for _, n := range gjson.GetBytes(raw, "tools.#.function.name").Array() {
		if n.String() == "operator_command" {
			offered = true
		}
	}
	if turn == 1 && offered {
		args, _ := json.Marshal(map[string]string{"command": b.command, "reason": "run_command was refused"})
		send(map[string]any{"role": "assistant", "tool_calls": []map[string]any{{
			"index": 0, "id": "call_op_1", "type": "function",
			"function": map[string]string{"name": "operator_command", "arguments": string(args)},
		}}}, nil)
		send(map[string]any{}, "tool_calls")
	} else {
		send(map[string]any{"role": "assistant", "content": "Done."}, nil)
		send(map[string]any{}, "stop")
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func (b *operatorStubBackend) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.requests...)
}

type webOperatorState struct {
	root      string
	backend   *operatorStubBackend
	backendTS *httptest.Server
	srv       *Server
	ts        *httptest.Server
	sid       string
	events    []sseEvent
	answer    int
}

type sseEvent struct {
	name string
	data string
}

func (s *webOperatorState) close() {
	if s.ts != nil {
		s.ts.Close()
	}
	if s.srv != nil {
		s.srv.Drain()
	}
	if s.backendTS != nil {
		s.backendTS.Close()
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
	}
	*s = webOperatorState{}
}

func (s *webOperatorState) serverAsks(command string) error {
	root, err := os.MkdirTemp("", "coddy-bdd-operator-web-*")
	if err != nil {
		return err
	}
	s.root = root
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "workspace")
	for _, d := range []string{home, cwd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	s.backend = &operatorStubBackend{command: command}
	s.backendTS = httptest.NewServer(s.backend)
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: s.backendTS.URL, APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: "local/stub"}},
		Agent:     config.Agent{Model: "local/stub"},
	}
	cfg.Tools.PermissionMode = config.PermModeBypass
	log := slog.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, log, cwd, &session.FileStore{Root: filepath.Join(root, "sessions")})
	s.srv = New(cfg, mgr, log, cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	s.sid = "sess_0f1e2d3c4b5a69788796a5b4"
	return nil
}

// streamTurn posts a streaming turn and reads its events; onOperator answers
// an operator_command event while the stream is still open.
func (s *webOperatorState) streamTurn(metadata string, onOperator func(data string) error) error {
	body := `{"model":"agent","input":"run it","stream":true` + metadata + `}`
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sid)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("POST /v1/responses: %d %s", res.StatusCode, raw)
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	name := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			s.events = append(s.events, sseEvent{name: name, data: data})
			if name == "operator_command" && onOperator != nil {
				if err := onOperator(data); err != nil {
					return err
				}
			}
			if data == "[DONE]" {
				return nil
			}
		case line == "":
			name = ""
		}
	}
	return sc.Err()
}

func (s *webOperatorState) webUIStreamsDeclaring() error {
	s.events = nil
	return s.streamTurn(`,"metadata":{"surface":"webui","coddy_operator_commands":"1"}`, func(data string) error {
		rid := gjson.Get(data, "requestId").String()
		b, _ := json.Marshal(map[string]string{"requestId": rid, "action": "run"})
		res, err := http.Post(s.ts.URL+"/coddy/sessions/"+s.sid+"/operator-command", "application/json", bytes.NewReader(b))
		if err != nil {
			return err
		}
		_ = res.Body.Close()
		s.answer = res.StatusCode
		return nil
	})
}

func (s *webOperatorState) operatorAnswersRun() error {
	if s.answer != http.StatusNoContent {
		return fmt.Errorf("POST operator-command answered %d", s.answer)
	}
	return nil
}

func (s *webOperatorState) streamCarriedCommand(command string) error {
	for _, e := range s.events {
		if e.name != "operator_command" {
			continue
		}
		if gjson.Get(e.data, "command").String() != command {
			return fmt.Errorf("event command = %q", gjson.Get(e.data, "command").String())
		}
		for _, f := range []string{"shell", "host", "cwd", "requestId", "toolCallId"} {
			if gjson.Get(e.data, f).String() == "" {
				return fmt.Errorf("event lacks %s: %s", f, e.data)
			}
		}
		return nil
	}
	return fmt.Errorf("no operator_command event among %d events", len(s.events))
}

func (s *webOperatorState) streamCarriedRun() error {
	var phases []string
	for _, e := range s.events {
		p := gjson.Get(e.data, "_meta.coddy.operatorCommand.phase")
		if p.Exists() {
			phases = append(phases, p.String())
		}
	}
	if len(phases) == 0 || phases[0] != "running" || phases[len(phases)-1] != "ended" {
		return fmt.Errorf("frame phases = %v", phases)
	}
	return nil
}

func (s *webOperatorState) nextRequestCarried(output string) error {
	reqs := s.backend.snapshot()
	if len(reqs) < 2 {
		return fmt.Errorf("the model was called %d times", len(reqs))
	}
	for _, m := range gjson.Get(reqs[1], "messages").Array() {
		if m.Get("role").String() == "tool" && strings.Contains(m.Get("content").String(), output) {
			return nil
		}
	}
	return fmt.Errorf("the model's second request lacks %q: %s", output, reqs[1])
}

func (s *webOperatorState) clientStreamsWithoutDeclaring() error {
	s.events = nil
	return s.streamTurn(``, nil)
}

func (s *webOperatorState) notOffered() error {
	reqs := s.backend.snapshot()
	if len(reqs) == 0 {
		return fmt.Errorf("the model was never called")
	}
	for _, n := range gjson.Get(reqs[0], "tools.#.function.name").Array() {
		if n.String() == "operator_command" {
			return fmt.Errorf("a client that declared nothing was offered operator_command")
		}
	}
	for _, e := range s.events {
		if e.name == "operator_command" {
			return fmt.Errorf("the stream carried an operator_command event")
		}
	}
	return nil
}

func initializeWebOperatorScenario(sc *godog.ScenarioContext) {
	s := &webOperatorState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a web server whose model asks the operator to run "([^"]*)"$`, s.serverAsks)
	sc.Step(`^the web UI streams a turn that declares operator commands$`, s.webUIStreamsDeclaring)
	sc.Step(`^the operator answers the operator_command event with run$`, s.operatorAnswersRun)
	sc.Step(`^the stream carried the command "([^"]*)" with its shell, host and folder$`, s.streamCarriedCommand)
	sc.Step(`^the stream carried the run of the command until it ended$`, s.streamCarriedRun)
	sc.Step(`^the model's next request carried the output "([^"]*)"$`, s.nextRequestCarried)
	sc.Step(`^a client streams a turn without declaring operator commands$`, s.clientStreamsWithoutDeclaring)
	sc.Step(`^the model was not offered operator_command$`, s.notOffered)
}

func TestWebOperatorCommandFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "web-operator-command",
		ScenarioInitializer: initializeWebOperatorScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_operator_command.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web operator command feature suite failed")
	}
}
