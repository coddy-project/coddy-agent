//go:build http

package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

var errRunnerExploded = errors.New("runner exploded")

// watchedTurn drives a profile turn whose runner blocks until the test has attached a
// composer-stream subscriber, so the assertions never race the relay's lifetime.
type watchedTurn struct {
	started  chan struct{}
	release  chan struct{}
	runError error
}

func (w *watchedTurn) runner() session.AgentRunner {
	var once sync.Once
	return func(_ context.Context, st *session.State, _ []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		once.Do(func() { close(w.started) })
		<-w.release
		if w.runError != nil {
			return "", w.runError
		}
		_ = sender.SendSessionUpdate(st.GetID(), acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "watched reply"},
		})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "watched reply"})
		return string(acp.StopReasonEndTurn), nil
	}
}

// postNonStreamProfileTurn starts a stream:false profile turn against path and returns a
// channel carrying the HTTP response body once the turn completes.
func postNonStreamProfileTurn(t *testing.T, ts *httptest.Server, path, sid, payload string) <-chan *http.Response {
	t.Helper()
	out := make(chan *http.Response, 1)
	go func() {
		req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(payload))
		if err != nil {
			t.Error(err)
			close(out)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", sid)
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Error(err)
			close(out)
			return
		}
		out <- res
	}()
	return out
}

func subscribeComposerStream(t *testing.T, ts *httptest.Server, sid string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		ts.URL+"/coddy/sessions/"+url.PathEscape(sid)+"/composer-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Coddy-Session-ID", sid)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("composer-stream status %d", res.StatusCode)
	}
	return res
}

// The ralph-loop case: a script drives the session with plain non-streaming POSTs and
// never reads an SSE body, while a browser watches the same session live.
func TestResponsesNonStreamProfileTurnIsWatchable(t *testing.T) {
	turn := &watchedTurn{started: make(chan struct{}), release: make(chan struct{})}
	_, srv, _ := testHTTPServerPersistWithRunner(t, turn.runner())
	sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := sn.SessionID
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resCh := postNonStreamProfileTurn(t, ts, "/v1/responses", sid,
		`{"model":"agent","input":"hi","stream":false}`)
	<-turn.started

	sub := subscribeComposerStream(t, ts, sid)
	defer func() { _ = sub.Body.Close() }()
	close(turn.release)

	watched, err := io.ReadAll(sub.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(watched), `"content":"watched reply"`) {
		t.Fatalf("watcher never saw the assistant delta: %s", watched)
	}
	if !strings.Contains(string(watched), "data: [DONE]") {
		t.Fatalf("watcher never saw the stream terminate: %s", watched)
	}

	res := <-resCh
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST status %d: %s", res.StatusCode, body)
	}
	// The caller's own answer must stay the plain JSON body it has always been.
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("POST content-type %q, want json", ct)
	}
	if !strings.Contains(string(body), `"object":"response"`) ||
		!strings.Contains(string(body), "watched reply") {
		t.Fatalf("POST body changed shape: %s", body)
	}
}

func TestChatCompletionsNonStreamProfileTurnIsWatchable(t *testing.T) {
	turn := &watchedTurn{started: make(chan struct{}), release: make(chan struct{})}
	_, srv, _ := testHTTPServerPersistWithRunner(t, turn.runner())
	sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := sn.SessionID
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resCh := postNonStreamProfileTurn(t, ts, "/v1/chat/completions", sid,
		`{"model":"agent","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	<-turn.started

	sub := subscribeComposerStream(t, ts, sid)
	defer func() { _ = sub.Body.Close() }()
	close(turn.release)

	watched, err := io.ReadAll(sub.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(watched), `"content":"watched reply"`) {
		t.Fatalf("watcher never saw the assistant delta: %s", watched)
	}

	res := <-resCh
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST status %d: %s", res.StatusCode, body)
	}
	if !strings.Contains(string(body), `"object":"chat.completion"`) {
		t.Fatalf("POST body changed shape: %s", body)
	}
}

// A watcher must be told the turn failed rather than left staring at a stream that
// simply stops.
func TestNonStreamProfileTurnErrorReachesWatchers(t *testing.T) {
	turn := &watchedTurn{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		runError: errRunnerExploded,
	}
	_, srv, _ := testHTTPServerPersistWithRunner(t, turn.runner())
	sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := sn.SessionID
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resCh := postNonStreamProfileTurn(t, ts, "/v1/responses", sid,
		`{"model":"agent","input":"hi","stream":false}`)
	<-turn.started

	sub := subscribeComposerStream(t, ts, sid)
	defer func() { _ = sub.Body.Close() }()
	close(turn.release)

	watched, err := io.ReadAll(sub.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(watched), errRunnerExploded.Error()) {
		t.Fatalf("watcher never saw the error frame: %s", watched)
	}
	if !strings.Contains(string(watched), "data: [DONE]") {
		t.Fatalf("a failed turn must still terminate the watched stream: %s", watched)
	}

	res := <-resCh
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("POST status %d: %s", res.StatusCode, body)
	}
}

// Taking the turn lock in the handler must keep answering the same 409 the streaming
// branch has always answered.
func TestNonStreamProfileTurnOnBusySessionConflicts(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := sn.SessionID
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	unlock, err := mgr.AcquireComposerTurnLock(sid, mgr.SessionByID(sid))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses",
		strings.NewReader(`{"model":"agent","input":"hi","stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", sid)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)

	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", res.StatusCode, body)
	}
	if !strings.Contains(string(body), "session busy: another agent turn is in progress") {
		t.Fatalf("409 body changed: %s", body)
	}
}

