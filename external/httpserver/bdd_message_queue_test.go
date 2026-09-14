//go:build http

package httpserver

// Godog harness for features/message_queue_http.feature: drives the queue over
// the real HTTP surface against a stub runner that parks a turn, so "a turn is
// running" is a fact of the server rather than a sleep.

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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const queueFeatureReply = "the parked answer"

type queueHTTPState struct {
	root      string
	ts        *httptest.Server
	srv       *Server
	sessionID string

	turnStarted chan struct{}
	releaseTurn chan struct{}
	turnDone    chan struct{}
	releaseOnce sync.Once

	// last is the decoded body of the most recent queue call.
	lastStatus int
	lastBody   map[string]interface{}
	lastRaw    string

	watchBody  *bufio.Reader
	closeWatch func()

	// eventsBody is a client that holds GET /coddy/events without reading any
	// turn's stream - the shape of a second browser tab, or a console attached
	// over --remote, that is merely looking at the session.
	eventsBody  *bufio.Reader
	closeEvents func()
}

func (s *queueHTTPState) reset() {
	s.close()
	s.root, _ = os.MkdirTemp("", "coddy-queue-http-*")
	s.turnStarted = make(chan struct{})
	s.releaseTurn = make(chan struct{})
	s.turnDone = make(chan struct{})
	s.releaseOnce = sync.Once{}
	s.lastStatus = 0
	s.lastBody = nil
	s.lastRaw = ""
}

func (s *queueHTTPState) close() {
	s.release()
	if s.turnDone != nil {
		select {
		case <-s.turnDone:
		case <-time.After(5 * time.Second):
		}
	}
	if s.closeWatch != nil {
		s.closeWatch()
		s.closeWatch = nil
		s.watchBody = nil
	}
	if s.closeEvents != nil {
		s.closeEvents()
		s.closeEvents = nil
		s.eventsBody = nil
	}
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *queueHTTPState) release() {
	if s.releaseTurn == nil {
		return
	}
	s.releaseOnce.Do(func() { close(s.releaseTurn) })
}

func (s *queueHTTPState) startServer() error {
	home := filepath.Join(s.root, "home")
	sessRoot := filepath.Join(s.root, "sessions")
	for _, d := range []string{home, sessRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	var started sync.Once
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		started.Do(func() { close(s.turnStarted) })
		<-s.releaseTurn
		_ = sender.SendSessionUpdate(st.GetID(), acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: queueFeatureReply},
		})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: queueFeatureReply})
		// The stub is not the ReAct loop, so it reads the queue itself: without
		// that the manager's boundary drain would run it again forever.
		st.TakeQueuedMessages()
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: s.root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.root, &session.FileStore{Root: sessRoot})
	s.srv = New(cfg, mgr, slog.Default(), s.root)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *queueHTTPState) haveSession() error {
	sn, err := s.srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
	if err != nil {
		return err
	}
	s.sessionID = sn.SessionID
	return nil
}

// turnIsRunning starts a turn the stub runner parks, and returns once the
// server is genuinely inside it.
func (s *queueHTTPState) turnIsRunning() error {
	go func() {
		defer close(s.turnDone)
		req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses",
			strings.NewReader(`{"model":"agent","input":"go","stream":false}`))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", s.sessionID)
		res, err := s.ts.Client().Do(req)
		if err != nil {
			return
		}
		defer res.Body.Close()
		_, _ = io.ReadAll(res.Body)
	}()
	select {
	case <-s.turnStarted:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("the turn never started")
	}
}

func (s *queueHTTPState) queueURL(suffix string) string {
	return s.ts.URL + "/coddy/sessions/" + url.PathEscape(s.sessionID) + "/queue" + suffix
}

func (s *queueHTTPState) call(method, suffix string, body []byte) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, s.queueURL(suffix), rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := s.ts.Client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	s.lastStatus = res.StatusCode
	s.lastRaw = string(raw)
	s.lastBody = map[string]interface{}{}
	_ = json.Unmarshal(raw, &s.lastBody)
	return nil
}

func (s *queueHTTPState) postsToQueue(text string) error {
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	return s.call(http.MethodPost, "", body)
}

