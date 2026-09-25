//go:build http

package httpserver

// Godog harness for features/background_wake_web.feature: the real agent loop,
// the real task pool and the server's own waker, a scripted model (llmstub)
// that starts a failing command in the background with notify_on_finish, and
// the three things a browser reads - GET /coddy/events, the composer relay of
// the woken turn and GET .../messages - read here as a browser reads them.

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

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake/llmstub"
)

const wakeWebFailCommand = "echo 'tests failed' >&2; exit 2"

type wakeWebState struct {
	root  string
	cwd   string
	model *httptest.Server
	srv   *Server
	ts    *httptest.Server
	sid   string

	eventsCancel context.CancelFunc
	mu           sync.Mutex
	events       []sseFrame
	// relay holds the frames of the woken turn, read from the composer relay
	// as soon as the events stream announces the turn.
	relay     []sseFrame
	relayDone chan struct{}
}

func (s *wakeWebState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-wake-web-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sid = ""
	s.events, s.relay = nil, nil
	s.relayDone = nil
	return nil
}

func (s *wakeWebState) close() {
	bgtask.Default().SubscribeKeyed(bgtask.WakeWatcherKey, nil)
	if s.sid != "" {
		bgtask.Default().StopSession(s.sid)
	}
	if s.eventsCancel != nil {
		s.eventsCancel()
		s.eventsCancel = nil
	}
	if s.ts != nil {
		s.ts.CloseClientConnections()
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		bgtask.Default().SetDraining(false)
		s.srv = nil
	}
	if s.model != nil {
		s.model.Close()
		s.model = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

// startServer boots the HTTP API over the real agent loop, answering from the
// scripted model: the first turn starts the failing command in the background,
// the woken turn answers - or first runs a gated command - as the scenario asks.
func (s *wakeWebState) startServer(fixes bool) error {
	start, _ := json.Marshal(map[string]any{
		"command": wakeWebFailCommand, "background": true, "notify_on_finish": true, "expected_seconds": 1,
	})
	woken := llmstub.Rule{Match: "background task you started has finished", Answer: "The tests failed with exit 2."}
	if fixes {
		woken = llmstub.Rule{
			Match:  "background task you started has finished",
			Tool:   &llmstub.ToolCall{Name: "run_command", Arguments: json.RawMessage(`{"command":"touch fixed.txt"}`)},
			Answer: "Fixed it.",
		}
	}
	// A pause between streamed words keeps the woken turn running long enough
	// for a browser to attach to it, as a real model does.
	stub := &llmstub.Server{Delay: 150 * time.Millisecond, Rules: []llmstub.Rule{
		{Match: "start the tests", Tool: &llmstub.ToolCall{Name: "run_command", Arguments: start}, Answer: "Started the tests in the background."},
		woken,
	}}
	s.model = httptest.NewServer(stub.Handler())

	home := filepath.Join(s.root, "home")
	s.cwd = filepath.Join(s.root, "work")
	for _, dir := range []string{home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "stub", Type: "openai", APIBase: s.model.URL + "/v1", APIKey: "sk-stub"}},
		Models:    []config.ModelEntry{{Model: "stub/coddy-demo", MaxContextTokens: 131072}},
		Agent:     config.Agent{Model: "stub/coddy-demo"},
	}
	// Commands are asked about, except the one the first turn starts: the
	// woken turn's command is the prompt under test.
	cfg.Tools.PermissionMode = config.PermModeAsk
	cfg.Tools.CommandAllowlist = []string{"echo"}
	log := slog.New(slog.DiscardHandler)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	store := &session.FileStore{Root: filepath.Join(s.root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, log, s.cwd, store)
	s.srv = New(cfg, mgr, log, s.cwd)
	s.srv.AttachBackgroundWaker()
	s.ts = httptest.NewServer(s.srv.Handler())

	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return err
	}
	s.sid = res.SessionID
	return nil
}

func (s *wakeWebState) serverWithScriptedModel() error { return s.startServer(false) }

func (s *wakeWebState) serverWithFixingModel() error { return s.startServer(true) }

// browserFollowsEvents subscribes to GET /coddy/events the way the web UI
// does and, like it, attaches to the composer relay of the session the moment
// a woken turn is announced.
func (s *wakeWebState) browserFollowsEvents() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.eventsCancel = cancel
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ts.URL+"/coddy/events", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return fmt.Errorf("GET /coddy/events: %s", res.Status)
	}
	s.relayDone = make(chan struct{})
	ready := make(chan struct{})
	go func() {
		defer func() { _ = res.Body.Close() }()
		var once sync.Once
		readSSEStream(res.Body, func(f sseFrame) {
			if f.event == "ready" {
				once.Do(func() { close(ready) })
			}
			s.mu.Lock()
			s.events = append(s.events, f)
			s.mu.Unlock()
			if f.event == "background_wake" && strings.Contains(f.data, s.sid) {
				go s.readRelay()
			}
		})
	}()
	select {
	case <-ready:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("the events stream never became ready")
	}
}