// seedGoalHistory stores what a goal's turn leaves in a session - a prompt, a
// run_command that stopped a container with its result, the answer - each row
// stamped with the goal's id, then pauses the goal so no supervisor follows the
// requests the tests send. It returns the state and the goal.
func seedGoalHistory(t *testing.T, mgr *session.Manager) (*session.State, session.GoalState) {
	t.Helper()
	sn, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(sn.SessionID)
	goal, _ := session.NewGoal("stop the database")
	st.SetGoal(goal)
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "stop the database"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run_command", InputJSON: `{"command":"docker compose down"}`}}})
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Content: "stopped db"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})
	st.UpdateGoal("", func(g *session.GoalState) bool { g.Status = session.GoalPaused; return true })
	for i, m := range st.GetMessages() {
		if m.Goal != goal.ID {
			t.Fatalf("seed row %d is not stamped with the goal", i)
		}
	}
	return st, goal
}

func describeRows(rows []llm.Message) string {
	parts := make([]string, len(rows))
	for i, m := range rows {
		parts[i] = fmt.Sprintf("%s %q goal=%q acked=%v", m.Role, m.Content, m.Goal, m.RiskAcknowledged)
	}
	return strings.Join(parts, "; ")
}

// The history a client sends replaces the stored one, but only for a request
// that is let in: one that finds the session busy is refused before it touches
// anything, so it cannot drop the rows of the running turn nor the goal ids
// stamped on them, whichever shape it asked its answer in.
func TestChatCompletionsBusyRequestLeavesTheStoredHistoryAlone(t *testing.T) {
	for _, stream := range []string{"false", "true"} {
		t.Run("stream_"+stream, func(t *testing.T) {
			mgr, srv, _ := testHTTPServerPersist(t)
			st, _ := seedGoalHistory(t, mgr)
			before := st.GetMessages()
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()

			unlock, err := mgr.AcquireComposerTurnLock(st.GetID(), st)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()

			req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(
				`{"model":"agent","stream":`+stream+`,"messages":[{"role":"user","content":"a history of the client's own"},{"role":"assistant","content":"so it says"},{"role":"user","content":"hi"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Coddy-Session-ID", st.GetID())
			res, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != http.StatusConflict {
				t.Fatalf("status %d, want 409: %s", res.StatusCode, body)
			}
			if after := st.GetMessages(); !reflect.DeepEqual(after, before) {
				t.Fatalf("a refused request changed the stored history:\nbefore %s\nafter  %s", describeRows(before), describeRows(after))
			}
		})
	}
}

// A client that sends back the history it holds - the prompt, the call with its
// result, the answer - and one more prompt keeps the goal ids on the rows that
// are the stored ones, whichever kind of model answers: the risk gate reads a
// goal's docker steps off those ids. A row the client wrote carries none.
func TestChatCompletionsReplayedHistoryKeepsTheGoalStamps(t *testing.T) {
	const replay = `[{"role":"user","content":"stop the database"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"run_command","arguments":"{\"command\":\"docker compose down\"}"}}]},` +
		`{"role":"tool","tool_call_id":"c1","content":"stopped db"},` +
		`{"role":"assistant","content":"done"},` +
		`{"role":"user","content":"and now?"}]`
	for _, model := range []string{"agent", "openai/gpt-4o"} {
		t.Run(model, func(t *testing.T) {
			mgr, srv, _ := testHTTPServerPersist(t)
			srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) {
				return fakeProvider{reply: "ok"}, nil
			}
			st, goal := seedGoalHistory(t, mgr)
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()

			req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(
				`{"model":"`+model+`","stream":false,"messages":`+replay+`}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Coddy-Session-ID", st.GetID())
			res, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", res.StatusCode, body)
			}

			rows := st.GetMessages()
			if len(rows) < 5 {
				t.Fatalf("history has %d rows: %+v", len(rows), rows)
			}
			for i := 0; i < 4; i++ {
				if rows[i].Goal != goal.ID {
					t.Errorf("replayed row %d (%s %q) lost its goal id: %q", i, rows[i].Role, rows[i].Content, rows[i].Goal)
				}
			}
			if rows[4].Role != llm.RoleUser || rows[4].Goal != "" {
				t.Errorf("the prompt of the request, row 4 = %+v, want a user row of no goal (the goal is paused)", rows[4])
			}
		})
	}
}

// Publishing to a relay must not smuggle in the streaming path's detached context: a
// non-streaming caller that hangs up still cancels its own turn.
func TestNonStreamProfileTurnDiesWithItsRequest(t *testing.T) {
	started := make(chan struct{})
	ctxEnded := make(chan struct{})
	var once sync.Once
	runner := func(ctx context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		close(ctxEnded)
		return string(acp.StopReasonCancelled), nil
	}
	_, srv, _ := testHTTPServerPersistWithRunner(t, runner)
	sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	reqCtx, cancel := context.WithCancel(context.Background())
	go func() {
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodPost, ts.URL+"/v1/responses",
			strings.NewReader(`{"model":"agent","input":"hi","stream":false}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", sn.SessionID)
		res, err := ts.Client().Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
	}()

	<-started
	cancel()
	select {
	case <-ctxEnded:
	case <-time.After(10 * time.Second):
		t.Fatal("a non-streaming turn outlived the request that started it")
	}
}