// queuedMessages reads the messages array out of the last answer.
func (s *queueHTTPState) queuedMessages() []map[string]interface{} {
	raw, _ := s.lastBody["messages"].([]interface{})
	out := make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func (s *queueHTTPState) queueAnsweredWithMessage(text string) error {
	if s.lastStatus != http.StatusCreated {
		return fmt.Errorf("queue answered %d: %s", s.lastStatus, s.lastRaw)
	}
	msg, _ := s.lastBody["message"].(map[string]interface{})
	if msg == nil {
		return fmt.Errorf("the answer carries no message: %s", s.lastRaw)
	}
	if got, _ := msg["text"].(string); got != text {
		return fmt.Errorf("queued text = %q, want %q", got, text)
	}
	if id, _ := msg["id"].(string); strings.TrimSpace(id) == "" {
		return fmt.Errorf("the queued message has no identifier: %s", s.lastRaw)
	}
	return nil
}

func (s *queueHTTPState) readingQueueListsOne(text string) error {
	if err := s.call(http.MethodGet, "", nil); err != nil {
		return err
	}
	if s.lastStatus != http.StatusOK {
		return fmt.Errorf("reading the queue answered %d: %s", s.lastStatus, s.lastRaw)
	}
	msgs := s.queuedMessages()
	if len(msgs) != 1 {
		return fmt.Errorf("the queue lists %d messages, want 1: %s", len(msgs), s.lastRaw)
	}
	if got, _ := msgs[0]["text"].(string); got != text {
		return fmt.Errorf("the queue lists %q, want %q", got, text)
	}
	return nil
}

func (s *queueHTTPState) deletesQueuedMessage() error {
	msg, _ := s.lastBody["message"].(map[string]interface{})
	id, _ := msg["id"].(string)
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("no queued message to delete: %s", s.lastRaw)
	}
	return s.call(http.MethodDelete, "/"+url.PathEscape(id), nil)
}

func (s *queueHTTPState) queueIsEmpty() error {
	if s.lastStatus != http.StatusOK {
		return fmt.Errorf("the delete answered %d: %s", s.lastStatus, s.lastRaw)
	}
	if msgs := s.queuedMessages(); len(msgs) != 0 {
		return fmt.Errorf("the queue still holds %d messages: %s", len(msgs), s.lastRaw)
	}
	return nil
}

func (s *queueHTTPState) queueRefusedNoTurn() error {
	if s.lastStatus != http.StatusConflict {
		return fmt.Errorf("the queue answered %d, want 409: %s", s.lastStatus, s.lastRaw)
	}
	errObj, _ := s.lastBody["error"].(map[string]interface{})
	if code, _ := errObj["code"].(string); code != "no_active_turn" {
		return fmt.Errorf("refusal code = %q, want %q: %s", code, "no_active_turn", s.lastRaw)
	}
	return nil
}

func (s *queueHTTPState) readingQueueListsNothing() error {
	if err := s.call(http.MethodGet, "", nil); err != nil {
		return err
	}
	if s.lastStatus != http.StatusOK {
		return fmt.Errorf("reading the queue answered %d: %s", s.lastStatus, s.lastRaw)
	}
	if msgs := s.queuedMessages(); len(msgs) != 0 {
		return fmt.Errorf("the queue lists %d messages on an idle session", len(msgs))
	}
	return nil
}

// watchTurn attaches a second client to the composer stream of the turn that is
// already running, and keeps the body open so the scenario can read the frames
// the queue publishes into it.
func (s *queueHTTPState) watchTurn() error {
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.ts.URL+"/coddy/sessions/"+url.PathEscape(s.sessionID)+"/composer-stream", nil)
	if err != nil {
		cancel()
		return err
	}
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := s.ts.Client().Do(req)
	if err != nil {
		cancel()
		return err
	}
	if res.StatusCode != http.StatusOK {
		cancel()
		res.Body.Close()
		return fmt.Errorf("composer stream answered %d", res.StatusCode)
	}
	s.watchBody = bufio.NewReader(res.Body)
	s.closeWatch = func() {
		cancel()
		res.Body.Close()
	}
	return nil
}