// readRelay reads the woken turn from its composer relay until it ends.
func (s *wakeWebState) readRelay() {
	defer close(s.relayDone)
	req, err := http.NewRequest(http.MethodGet, s.ts.URL+"/coddy/sessions/"+s.sid+"/composer-stream", nil)
	if err != nil {
		return
	}
	req.Header.Set("X-Coddy-Session-ID", s.sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = res.Body.Close() }()
	readSSEStream(res.Body, func(f sseFrame) {
		s.mu.Lock()
		s.relay = append(s.relay, f)
		s.mu.Unlock()
	})
}

// readSSEStream calls fn with every frame of an SSE body as it arrives.
func readSSEStream(r io.Reader, fn func(sseFrame)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	var f sseFrame
	var data []string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if f.event != "" || len(data) > 0 {
				f.data = strings.Join(data, "\n")
				fn(f)
			}
			f, data = sseFrame{}, nil
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			f.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
}

func (s *wakeWebState) userAsks(text string) error {
	body, _ := json.Marshal(map[string]any{"model": "agent", "input": text, "stream": true})
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader(body))
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
	if answer := streamedText(parseSSEFrames(string(raw))); res.StatusCode != http.StatusOK || !strings.Contains(answer, "Started the tests in the background.") {
		return fmt.Errorf("the first turn answered %s %q:\n%s", res.Status, answer, raw)
	}
	return nil
}

// streamedText joins the assistant text deltas of a composer stream.
func streamedText(frames []sseFrame) string {
	var text strings.Builder
	for _, f := range frames {
		if f.event != "" || f.data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(f.data), &chunk) == nil {
			for _, c := range chunk.Choices {
				text.WriteString(c.Delta.Content)
			}
		}
	}
	return text.String()
}

type wakeTaskJSON struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Label      string `json:"label"`
	ExitCode   *int   `json:"exitCode"`
	ExitCodeDB *int   `json:"exit_code"`
}

// waitFrame waits for a frame the predicate accepts in one of the collected
// streams and returns it.
func (s *wakeWebState) waitFrame(stream func() []sseFrame, what string, ok func(sseFrame) bool) (sseFrame, error) {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, f := range stream() {
			if ok(f) {
				return f, nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sseFrame{}, fmt.Errorf("never saw %s; frames: %+v", what, stream())
}

func (s *wakeWebState) eventFrames() []sseFrame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sseFrame(nil), s.events...)
}

func (s *wakeWebState) relayFrames() []sseFrame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sseFrame(nil), s.relay...)
}

