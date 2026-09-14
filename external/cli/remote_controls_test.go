//go:build cli

package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
)

func TestRemoteControlsReconnectReadyRecoversAMissedEnd(t *testing.T) {
	f := newRemoteControlStand(t)
	f.mu.Lock()
	f.active = true
	f.mu.Unlock()
	f.turnEvent(t, sharedControlSession, true)
	if !f.app.remoteTurnActive {
		t.Fatal("start was not observed")
	}
	f.mu.Lock()
	f.active = false
	f.rows = nil
	f.version = 9
	f.mu.Unlock()
	f.syncEvents(t, controlFrame("ready", `{}`))
	if f.app.remoteTurnActive || f.app.queue.version != 9 {
		pumpControls(t, f.app, func(updateMsg) bool { return !f.app.remoteTurnActive && f.app.queue.version == 9 })
	}
	if f.app.handleGlobalKey([]byte("\x1b")) {
		t.Fatal("missed end left Escape enabled after reconnect")
	}
	f.app.editor.SetText("next turn")
	f.app.dispatchInput([]byte("\r"))
	f.request(t, "prompt")
}

func TestRemoteControlsOwnEOFDoesNotClearServerActivityOrQueue(t *testing.T) {
	f := newRemoteControlStand(t)
	f.app.mgr = &recordingControlBackend{backend: f.h}
	f.turnEvent(t, sharedControlSession, true)
	f.syncEvents(t, controlQueueFrame(sharedControlSession, nil, 10))
	f.app.queue.Apply([]acp.QueuedMessage{{ID: "keep"}}, 11)
	f.app.turnActive, f.app.turnSessionID = true, sharedControlSession
	f.app.applyLoopMessage(updateMsg{sessionID: sharedControlSession, update: turnDone{sessionID: sharedControlSession, err: fmt.Errorf("stream ended early")}})
	if f.app.turnActive {
		t.Fatal("own worker was not released")
	}
	if !f.app.remoteTurnActive || len(f.app.queue.Rows()) != 1 {
		t.Fatal("own EOF discarded server-owned controls")
	}
	if !f.app.handleGlobalKey([]byte("\x1b")) {
		t.Fatal("own EOF disabled Stop")
	}
}

func TestRemoteControlsIgnoreStaleActivityAndAnotherSessionsReplies(t *testing.T) {
	f := newRemoteControlStand(t)
	a := f.app
	a.applyLoopMessage(updateMsg{sessionID: sharedControlSession, update: remote.ActivityUpdate{TurnActive: true, Revision: 20}})
	a.applyLoopMessage(updateMsg{sessionID: sharedControlSession, update: remote.ActivityUpdate{TurnActive: false, Revision: 19}})
	a.editor.SetText("current session draft")
	a.applyLoopMessage(updateMsg{sessionID: "sess_old", update: remote.ActivityUpdate{TurnActive: false, Revision: 30}})
	a.applyLoopMessage(updateMsg{sessionID: "sess_old", update: queueResult{action: "enqueue", text: "old draft", err: fmt.Errorf("queue full")}})
	if !a.remoteTurnActive || a.remoteActivityRevision != 20 {
		t.Fatal("stale or other-session activity disabled controls")
	}
	if a.editor.PendingText() != "current session draft" {
		t.Fatal("another session's queue failure overwrote the draft")
	}
	a.adoptSession("sess_other", nil, nil)
	if a.remoteTurnActive || a.remoteActivityRevision != 0 {
		t.Fatal("session switch inherited the old session's activity")
	}
}

func TestRemoteControlsQueueRefusalKeepsNewerTyping(t *testing.T) {
	f := newRemoteControlStand(t)
	f.app.editor.SetText("newer draft")
	f.app.applyLoopMessage(updateMsg{sessionID: sharedControlSession, update: queueResult{action: "enqueue", text: "refused draft", err: fmt.Errorf("queue full")}})
	if got := f.app.editor.PendingText(); got != "refused draft\nnewer draft" {
		t.Fatalf("queue failure lost text typed while waiting: %q", got)
	}
}

func TestRemoteControlsSenderOptsIntoPrivateUpdates(t *testing.T) {
	f := newRemoteControlStand(t)
	sender, ok := f.app.Sender().(interface {
		SendControlUpdate(string, any) error
	})
	if !ok {
		t.Fatal("console sender does not expose the private control boundary")
	}
	if err := sender.SendControlUpdate(sharedControlSession, remote.ActivityUpdate{TurnActive: true, Revision: 7}); err != nil {
		t.Fatal(err)
	}
	if err := sender.SendControlUpdate(sharedControlSession, remote.CancelUpdate{Error: "stop unavailable"}); err != nil {
		t.Fatal(err)
	}
	drainControls(f.app)
	if !f.app.remoteTurnActive || f.app.remoteActivityRevision != 7 || !strings.Contains(transcriptText(f.app), "stop unavailable") {
		t.Fatal("private controls did not reach the console loop")
	}
}

type remoteControlTransport func(*http.Request) (*http.Response, error)

func (f remoteControlTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRemoteControlsShutdownCancelsPendingQueueHTTP(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	transport := remoteControlTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/queue") {
			close(started)
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
		return nil, context.Canceled
	})
	app, err := buildRemoteApp(&config.Config{}, &remote.Options{
		BaseURL: "http://remote.invalid", HTTPClient: &http.Client{Transport: transport}, Log: slog.New(slog.DiscardHandler),
	}, slog.New(slog.DiscardHandler), &bddTerminal{cols: 100, rows: 30}, "dark", true)
	if err != nil {
		t.Fatal(err)
	}
	h := app.mgr.(*remote.Handler)
	t.Cleanup(func() {
		close(release)
		h.Close()
		app.Close()
		app.JoinWorkers(3 * time.Second)
	})
	app.sessionID = sharedControlSession
	app.enqueuePrompt("follow-up")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("queue worker did not start")
	}
	// Match runInteractive.restore: backend Close precedes App.JoinWorkers.
	h.Close()
	app.Close()
	done := make(chan struct{})
	go func() { app.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("console shutdown left the queue worker blocked on HTTP")
	}
}
