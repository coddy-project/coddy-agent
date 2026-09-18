//go:build http

package httpserver

// Godog harness for features/composer_live_watch.feature: proves an agent turn is
// watchable from a second client no matter which response shape started it. Every
// request goes over the real HTTP surface; a stub runner keeps it LLM-free.

import (
	"bufio"
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

const watchFeatureReply = "the watched answer"

const (
	watchFirstStep  = "the step already persisted"
	watchSecondStep = "the step still streaming"
)

type composerWatchState struct {
	root      string
	ts        *httptest.Server
	srv       *Server
	sessionID string

	turnStarted  chan struct{}
	releaseTurn  chan struct{}
	scriptStatus int
	scriptBody   string
	scriptDone   chan struct{}

	watched string

	// twoSteps makes the runner persist a first step and stream a second one before
	// parking, the moment a reloaded tab loads the transcript and attaches.
	twoSteps      bool
	firstStepDone chan struct{}
	messagesRev   uint64
	transcript    string

	eventsBody  *bufio.Reader
	closeEvents func()
}

func (s *composerWatchState) reset() {
	s.close()
	s.root, _ = os.MkdirTemp("", "coddy-watch-*")
	s.turnStarted = make(chan struct{})
	s.releaseTurn = make(chan struct{})
	s.scriptDone = make(chan struct{})
	s.watched = ""
	s.twoSteps = false
	s.firstStepDone = make(chan struct{})
	s.messagesRev = 0
	s.transcript = ""
	s.scriptStatus = 0
	s.scriptBody = ""
}

func (s *composerWatchState) close() {
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

func (s *composerWatchState) startServer() error {
	home := filepath.Join(s.root, "home")
	sessRoot := filepath.Join(s.root, "sessions")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		return err
	}
	var once sync.Once
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		once.Do(func() { close(s.turnStarted) })
		if s.twoSteps {
			_ = sender.SendSessionUpdate(st.GetID(), acp.MessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeAgentMessageChunk,
				Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: watchFirstStep},
			})
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: watchFirstStep})
			_ = sender.SendSessionUpdate(st.GetID(), acp.MessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeAgentMessageChunk,
				Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: watchSecondStep},
			})
			close(s.firstStepDone)
			<-s.releaseTurn
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: watchSecondStep})
			return string(acp.StopReasonEndTurn), nil
		}
		<-s.releaseTurn
		_ = sender.SendSessionUpdate(st.GetID(), acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: watchFeatureReply},
		})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: watchFeatureReply})
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

func (s *composerWatchState) haveSession() error {
	sn, err := s.srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
	if err != nil {
		return err
	}
	s.sessionID = sn.SessionID
	return nil
}

// scriptStartsNonStreamingTurn mirrors a driver script: POST and wait for JSON, never
// reading an SSE body.
func (s *composerWatchState) scriptStartsNonStreamingTurn() error {
	go func() {
		defer close(s.scriptDone)
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
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		s.scriptStatus = res.StatusCode
		s.scriptBody = string(body)
	}()
	<-s.turnStarted
	return nil
}

func (s *composerWatchState) scriptStartsTwoStepTurn() error {
	s.twoSteps = true
	if err := s.scriptStartsNonStreamingTurn(); err != nil {
		return err
	}
	select {
	case <-s.firstStepDone:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("the first step never finished")
	}
}

