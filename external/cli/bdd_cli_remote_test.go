//go:build cli

package cli

// Godog harness for features/cli_remote.feature: the console (interactive and
// one-shot print) runs against a fake remote coddy serve server that speaks
// the documented SSE and REST contract, bearer auth included.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
)

const bddRemoteToken = "bdd-remote-token"

// fakeRemoteServer implements just enough of the coddy serve HTTP contract for the
// console: model catalog, streaming responses, command catalogs.
type fakeRemoteServer struct {
	ts     *httptest.Server
	answer string

	// events feeds GET /coddy/events after its ready frame.
	events chan string

	mu        sync.Mutex
	turns     []fakeRemoteTurn
	authFails int
	// permissions records what was posted to POST /coddy/sessions/{id}/permission.
	permissions []fakeRemotePermission

	// task is the one background task the server reports for every session, with
	// the output it printed; stops records the ids the console asked to stop.
	task       *bgtask.Snapshot
	taskOutput string
	stops      []string

	// progressTokens, when above zero, makes a turn report that many generated
	// tokens and then wait for holdTurn before it answers.
	progressTokens int
	holdTurn       chan struct{}

	// woken holds the frames of the turn the server woke on its own, served
	// on the composer relay of the session it woke.
	woken string
}

type fakeRemotePermission struct {
	sessionID  string
	toolCallID string
	optionID   string
}

type fakeRemoteTurn struct {
	sessionID string
	model     string
	input     string
}

func newFakeRemoteServer(answer string) *fakeRemoteServer {
	f := &fakeRemoteServer{answer: answer, events: make(chan string, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /coddy/events", f.withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
		if fl != nil {
			fl.Flush()
		}
		for {
			select {
			case frame := <-f.events:
				if frame == "" {
					return // Disconnect; the next connection replays an empty snapshot.
				}
				_, _ = fmt.Fprint(w, frame)
				if fl != nil {
					fl.Flush()
				}
			case <-r.Context().Done():
				return
			}
		}
	}))
	mux.HandleFunc("POST /coddy/sessions/{id}/permission", f.withAuth(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ToolCallID string `json:"toolCallId"`
			OptionID   string `json:"optionId"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in)
		f.mu.Lock()
		f.permissions = append(f.permissions, fakeRemotePermission{sessionID: r.PathValue("id"), toolCallID: in.ToolCallID, optionID: in.OptionID})
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	// The composer relay of a session: the woken turn's frames, then the end.
	mux.HandleFunc("GET /coddy/sessions/{id}/composer-stream", f.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		frames := f.woken
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, frames)
	}))
	mux.HandleFunc("GET /v1/models", f.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","default_agent_model":"remote/deep-1","data":[
			{"id":"agent","object":"model","owned_by":"coddy"},
			{"id":"plan","object":"model","owned_by":"coddy"},
			{"id":"remote/deep-1","object":"model","owned_by":"neuraldeep"},
			{"id":"remote/deep-2","object":"model","owned_by":"neuraldeep"}]}`))
	}))
	mux.HandleFunc("POST /v1/responses", f.withAuth(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		_ = json.Unmarshal(body, &req)
		f.mu.Lock()
		f.turns = append(f.turns, fakeRemoteTurn{
			sessionID: r.Header.Get("X-Coddy-Session-ID"),
			model:     req.Model,
			input:     req.Input,
		})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if f.progressTokens > 0 {
			// What the agent loop publishes while a call streams: the turn's clock
			// and the tokens generated so far.
			_, _ = fmt.Fprintf(w, "event: turn_progress\ndata: {\"sessionUpdate\":\"turn_progress\",\"startedAt\":%q,\"elapsedMs\":2000,\"outputTokens\":%d,\"estimated\":true}\n\n",
				time.Now().Add(-2*time.Second).UTC().Format(time.RFC3339Nano), f.progressTokens)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			select {
			case <-f.holdTurn:
			case <-r.Context().Done():
				return
			}
		}
		chunk, _ := json.Marshal(map[string]interface{}{
			"object":  "chat.completion.chunk",
			"choices": []map[string]interface{}{{"delta": map[string]string{"content": f.answer}}},
		})
		_, _ = fmt.Fprintf(w, "event: token_usage\ndata: {\"sessionUpdate\":\"token_usage\",\"inputTokens\":7,\"outputTokens\":3,\"totalTokens\":10}\n\n")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		_, _ = fmt.Fprintf(w, "event: coddy_meta\ndata: {\"metadata\":{\"model\":\"remote/deep-1\",\"api_model\":\"deep-1\"}}\n\n")
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	// The three background-task routes of external/httpserver/background_http.go.
	taskAnswer := func(w http.ResponseWriter) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": "coddy.background_task", "task": f.task, "output": f.taskOutput})
	}
	mux.HandleFunc("GET /coddy/sessions/{id}/background-tasks", f.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		rows := []bgtask.Snapshot{}
		if f.task != nil {
			rows = append(rows, *f.task)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": "coddy.background_task_list", "data": rows})
	}))
	mux.HandleFunc("GET /coddy/sessions/{id}/background-tasks/{task}", f.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		taskAnswer(w)
	}))
	mux.HandleFunc("POST /coddy/sessions/{id}/background-tasks/{task}/stop", f.withAuth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.stops = append(f.stops, r.PathValue("task"))
		if f.task != nil {
			now := time.Now()
			f.task.Status, f.task.FinishedAt = bgtask.StatusStopped, &now
		}
		f.mu.Unlock()
		taskAnswer(w)
	}))
	mux.HandleFunc("GET /coddy/commands", f.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"coddy.commands","items":[{"name":"compact","description":"Summarize the conversation"}]}`))
	}))
	mux.HandleFunc("GET /coddy/slash-commands", f.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"coddy.slash_commands_page","items":[],"total":0,"has_more":false,"page":1,"page_size":200}`))
	}))
	f.ts = httptest.NewServer(mux)
	return f
}