func (s *wakeWebState) eventsAnnounceWake(status string) error {
	f, err := s.waitFrame(s.eventFrames, "a background_wake event", func(f sseFrame) bool { return f.event == "background_wake" })
	if err != nil {
		return err
	}
	var payload struct {
		Object    string         `json:"object"`
		SessionID string         `json:"sessionId"`
		Tasks     []wakeTaskJSON `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(f.data), &payload); err != nil {
		return err
	}
	if payload.Object != "coddy.background_wake" || payload.SessionID != s.sid || len(payload.Tasks) != 1 || payload.Tasks[0].Status != status {
		return fmt.Errorf("background_wake event = %s", f.data)
	}
	return nil
}

// isControlFrame reports the frames a turn opens with that are not the
// transcript - the queue it opened, its clock, its counters.
func isControlFrame(f sseFrame) bool {
	switch f.event {
	case "message_queue", "turn_progress", "token_usage", "usage_update", "coddy_meta":
		return true
	}
	return false
}

func (s *wakeWebState) relayOpensWithWake(status string, code int) error {
	// The first row of the transcript must be the wake, however many control
	// frames came before it.
	first, err := s.waitFrame(s.relayFrames, "the woken turn's first transcript frame", func(f sseFrame) bool { return !isControlFrame(f) })
	if err != nil {
		return err
	}
	if first.event != "background_wake" {
		return fmt.Errorf("the woken turn's stream opens with %+v, want background_wake", first)
	}
	var u acp.BackgroundWakeUpdate
	if err := json.Unmarshal([]byte(first.data), &u); err != nil {
		return err
	}
	if len(u.Tasks) != 1 || u.Tasks[0].Status != status || u.Tasks[0].ExitCode == nil || *u.Tasks[0].ExitCode != code || u.Tasks[0].Label != wakeWebFailCommand {
		return fmt.Errorf("background_wake frame = %s", first.data)
	}
	return nil
}

func (s *wakeWebState) relayCarriesAnswer(answer string) error {
	select {
	case <-s.relayDone:
	case <-time.After(20 * time.Second):
		return fmt.Errorf("the woken turn's stream never ended; frames: %+v", s.relayFrames())
	}
	if text := streamedText(s.relayFrames()); !strings.Contains(text, answer) {
		return fmt.Errorf("the woken turn answered %q, want %q", text, answer)
	}
	return nil
}

func (s *wakeWebState) messagesKeepWake() error {
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + s.sid + "/messages")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Messages []struct {
			Role           string `json:"role"`
			Content        string `json:"content"`
			BackgroundWake *struct {
				Tasks []wakeTaskJSON `json:"tasks"`
			} `json:"background_wake"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	users := 0
	for _, m := range body.Messages {
		if m.Role != "user" {
			continue
		}
		users++
		if m.BackgroundWake == nil {
			continue
		}
		if users != 2 {
			return fmt.Errorf("the wake is user message %d, want the second", users)
		}
		t := m.BackgroundWake.Tasks
		if len(t) != 1 || t[0].Status != "failed" || t[0].ExitCodeDB == nil || *t[0].ExitCodeDB != 2 || t[0].Label != wakeWebFailCommand {
			return fmt.Errorf("background_wake = %+v", m.BackgroundWake)
		}
		return nil
	}
	return fmt.Errorf("no message carries background_wake: %+v", body.Messages)
}

// taskSaysItWokeTheAgent reads the session's task list the way the Tasks panel
// does: the failed command that started the woken turn is marked as having woken
// the agent, which keeps the bell on its card once it has ended.
func (s *wakeWebState) taskSaysItWokeTheAgent() error {
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + s.sid + "/background-tasks")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Data []struct {
			ID             string `json:"id"`
			Command        string `json:"command"`
			Status         string `json:"status"`
			NotifyOnFinish bool   `json:"notify_on_finish"`
			WokeAgent      bool   `json:"woke_agent"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	for _, t := range body.Data {
		if t.Command == wakeWebFailCommand {
			if t.Status != "failed" || !t.NotifyOnFinish || !t.WokeAgent {
				return fmt.Errorf("the task that woke the agent reads %+v", t)
			}
			return nil
		}
	}
	return fmt.Errorf("the session lists no task for %q: %+v", wakeWebFailCommand, body.Data)
}

func (s *wakeWebState) relayAsksPermission(command string) error {
	f, err := s.waitFrame(s.relayFrames, "a permission prompt", func(f sseFrame) bool { return f.event == "permission" })
	if err != nil {
		return err
	}
	if !strings.Contains(f.data, command) {
		return fmt.Errorf("permission prompt %s does not name %q", f.data, command)
	}
	return nil
}

func (s *wakeWebState) webUIAllows() error {
	var params acp.PermissionRequestParams
	for _, f := range s.relayFrames() {
		if f.event == "permission" {
			if err := json.Unmarshal([]byte(f.data), &params); err != nil {
				return err
			}
		}
	}
	body, _ := json.Marshal(map[string]string{"toolCallId": params.ToolCall.ToolCallID, "optionId": "allow"})
	res, err := http.Post(s.ts.URL+"/coddy/sessions/"+s.sid+"/permission", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("permission answer: %s %s", res.Status, raw)
	}
	return nil
}

func (s *wakeWebState) workspaceHolds(name string) error {
	if _, err := os.Stat(filepath.Join(s.cwd, name)); err != nil {
		return fmt.Errorf("the woken turn's command did not run: %w", err)
	}
	return nil
}

func initializeWakeWebScenario(sc *godog.ScenarioContext) {
	s := &wakeWebState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a coddy serve HTTP API whose agent is a scripted model$`, s.serverWithScriptedModel)
	sc.Step(`^a coddy serve HTTP API whose agent is a scripted model that fixes what woke it$`, s.serverWithFixingModel)
	sc.Step(`^a browser following the server's events$`, s.browserFollowsEvents)
	sc.Step(`^the user asks the agent to "([^"]*)" in the background$`, s.userAsks)
	sc.Step(`^the events stream announces a background wake of that session naming the task as "([^"]*)"$`, s.eventsAnnounceWake)
	sc.Step(`^the woken turn's stream opens with a background wake naming the task as "([^"]*)" with exit code (\d+)$`, s.relayOpensWithWake)
	sc.Step(`^the woken turn's stream carries the answer "([^"]*)"$`, s.relayCarriesAnswer)
	sc.Step(`^the session messages keep the woken turn's first message as a background wake naming the task$`, s.messagesKeepWake)
	sc.Step(`^the session's background tasks say the task woke the agent$`, s.taskSaysItWokeTheAgent)
	sc.Step(`^the woken turn's stream asks permission to run "([^"]*)"$`, s.relayAsksPermission)
	sc.Step(`^the web UI allows it$`, s.webUIAllows)
	sc.Step(`^the workspace holds "([^"]*)"$`, s.workspaceHolds)
}

func TestBackgroundWakeWebFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "background-wake-web",
		ScenarioInitializer: initializeWakeWebScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/background_wake_web.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("background wake web feature suite failed")
	}
}
