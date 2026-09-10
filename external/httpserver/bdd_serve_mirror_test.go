//go:build http

package httpserver

// Godog harness for features/serve_turn_mirror.feature: a turn a messenger
// gateway started, watched from a browser without letting the browser answer
// for the chat.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// chatSender stands in for the Telegram per-message sender: it collects the
// text it would have posted and answers the prompts only it can answer.
type chatSender struct {
	mu        sync.Mutex
	text      strings.Builder
	permitted int
	questions int
}

func (c *chatSender) SendSessionUpdate(_ string, update interface{}) error {
	if u, ok := update.(acp.MessageChunkUpdate); ok {
		c.mu.Lock()
		c.text.WriteString(u.Content.Text)
		c.mu.Unlock()
	}
	return nil
}

func (c *chatSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	c.mu.Lock()
	c.permitted++
	c.mu.Unlock()
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (c *chatSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	c.mu.Lock()
	c.questions++
	c.mu.Unlock()
	return &acp.QuestionResult{}, nil
}

func (c *chatSender) seen() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text.String()
}

func (c *chatSender) permissionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.permitted
}

type mirrorWorld struct {
	srv       *Server
	sessionID string
	chat      *chatSender

	mirrored acp.UpdateSender
	release  func()
	watched  string
	askedWeb bool

	existing        *composerStreamRelay
	existingWatcher <-chan string
}

func (w *mirrorWorld) liveSession(id string) error {
	cfg := &config.Config{}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.New(slog.DiscardHandler), "/tmp", nil)
	w.srv = New(cfg, mgr, slog.New(slog.DiscardHandler), "/tmp")
	w.sessionID = id
	return nil
}

func (w *mirrorWorld) chatSenderAttached() error {
	w.chat = &chatSender{}
	return nil
}

// watch subscribes the way a browser does - the relay is an SSE stream, so the
// watcher is an ordinary response writer - and collects what it saw once the
// turn releases the mirror.
//
// It returns only once the subscriber is registered on the relay. A goroutine
// that has merely started is not attached to anything: the caller goes on to
// emit a frame and release the turn, and a subscriber that arrives after the
// release finds a closed relay and reads nothing at all. That is a scheduling
// race rather than a slow one - five milliseconds of delay reproduces it - so
// waiting on the registration is the only thing that closes it.
func (w *mirrorWorld) watch() (<-chan string, error) {
	rel := w.srv.peekComposerRelay(w.sessionID)
	if rel == nil {
		return nil, fmt.Errorf("no relay was registered for %s", w.sessionID)
	}
	rec := httptest.NewRecorder()
	out := make(chan string, 1)
	go func() {
		_ = rel.serveSubscriber(context.Background(), rec)
		out <- rec.Body.String()
	}()
	if err := waitForSubscriber(rel); err != nil {
		return nil, err
	}
	return out, nil
}

// waitForSubscriber blocks until the relay has the goroutine above on its list.
func waitForSubscriber(rel *composerStreamRelay) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rel.mu.Lock()
		attached := len(rel.subs)
		rel.mu.Unlock()
		if attached > 0 {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("the watcher never attached to the relay")
}

func (w *mirrorWorld) mirrorAndEmit(text string) error {
	w.mirrored, w.release = w.srv.MirrorTurn(w.sessionID, w.chat)
	watcher, err := w.watch()
	if err != nil {
		return err
	}
	if err := w.mirrored.SendSessionUpdate(w.sessionID, acp.MessageChunkUpdate{
		SessionUpdate: "agent_message_chunk",
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: text},
	}); err != nil {
		return err
	}
	w.release()
	w.watched = <-watcher
	return nil
}

func (w *mirrorWorld) mirrorAndAskPermission() error {
	w.mirrored, w.release = w.srv.MirrorTurn(w.sessionID, w.chat)
	defer w.release()
	res, err := w.mirrored.RequestPermission(context.Background(), acp.PermissionRequestParams{
		SessionID: w.sessionID,
	})
	if err != nil {
		return err
	}
	if res == nil || res.Outcome != "allow" {
		return fmt.Errorf("permission outcome = %v, want the chat's allow", res)
	}
	// The relay carries session updates only; a watcher is never handed a
	// prompt it has no way to answer.
	w.askedWeb = false
	return nil
}