func (s *queueHTTPState) watcherToldQueueHolds(text string) error {
	deadline := time.Now().Add(10 * time.Second)
	var seen strings.Builder
	if s.watchBody == nil {
		return fmt.Errorf("no client is watching the turn")
	}
	for time.Now().Before(deadline) {
		line, err := s.watchBody.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read composer stream (seen %q): %w", seen.String(), err)
		}
		seen.WriteString(line)
		if strings.Contains(seen.String(), "event: message_queue") && strings.Contains(seen.String(), text) {
			return nil
		}
	}
	return fmt.Errorf("the watching client was never told about the queue, seen %q", seen.String())
}

// subscribeEvents attaches a client to the server-wide event stream and drains
// the connect-time snapshot, so a later read sees only new frames.
func (s *queueHTTPState) subscribeEvents() error {
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ts.URL+"/coddy/events", nil)
	if err != nil {
		cancel()
		return err
	}
	res, err := s.ts.Client().Do(req)
	if err != nil {
		cancel()
		return err
	}
	if res.StatusCode != http.StatusOK {
		cancel()
		res.Body.Close()
		return fmt.Errorf("events stream answered %d", res.StatusCode)
	}
	s.eventsBody = bufio.NewReader(res.Body)
	s.closeEvents = func() {
		cancel()
		res.Body.Close()
	}
	return s.awaitEvents("event: ready", "")
}

// awaitEvents reads whole frames until one contains want (and, when body is not
// empty, that too).
func (s *queueHTTPState) awaitEvents(want, body string) error {
	if s.eventsBody == nil {
		return fmt.Errorf("no client is subscribed to the event stream")
	}
	deadline := time.Now().Add(10 * time.Second)
	var frame strings.Builder
	for time.Now().Before(deadline) {
		line, err := s.eventsBody.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read events (seen %q): %w", frame.String(), err)
		}
		frame.WriteString(line)
		if !strings.HasSuffix(frame.String(), "\n\n") {
			continue
		}
		got := frame.String()
		frame.Reset()
		if strings.Contains(got, want) && (body == "" || strings.Contains(got, body)) {
			return nil
		}
	}
	return fmt.Errorf("timed out waiting for %q in the event stream", want)
}

func initializeMessageQueueHTTPScenario(sc *godog.ScenarioContext) {
	s := &queueHTTPState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a running coddy composer server$`, s.startServer)
	sc.Step(`^an agent session$`, s.haveSession)
	sc.Step(`^a turn is running for that session$`, s.turnIsRunning)
	sc.Step(`^a second client is watching that turn$`, s.watchTurn)
	sc.Step(`^a third client is subscribed to the server event stream$`, s.subscribeEvents)
	sc.Step(`^the operator posts "([^"]*)" to the session queue$`, s.postsToQueue)
	sc.Step(`^the operator posted "([^"]*)" to the session queue$`, s.postsToQueue)
	sc.Step(`^the queue answers with that message and an identifier$`, func() error {
		return s.queueAnsweredWithMessage("check the Windows path too")
	})
	sc.Step(`^reading the queue lists that one message$`, func() error {
		return s.readingQueueListsOne("check the Windows path too")
	})
	sc.Step(`^the operator deletes that queued message$`, s.deletesQueuedMessage)
	sc.Step(`^the queue is empty$`, s.queueIsEmpty)
	sc.Step(`^the queue refuses it because no turn is running$`, s.queueRefusedNoTurn)
	sc.Step(`^reading the queue lists no messages$`, s.readingQueueListsNothing)
	sc.Step(`^the watching client is told what the queue now holds$`, func() error {
		return s.watcherToldQueueHolds("check the Windows path too")
	})
	sc.Step(`^the subscribed client is told what the queue holds$`, func() error {
		return s.awaitEvents("event: message_queue", "check the Windows path too")
	})
	sc.Step(`^the subscribed client is told the queue is empty$`, func() error {
		return s.awaitEvents("event: message_queue", `"messages":[]`)
	})
}

func TestMessageQueueHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "message-queue-http",
		ScenarioInitializer: initializeMessageQueueHTTPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/message_queue_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("message queue HTTP feature suite failed")
	}
}
