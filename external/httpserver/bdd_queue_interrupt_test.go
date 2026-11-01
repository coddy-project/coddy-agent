//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// queueInterruptHTTPState uses real HTTP admission and queue operations. Unlike
// the legacy queue stand, its runner explicitly consumes and acknowledges the
// reserved batch; returning success without reading it is not a continuation.
type queueInterruptHTTPState struct {
	queueHTTPState
	unwinding  chan struct{}
	exitFirst  chan struct{}
	batchReady chan struct{}
	readBatch  chan struct{}
	batchRead  chan struct{}
	runs       atomic.Int32
	batch      []session.QueuedMessage
	child      context.Context
}

func (s *queueInterruptHTTPState) startServer() error {
	s.reset()
	s.unwinding = make(chan struct{})
	s.exitFirst = make(chan struct{})
	s.batchReady = make(chan struct{})
	s.readBatch = make(chan struct{})
	s.batchRead = make(chan struct{})
	cfg := &config.Config{
		Paths:  config.Paths{Home: filepath.Join(s.root, "home"), CWD: s.root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Multimodal: true}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	mgr := session.NewManager(cfg, noopSender{}, s.run, slog.Default(), s.root,
		&session.FileStore{Root: filepath.Join(s.root, "sessions")})
	s.srv = New(cfg, mgr, slog.Default(), s.root)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *queueInterruptHTTPState) run(ctx context.Context, st *session.State, prompt []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
	if s.runs.Add(1) == 1 {
		s.child = ctx
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: prompt[0].Text})
		close(s.turnStarted)
		<-ctx.Done()
		close(s.unwinding)
		select {
		case <-s.exitFirst:
		case <-s.releaseTurn:
		}
		return string(acp.StopReasonCancelled), ctx.Err()
	}
	rows, ok := session.TakeQueuedPromptBatch(ctx, st.GetID())
	if !ok {
		return "", fmt.Errorf("replacement runner received no reserved batch")
	}
	s.batch = rows
	close(s.batchReady)
	select {
	case <-s.readBatch:
	case <-ctx.Done():
		return string(acp.StopReasonCancelled), ctx.Err()
	case <-s.releaseTurn:
		return string(acp.StopReasonCancelled), nil
	}
	if !session.BeginQueuedPromptRead(ctx, st.GetID()) {
		return "", fmt.Errorf("reserved batch acknowledgement refused")
	}
	// BeginQueuedPromptRead commits this bounded batch. Do not check
	// cancellation between rows, which would acknowledge only a prefix.
	for _, q := range rows {
		images := make([]llm.ImagePart, 0, len(q.ImageParts))
		for _, p := range q.ImageParts {
			images = append(images, llm.ImagePart{Name: p.Name, DataURL: p.DataURL})
		}
		if err := session.SavePartsToAssets(images, st.GetPersistedSessionDir()); err != nil {
			return "", err
		}
		if err := sender.SendSessionUpdate(st.GetID(), acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeUserMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: q.Text},
		}); err != nil {
			return "", err
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: q.Text, ImageParts: images, FollowUp: true})
	}
	close(s.batchRead)
	select {
	case <-s.releaseTurn:
		return string(acp.StopReasonEndTurn), nil
	case <-ctx.Done():
		return string(acp.StopReasonCancelled), ctx.Err()
	}
}

func (s *queueInterruptHTTPState) close() {
	if s.srv != nil {
		s.srv.mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: s.sessionID})
	}
	s.queueHTTPState.close()
}

func queueInterruptAwait(ch <-chan struct{}, what string) error {
	select {
	case <-ch:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timed out waiting for %s", what)
	}
}

func (s *queueInterruptHTTPState) enqueueBatch() error {
	for _, body := range []string{
		`{"text":"first","mode":"after_turn"}`,
		`{"text":"duplicate","mode":"steer","inline_files":[{"name":"pixel.png","data_url":"` + queueFeatureImage + `"}]}`,
		`{"text":"duplicate","mode":"after_turn"}`,
	} {
		if err := s.call(http.MethodPost, "", []byte(body)); err != nil {
			return err
		}
		if s.lastStatus != http.StatusCreated {
			return fmt.Errorf("enqueue answered %d: %s", s.lastStatus, s.lastRaw)
		}
	}
	return nil
}

func (s *queueInterruptHTTPState) cancel(body string) error {
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/coddy/sessions/"+s.sessionID+"/cancel", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := s.ts.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	s.lastStatus, s.lastRaw = res.StatusCode, string(raw)
	s.lastBody = map[string]interface{}{}
	return json.Unmarshal(raw, &s.lastBody)
}

func (s *queueInterruptHTTPState) requestContinuation() error {
	if err := s.cancel(`{"continue_queue":true}`); err != nil {
		return err
	}
	if s.lastStatus != http.StatusOK || s.lastBody["queueContinued"] != true ||
		s.lastBody["object"] != "coddy.session_cancelled" || s.lastBody["id"] != s.sessionID {
		return fmt.Errorf("queue interrupt was not accepted: %d %s", s.lastStatus, s.lastRaw)
	}
	return nil
}