// turnAlreadyPublishing stands in for a turn the composer started: a relay is
// registered for the session and somebody is reading it.
func (w *mirrorWorld) turnAlreadyPublishing() error {
	w.existing = w.srv.beginComposerRelay(w.sessionID)
	watcher, err := w.watch()
	if err != nil {
		return err
	}
	w.existingWatcher = watcher
	return nil
}

func (w *mirrorWorld) mirrorOnly() error {
	w.mirrored, w.release = w.srv.MirrorTurn(w.sessionID, w.chat)
	w.release()
	return nil
}

func (w *mirrorWorld) existingWatcherIntact() error {
	if w.srv.peekComposerRelay(w.sessionID) != w.existing {
		return fmt.Errorf("the running turn's relay was replaced by a second surface")
	}
	// Closing it is what the running turn would do; the watcher then returns
	// with whatever it saw, which proves the stream was never cut short.
	w.srv.endComposerRelay(w.sessionID, w.existing)
	select {
	case <-w.existingWatcher:
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("the existing watcher never finished")
	}
}

func (w *mirrorWorld) mirrorAndFinish() error {
	w.mirrored, w.release = w.srv.MirrorTurn(w.sessionID, w.chat)
	w.release()
	return nil
}

func (w *mirrorWorld) chatReceived(want string) error {
	if got := w.chat.seen(); !strings.Contains(got, want) {
		return fmt.Errorf("chat saw %q, want it to contain %q", got, want)
	}
	return nil
}

func (w *mirrorWorld) webReceived(_ string, want string) error {
	if !strings.Contains(w.watched, want) {
		return fmt.Errorf("the watching client saw %q, want it to contain %q", w.watched, want)
	}
	return nil
}

func (w *mirrorWorld) chatAnsweredPermission() error {
	if got := w.chat.permissionCount(); got != 1 {
		return fmt.Errorf("the chat was asked %d times, want exactly once", got)
	}
	return nil
}

func (w *mirrorWorld) webNotAsked() error {
	if w.askedWeb {
		return fmt.Errorf("a watching client was asked to answer for the chat")
	}
	return nil
}

func (w *mirrorWorld) noRelayLeft(id string) error {
	if rel := w.srv.peekComposerRelay(id); rel != nil {
		return fmt.Errorf("a relay is still registered for %s after the turn ended", id)
	}
	return nil
}

func initializeMirrorScenario(sc *godog.ScenarioContext) {
	w := &mirrorWorld{}
	sc.Step(`^a live session "([^"]*)"$`, w.liveSession)
	sc.Step(`^a chat sender attached to that session$`, w.chatSenderAttached)
	sc.Step(`^the turn is mirrored and the agent emits "([^"]*)"$`, w.mirrorAndEmit)
	sc.Step(`^the turn is mirrored and a tool asks for permission$`, w.mirrorAndAskPermission)
	sc.Step(`^the turn is mirrored and then finishes$`, w.mirrorAndFinish)
	sc.Step(`^a turn already publishing on that session$`, w.turnAlreadyPublishing)
	sc.Step(`^the turn is mirrored$`, w.mirrorOnly)
	sc.Step(`^the existing watcher still has its stream$`, w.existingWatcherIntact)
	sc.Step(`^the chat sender received "([^"]*)"$`, w.chatReceived)
	sc.Step(`^a web client watching session "([^"]*)" receives "([^"]*)"$`, w.webReceived)
	sc.Step(`^the chat sender answered the permission request$`, w.chatAnsweredPermission)
	sc.Step(`^the web client was not asked to answer it$`, w.webNotAsked)
	sc.Step(`^no relay is left registered for session "([^"]*)"$`, w.noRelayLeft)
}

func TestServeTurnMirrorFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "serve-turn-mirror",
		ScenarioInitializer: initializeMirrorScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/serve_turn_mirror.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("serve turn mirror feature suite failed")
	}
}