// clientLoadsTranscript is what a reloaded tab does first: read the persisted history
// and the revision it was read at.
func (s *composerWatchState) clientLoadsTranscript() error {
	req, err := http.NewRequest(http.MethodGet,
		s.ts.URL+"/coddy/sessions/"+url.PathEscape(s.sessionID)+"/messages", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := s.ts.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Messages    []map[string]any `json:"messages"`
		MessagesRev *uint64          `json:"messagesRev"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	if body.MessagesRev == nil {
		return fmt.Errorf("the transcript does not say which revision it was read at")
	}
	s.messagesRev = *body.MessagesRev
	b, _ := json.Marshal(body.Messages)
	s.transcript = string(b)
	if !strings.Contains(s.transcript, watchFirstStep) {
		return fmt.Errorf("the transcript is missing the persisted step: %s", s.transcript)
	}
	return nil
}

func (s *composerWatchState) clientSubscribesAfterTranscript() error {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/coddy/sessions/%s/composer-stream?since_rev=%d",
		s.ts.URL, url.PathEscape(s.sessionID), s.messagesRev), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := s.ts.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return errStatus("composer stream not served", res.StatusCode, "")
	}
	select {
	case <-s.releaseTurn:
	default:
		close(s.releaseTurn)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	s.watched = string(b)
	return nil
}

func (s *composerWatchState) watcherSawStreamingStep() error {
	if !strings.Contains(s.watched, watchSecondStep) {
		return fmt.Errorf("watched stream missing the step still streaming: %s", s.watched)
	}
	return nil
}

func (s *composerWatchState) watcherSkippedPersistedStep() error {
	if strings.Contains(s.watched, watchFirstStep) {
		return fmt.Errorf("watched stream replayed a step the transcript holds: %s", s.watched)
	}
	return nil
}

func (s *composerWatchState) secondClientSubscribes() error {
	req, err := http.NewRequest(http.MethodGet,
		s.ts.URL+"/coddy/sessions/"+url.PathEscape(s.sessionID)+"/composer-stream", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := s.ts.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return errStatus("composer stream not served", res.StatusCode, "")
	}
	// The turn is parked until a watcher is attached, so releasing it here is what makes
	// the scenario deterministic rather than timing-dependent.
	select {
	case <-s.releaseTurn:
	default:
		close(s.releaseTurn)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	s.watched = string(b)
	return nil
}

func (s *composerWatchState) watcherSawAssistantText() error {
	if !strings.Contains(s.watched, watchFeatureReply) {
		return fmt.Errorf("watched stream missing the assistant text: %s", s.watched)
	}
	return nil
}

func (s *composerWatchState) watcherSawDone() error {
	if !strings.Contains(s.watched, "data: [DONE]") {
		return fmt.Errorf("watched stream never terminated: %s", s.watched)
	}
	return nil
}

func (s *composerWatchState) scriptGotItsJSON() error {
	<-s.scriptDone
	if s.scriptStatus != http.StatusOK {
		return errStatus("script response", s.scriptStatus, s.scriptBody)
	}
	if !strings.Contains(s.scriptBody, `"object":"response"`) ||
		!strings.Contains(s.scriptBody, watchFeatureReply) {
		return fmt.Errorf("script response is not the usual JSON body: %s", s.scriptBody)
	}
	return nil
}

func (s *composerWatchState) subscribesToServerEvents() error {
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
		_ = res.Body.Close()
		return errStatus("events stream", res.StatusCode, "")
	}
	s.eventsBody = bufio.NewReader(res.Body)
	s.closeEvents = func() {
		cancel()
		_ = res.Body.Close()
	}
	// Drain the connect-time snapshot so later reads see only new events.
	return s.awaitEventFrame("event: ready")
}

// awaitEventFrame reads whole SSE frames until one contains want.
func (s *composerWatchState) awaitEventFrame(want string) error {
	if s.eventsBody == nil {
		return fmt.Errorf("no event subscription")
	}
	var seen strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		line, err := s.eventsBody.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read events (seen %q): %w", seen.String(), err)
		}
		seen.WriteString(line)
		if strings.Contains(seen.String(), want) && strings.HasSuffix(seen.String(), "\n\n") {
			return nil
		}
	}
	return fmt.Errorf("timed out waiting for %q, seen %q", want, seen.String())
}

func (s *composerWatchState) toldTurnStarted() error {
	if err := s.awaitEventFrame("turn_started"); err != nil {
		return err
	}
	return nil
}

func (s *composerWatchState) toldTurnEnded() error {
	// The turn is parked until something releases it; this scenario has no composer
	// subscriber to do that, so the step releases it itself.
	select {
	case <-s.releaseTurn:
	default:
		close(s.releaseTurn)
	}
	return s.awaitEventFrame("turn_ended")
}

func (s *composerWatchState) watcherToldNoActiveStream() error {
	if !strings.Contains(s.watched, "no_active_stream") {
		return fmt.Errorf("watcher was not told the session is idle: %s", s.watched)
	}
	return nil
}

func initializeComposerWatchScenario(sc *godog.ScenarioContext) {
	s := &composerWatchState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		// Let a parked turn finish so its goroutine cannot outlive the scenario.
		select {
		case <-s.releaseTurn:
		default:
			close(s.releaseTurn)
		}
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy composer server$`, s.startServer)
	sc.Step(`^an agent session$`, s.haveSession)
	sc.Step(`^a script starts an agent turn with "stream" set to false$`, s.scriptStartsNonStreamingTurn)
	sc.Step(`^a second client subscribes to the composer stream of that session$`, s.secondClientSubscribes)
	sc.Step(`^the watching client receives the assistant text of the turn$`, s.watcherSawAssistantText)
	sc.Step(`^the watching client receives the terminating done frame$`, s.watcherSawDone)
	sc.Step(`^the script still receives its plain JSON response$`, s.scriptGotItsJSON)
	sc.Step(`^the watching client is told there is no active composer stream$`, s.watcherToldNoActiveStream)
	sc.Step(`^a client is subscribed to the server event stream$`, s.subscribesToServerEvents)
	sc.Step(`^the subscribed client is told the turn started for that session$`, s.toldTurnStarted)
	sc.Step(`^the subscribed client is told the turn ended when it finishes$`, s.toldTurnEnded)
	sc.Step(`^a script starts an agent turn whose first step is persisted while the second still streams$`, s.scriptStartsTwoStepTurn)
	sc.Step(`^a client loads the transcript of that session$`, s.clientLoadsTranscript)
	sc.Step(`^the client subscribes to the composer stream after that transcript$`, s.clientSubscribesAfterTranscript)
	sc.Step(`^the watching client receives the text of the step still streaming$`, s.watcherSawStreamingStep)
	sc.Step(`^the watching client does not receive the step its transcript already holds$`, s.watcherSkippedPersistedStep)
}

func TestComposerLiveWatchFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "composer-live-watch",
		ScenarioInitializer: initializeComposerWatchScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/composer_live_watch.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("composer live watch feature suite failed")
	}
}