func (s *queueInterruptHTTPState) onlyChildInterrupted() error {
	if err := queueInterruptAwait(s.unwinding, "child cancellation"); err != nil {
		return err
	}
	if !session.IsQueuedTurnInterrupt(s.child, s.sessionID) || s.child.Err() != context.Canceled {
		return fmt.Errorf("child did not receive the deliberate queue interrupt: %v", context.Cause(s.child))
	}
	st := s.srv.mgr.SessionByID(s.sessionID)
	if !s.srv.mgr.SessionTurnActiveInProcess(s.sessionID) || st.IsUserCancelledTurn() {
		return fmt.Errorf("interrupt cancelled or released the root admission")
	}
	select {
	case <-s.turnDone:
		return fmt.Errorf("root HTTP prompt finished before its continuation")
	default:
	}
	exists, err := session.CancelRequestExists(st.GetPersistedSessionDir())
	if err != nil || exists {
		return fmt.Errorf("accepted interrupt left a disk cancel marker: exists=%v, err=%v", exists, err)
	}
	return nil
}

func (s *queueInterruptHTTPState) consumeBatch() error {
	close(s.exitFirst)
	if err := queueInterruptAwait(s.batchReady, "replacement runner"); err != nil {
		return err
	}
	if len(s.batch) != 3 || s.batch[0].Text != "first" || s.batch[1].Text != "duplicate" || s.batch[2].Text != "duplicate" ||
		s.batch[0].Mode != session.QueueModeAfterTurn || s.batch[1].Mode != session.QueueModeSteer || s.batch[2].Mode != session.QueueModeAfterTurn ||
		len(s.batch[1].ImageParts) != 1 || s.batch[1].ImageParts[0].DataURL != queueFeatureImage ||
		s.batch[0].ID == s.batch[1].ID || s.batch[1].ID == s.batch[2].ID {
		return fmt.Errorf("reserved batch lost FIFO, duplicates, modes or image: %+v", s.batch)
	}
	close(s.readBatch)
	return queueInterruptAwait(s.batchRead, "whole batch acknowledgement")
}

func (s *queueInterruptHTTPState) watcherSeesRows() error {
	for _, text := range []string{"first", "duplicate", "duplicate"} {
		frame, err := s.watchBody.awaitFrame("event: user_message", "")
		if err != nil {
			return err
		}
		if !strings.Contains(frame, `"text":"`+text+`"`) {
			return fmt.Errorf("user event out of FIFO order, want %q: %s", text, frame)
		}
	}
	return nil
}

func (s *queueInterruptHTTPState) persistedRows() error {
	s.release()
	if err := queueInterruptAwait(s.turnDone, "continued HTTP prompt"); err != nil {
		return err
	}
	snap, err := s.srv.mgr.FileStore().ReadSnapshot(s.sessionID)
	if err != nil {
		return err
	}
	var texts []string
	for _, m := range snap.Messages {
		if m.Role == llm.RoleUser {
			texts = append(texts, m.Content)
		}
	}
	if !reflect.DeepEqual(texts, []string{"go", "first", "duplicate", "duplicate"}) || s.runs.Load() != 2 {
		return fmt.Errorf("persisted rows=%q, runs=%d; want initial prompt, three FIFO rows, exactly two runs", texts, s.runs.Load())
	}
	if len(snap.Messages[2].ImageParts) != 1 || snap.Messages[2].ImageParts[0].FilePath == "" {
		return fmt.Errorf("queued image was not persisted on its own row")
	}
	return s.readingQueueListsNothing()
}

func initializeQueueInterruptHTTPScenario(sc *godog.ScenarioContext) {
	s := &queueInterruptHTTPState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a running coddy composer server$`, s.startServer)
	sc.Step(`^an agent session$`, s.haveSession)
	sc.Step(`^a turn is running for that session$`, s.turnIsRunning)
	sc.Step(`^a second client is watching that turn$`, s.watchTurn)
	sc.Step(`^three FIFO follow-ups include duplicate text, mixed modes and an image$`, s.enqueueBatch)
	sc.Step(`^the operator stops with queue continuation enabled$`, s.requestContinuation)
	sc.Step(`^only the child run is interrupted without a disk cancel marker$`, s.onlyChildInterrupted)
	sc.Step(`^the replacement runner acknowledges the entire reserved batch$`, s.consumeBatch)
	sc.Step(`^the watcher receives each queued user row in FIFO order$`, s.watcherSeesRows)
	sc.Step(`^all three rows persist once without an extra run$`, s.persistedRows)
}

func TestQueueInterruptHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "queue-interrupt-http", ScenarioInitializer: initializeQueueInterruptHTTPScenario,
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/message_queue_http.feature"}, Tags: "@queue_interrupt", TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("queue interrupt HTTP feature suite failed")
	}
}