func (f *fakeRemoteServer) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+bddRemoteToken {
			f.mu.Lock()
			f.authFails++
			f.mu.Unlock()
			w.Header().Set("WWW-Authenticate", `Bearer realm="coddy"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (f *fakeRemoteServer) lastTurn() *fakeRemoteTurn {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.turns) == 0 {
		return nil
	}
	t := f.turns[len(f.turns)-1]
	return &t
}

// cliRemoteState drives the console against the fake remote server.
type cliRemoteState struct {
	server *fakeRemoteServer
	app    *App

	runCtx    context.Context
	runCancel context.CancelFunc
	appDone   chan error

	printOut  *syncBuffer
	printDone chan error
}

func (s *cliRemoteState) reset() {
	s.server = nil
	s.app = nil
	s.appDone = nil
	s.printOut = nil
	s.printDone = nil
}

func (s *cliRemoteState) shutdown() {
	if s.runCancel != nil {
		s.runCancel()
	}
	if s.app != nil {
		closeConsole(s.app, s.appDone != nil)
		// The console's events subscription holds a request open; the real
		// console closes its backend on exit (runInteractive), so the harness
		// does too before the server waits for its requests to finish.
		if c, ok := s.app.mgr.(interface{ Close() }); ok {
			c.Close()
		}
	}
	if s.server != nil {
		s.server.ts.CloseClientConnections()
		s.server.ts.Close()
	}
}

func (s *cliRemoteState) fakeServerAnswers(text string) error {
	s.server = newFakeRemoteServer(text)
	return nil
}

func (s *cliRemoteState) consoleConnectedToRemote() error {
	if s.server == nil {
		return fmt.Errorf("no fake server")
	}
	// The local config deliberately has no models: everything model-shaped
	// must come from the remote catalog.
	cfg := &config.Config{}
	cfg.Paths.CWD = "."
	term := &bddTerminal{cols: 100, rows: 35}
	app, err := buildRemoteApp(cfg, &remote.Options{
		BaseURL: s.server.ts.URL,
		Token:   bddRemoteToken,
		Log:     slog.New(slog.DiscardHandler),
	}, slog.New(slog.DiscardHandler), term, "dark", true)
	if err != nil {
		return err
	}
	s.app = app
	return nil
}

func (s *cliRemoteState) consoleStarts() error {
	if s.app == nil {
		return fmt.Errorf("no app")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.runCtx, s.runCancel = ctx, cancel
	if err := s.app.Start(ctx, "", false); err != nil {
		cancel()
		return err
	}
	s.appDone = make(chan error, 1)
	go func() { s.appDone <- s.app.Run(ctx) }()
	return s.waitScreen("coddy v", 3*time.Second)
}

func (s *cliRemoteState) screenText() string {
	var b strings.Builder
	for _, line := range s.app.screen.Snapshot() {
		b.WriteString(tui.StripTerminalSequences(line))
		b.WriteString("\n")
	}
	return b.String()
}

func (s *cliRemoteState) waitScreen(needle string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(s.screenText(), needle) {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("screen never showed %q; last frame:\n%s", needle, s.screenText())
}

func (s *cliRemoteState) screenShowsRemoteBanner() error {
	return s.waitScreen("remote: "+s.server.ts.URL, 2*time.Second)
}

func (s *cliRemoteState) footerNamesRemoteModel() error {
	// The footer renders the selector as "(provider) model".
	return s.waitScreen("(remote) deep-1", 2*time.Second)
}

func (s *cliRemoteState) remoteSessionHasRunningTask(command, output string) error {
	s.server.mu.Lock()
	defer s.server.mu.Unlock()
	s.server.task = &bgtask.Snapshot{ID: "bg_1", SessionID: "remote", Kind: bgtask.KindCommand, Label: command,
		Command: command, Status: bgtask.StatusRunning, StartedAt: time.Now().Add(-time.Minute)}
	s.server.taskOutput = output
	return nil
}

func (s *cliRemoteState) overlayListsRemoteTaskRunning(title string) error {
	if err := s.waitScreen(title, 3*time.Second); err != nil {
		return err
	}
	return s.waitScreen("1 running", 3*time.Second)
}

func (s *cliRemoteState) operatorOpensSelectedTask() error {
	s.app.OnTerminalInput([]byte("\r"))
	return nil
}

func (s *cliRemoteState) overlayShowsRemoteOutput(text string) error {
	return s.waitScreen(text, 3*time.Second)
}

func (s *cliRemoteState) operatorStopsTaskFromOverlay() error {
	s.app.OnTerminalInput([]byte("s"))
	return nil
}

func (s *cliRemoteState) serverAskedToStopTask() error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.server.mu.Lock()
		stops := append([]string(nil), s.server.stops...)
		s.server.mu.Unlock()
		if len(stops) == 1 && stops[0] == "bg_1" {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("the server was never asked to stop bg_1")
}

func (s *cliRemoteState) overlayListsRemoteTaskStopped() error {
	return s.waitScreen("stopped", 3*time.Second)
}

func (s *cliRemoteState) fakeServerHoldsTurnAfterProgress(tokens int) error {
	s.server = newFakeRemoteServer("done remotely")
	s.server.progressTokens = tokens
	s.server.holdTurn = make(chan struct{})
	return nil
}

func (s *cliRemoteState) consoleStatusLineShows(text string) error {
	return s.waitScreen(text, 3*time.Second)
}

func (s *cliRemoteState) fakeServerLetsTurnEnd() error {
	close(s.server.holdTurn)
	return nil
}

func (s *cliRemoteState) operatorSubmits(text string) error {
	for _, r := range text {
		s.app.OnTerminalInput([]byte(string(r)))
	}
	s.app.OnTerminalInput([]byte("\r"))
	return nil
}

func (s *cliRemoteState) transcriptShowsAssistant(text string) error {
	return s.waitScreen(text, 3*time.Second)
}

func (s *cliRemoteState) fakeServerReceivedTurn() error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if turn := s.server.lastTurn(); turn != nil {
			if turn.sessionID == "" || turn.sessionID != s.app.sessionID {
				return fmt.Errorf("turn session %q does not match the console session %q", turn.sessionID, s.app.sessionID)
			}
			if turn.model != "agent" {
				return fmt.Errorf("turn model %q, want the agent profile", turn.model)
			}
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("the fake server never received a turn")
}

const remoteWriterChild = "sess_remote_writer"

// serverWakesConsoleSession plays what coddy serve does when a task of the
// console's session ends: the woken turn runs on the relay, and the events
// stream says so.
func (s *cliRemoteState) serverWakesConsoleSession(label string, code int) error {
	task := map[string]interface{}{"id": "bg_1", "kind": "command", "label": label, "status": "failed", "exitCode": code, "durationMs": 90000}
	wake, err := json.Marshal(map[string]interface{}{"sessionUpdate": "background_wake", "tasks": []interface{}{task}})
	if err != nil {
		return err
	}
	chunk, err := json.Marshal(map[string]interface{}{
		"object":  "chat.completion.chunk",
		"choices": []map[string]interface{}{{"delta": map[string]string{"content": fmt.Sprintf("The build failed with exit %d.", code)}}},
	})
	if err != nil {
		return err
	}
	s.server.mu.Lock()
	s.server.woken = fmt.Sprintf("id: 1\nevent: background_wake\ndata: %s\n\nid: 2\ndata: %s\n\nid: 3\ndata: [DONE]\n\n", wake, chunk)
	s.server.mu.Unlock()
	event, err := json.Marshal(map[string]interface{}{
		"object": "coddy.background_wake", "sessionId": s.app.sessionID, "phase": "woken",
		"at": time.Now().UTC().Format(time.RFC3339Nano), "tasks": []interface{}{task},
	})
	if err != nil {
		return err
	}
	s.server.events <- "event: background_wake\ndata: " + string(event) + "\n\n"
	return nil
}

// wokenTurnShowsNothingBeforeTheAnswer checks that the turn the server woke
// reads as the agent carrying on: the frame that opens it leaves no row.
func (s *cliRemoteState) wokenTurnShowsNothingBeforeTheAnswer() error {
	text := s.screenText()
	for _, unwanted := range []string{"Woken by", "bg_1"} {
		if strings.Contains(text, unwanted) {
			return fmt.Errorf("the woken turn shows %q:\n%s", unwanted, text)
		}
	}
	return nil
}

// serverAnnouncesBackgroundSubagent pushes the frame the server sends once a
// background subagent of the console's session waits for a permission.
func (s *cliRemoteState) serverAnnouncesBackgroundSubagent(name, command string) error {
	body, err := json.Marshal(map[string]interface{}{
		"object":          "coddy.subagent_permission",
		"phase":           "asked",
		"parentSessionId": s.app.sessionID,
		"childSessionId":  remoteWriterChild,
		"taskId":          "bg_1",
		"toolCallId":      "call_remote_writer",
		"agentName":       name,
		"request": map[string]interface{}{
			"sessionId": remoteWriterChild,
			"toolCall": map[string]interface{}{
				"toolCallId": "call_remote_writer",
				"title":      "[subagent " + name + "] Run: " + command,
				"status":     "pending",
			},
			"options": []map[string]string{
				{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
				{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
			},
		},
	})
	if err != nil {
		return err
	}
	s.server.events <- "event: subagent_permission\ndata: " + string(body) + "\n\n"
	return nil
}

func (s *cliRemoteState) screenShowsModalNamingSubagent(name string) error {
	if err := s.waitScreen("Permission required", 5*time.Second); err != nil {
		return err
	}
	return s.waitScreen("[subagent "+name+"]", 2*time.Second)
}

func (s *cliRemoteState) operatorConfirmsHighlighted() error {
	s.app.OnTerminalInput([]byte("\r"))
	return nil
}

func (s *cliRemoteState) reconnectAfterPermissionSettled() error {
	s.server.events <- ""
	return nil
}

func (s *cliRemoteState) obsoletePermissionCloses() error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !strings.Contains(s.screenText(), "Permission required") {
			s.server.mu.Lock()
			defer s.server.mu.Unlock()
			if len(s.server.permissions) != 0 {
				return fmt.Errorf("the console answered an obsolete permission")
			}
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("the obsolete permission stayed open after reconnect")
}

func (s *cliRemoteState) serverReceivesChildAnswer(option string) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.server.mu.Lock()
		posted := append([]fakeRemotePermission(nil), s.server.permissions...)
		s.server.mu.Unlock()
		for _, p := range posted {
			if p.sessionID != remoteWriterChild || p.toolCallID != "call_remote_writer" {
				return fmt.Errorf("the answer went to %+v, want the child session %s", p, remoteWriterChild)
			}
			if p.optionID != option {
				return fmt.Errorf("the server received %q, want %q", p.optionID, option)
			}
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("the server never received an answer; last frame:\n%s", s.screenText())
}

func (s *cliRemoteState) operatorRunsRemoteOneShot(prompt string) error {
	if s.server == nil {
		return fmt.Errorf("no fake server")
	}
	h, err := remote.NewHandler(remote.Options{
		BaseURL: s.server.ts.URL,
		Token:   bddRemoteToken,
		Log:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		return err
	}
	cfg := &config.Config{}
	s.printOut = &syncBuffer{}
	s.printDone = make(chan error, 1)
	h.SetServer(&printSender{mgr: h, cfg: cfg, out: s.printOut, errOut: &syncBuffer{}})
	go func() {
		s.printDone <- PrintPrompt(context.Background(), h, PrintOptions{
			Prompt: prompt,
			Out:    s.printOut,
			ErrOut: &syncBuffer{},
			Config: cfg,
		})
	}()
	return nil
}

func (s *cliRemoteState) oneShotOutputContains(text string) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.printOut.String(), text) {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("one-shot output %q never contained %q", s.printOut.String(), text)
}

func (s *cliRemoteState) oneShotEndsCleanly() error {
	select {
	case err := <-s.printDone:
		return err
	case <-time.After(3 * time.Second):
		return fmt.Errorf("one-shot run did not finish")
	}
}

func initializeCLIRemoteScenario(sc *godog.ScenarioContext) {
	s := &cliRemoteState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.shutdown()
		return ctx, nil
	})
	sc.Step(`^a fake remote coddy server that answers "([^"]*)"$`, s.fakeServerAnswers)
	sc.Step(`^a console app connected to that remote server$`, s.consoleConnectedToRemote)
	sc.Step(`^the console app starts$`, s.consoleStarts)
	sc.Step(`^the screen shows the remote server banner$`, s.screenShowsRemoteBanner)
	sc.Step(`^the footer names the remote default model$`, s.footerNamesRemoteModel)
	sc.Step(`^the operator submits "([^"]*)"$`, s.operatorSubmits)
	sc.Step(`^the remote session has a running background task "([^"]*)" that printed "([^"]*)"$`, s.remoteSessionHasRunningTask)
	sc.Step(`^the tasks overlay lists the remote task "([^"]*)" as running$`, s.overlayListsRemoteTaskRunning)
	sc.Step(`^the operator opens the selected task$`, s.operatorOpensSelectedTask)
	sc.Step(`^the tasks overlay shows the remote output "([^"]*)"$`, s.overlayShowsRemoteOutput)
	sc.Step(`^the operator stops the task from the overlay$`, s.operatorStopsTaskFromOverlay)
	sc.Step(`^the server is asked to stop that task$`, s.serverAskedToStopTask)
	sc.Step(`^the tasks overlay lists the remote task as stopped$`, s.overlayListsRemoteTaskStopped)
	sc.Step(`^a fake remote coddy server that holds its turn after reporting (\d+) generated tokens$`, s.fakeServerHoldsTurnAfterProgress)
	sc.Step(`^the console status line shows "([^"]*)"$`, s.consoleStatusLineShows)
	sc.Step(`^the fake server lets the turn end$`, s.fakeServerLetsTurnEnd)
	sc.Step(`^the transcript shows the assistant text "([^"]*)"$`, s.transcriptShowsAssistant)
	sc.Step(`^the fake server received a turn for the console session$`, s.fakeServerReceivedTurn)
	sc.Step(`^the server announces that the background subagent "([^"]*)" of the console session asks to run "([^"]*)"$`, s.serverAnnouncesBackgroundSubagent)
	sc.Step(`^the screen shows a permission modal naming the subagent "([^"]*)"$`, s.screenShowsModalNamingSubagent)
	sc.Step(`^the operator confirms the highlighted option$`, s.operatorConfirmsHighlighted)
	sc.Step(`^the connection drops and the permission is settled elsewhere before reconnect$`, s.reconnectAfterPermissionSettled)
	sc.Step(`^the obsolete permission modal closes without posting an answer$`, s.obsoletePermissionCloses)
	sc.Step(`^the server receives the answer "([^"]*)" for that subagent's child session$`, s.serverReceivesChildAnswer)
	sc.Step(`^the server wakes the console session because "([^"]*)" failed with exit (\d+)$`, s.serverWakesConsoleSession)
	sc.Step(`^the woken turn shows nothing before the agent's answer$`, s.wokenTurnShowsNothingBeforeTheAnswer)
	sc.Step(`^the operator runs a remote one-shot prompt "([^"]*)"$`, s.operatorRunsRemoteOneShot)
	sc.Step(`^the one-shot output contains "([^"]*)"$`, s.oneShotOutputContains)
	sc.Step(`^the one-shot run ends cleanly$`, s.oneShotEndsCleanly)
}

func TestCLIRemoteFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "cli-remote",
		ScenarioInitializer: initializeCLIRemoteScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/cli_remote.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cli remote feature suite failed")
	}
}
