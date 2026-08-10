//go:build http

package httpserver

// Godog harness for features/composer_live_watch.feature: proves an agent turn is
// watchable from a second client no matter which response shape started it. Every
// request goes over the real HTTP surface; a stub runner keeps it LLM-free.

import (
	"context"
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

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const watchFeatureReply = "the watched answer"

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
}

func (s *composerWatchState) reset() {
	s.close()
	s.root, _ = os.MkdirTemp("", "coddy-watch-*")
	s.turnStarted = make(chan struct{})
	s.releaseTurn = make(chan struct{})
	s.scriptDone = make(chan struct{})
	s.watched = ""
	s.scriptStatus = 0
	s.scriptBody = ""
}

func (s *composerWatchState) close() {
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
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		s.scriptStatus = res.StatusCode
		s.scriptBody = string(body)
	}()
	<-s.turnStarted
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
	defer res.Body.Close()
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
