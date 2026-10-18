//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestOperatorCommandsFromHTTP(t *testing.T) {
	for raw, want := range map[string]bool{
		``:                                   false,
		`{}`:                                 false,
		`{"coddy_operator_commands":"1"}`:    true,
		`{"coddy_operator_commands":true}`:   true,
		`{"coddy_operator_commands":"true"}`: true,
		`{"coddy_operator_commands":"0"}`:    false,
		`{"coddy_operator_commands":false}`:  false,
		`not json`:                           false,
	} {
		if got := operatorCommandsFromHTTP(json.RawMessage(raw)); got != want {
			t.Errorf("%s: %v, want %v", raw, got, want)
		}
	}
}

// TestOperatorCommandsNeedTheDeclaredCapability: an interactive stream alone
// is not enough - an OpenAI client streams too and would never answer.
func TestOperatorCommandsNeedTheDeclaredCapability(t *testing.T) {
	sender := NewSender(&config.Config{}, &syncBuffer{}, true, "m")
	if acp.OperatorCommandsOf(sender) != nil {
		t.Fatal("a stream that did not declare the capability is offered operator commands")
	}
	sender.EnableOperatorCommands(true)
	if acp.OperatorCommandsOf(sender) == nil {
		t.Fatal("a stream that declared the capability is not offered operator commands")
	}
	relay := NewRelaySender(&config.Config{}, &syncBuffer{}, "m")
	relay.EnableOperatorCommands(true)
	if acp.OperatorCommandsOf(relay) != nil {
		t.Fatal("a relay sender with nobody reading is offered operator commands")
	}
	mirrored := &mirroredSender{primary: sender, watcher: relay}
	if acp.OperatorCommandsOf(mirrored) == nil {
		t.Fatal("the mirror hid the capability of the surface that started the turn")
	}
}

func startOperatorRequest(t *testing.T, sender *Sender, rec *syncBuffer, req acp.OperatorCommandRequest) <-chan *acp.OperatorCommandDecision {
	t.Helper()
	out := make(chan *acp.OperatorCommandDecision, 1)
	go func() {
		d, err := sender.RequestOperatorCommand(context.Background(), req)
		if err != nil {
			t.Errorf("RequestOperatorCommand: %v", err)
		}
		out <- d
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(rec.String(), "event: operator_command") {
		time.Sleep(2 * time.Millisecond)
	}
	if !strings.Contains(rec.String(), "event: operator_command") {
		t.Fatalf("no operator_command event: %s", rec.String())
	}
	return out
}

func postOperatorAnswer(t *testing.T, srv *Server, sessionID, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/coddy/sessions/"+sessionID+"/operator-command", strings.NewReader(body))
	r.SetPathValue("id", sessionID)
	w := httptest.NewRecorder()
	srv.coddySessionOperatorCommandPost(w, r)
	return w
}

func TestOperatorCommandRunStopOverHTTP(t *testing.T) {
	rec := &syncBuffer{}
	sender := NewSender(&config.Config{}, rec, true, "m")
	sender.EnableOperatorCommands(true)
	sid := "sess_0123456789abcdef01234567"
	req := acp.OperatorCommandRequest{SessionID: sid, RequestID: "oc_1", ToolCallID: "call_1", Command: `echo "hi"`, Shell: "bash", Host: "box", CWD: "/w"}
	decided := startOperatorRequest(t, sender, rec, req)
	if !strings.Contains(rec.String(), `"command":"echo \"hi\""`) {
		t.Fatalf("the event does not carry the command as it runs: %s", rec.String())
	}
	if !OperatorCommandPending(sid) {
		t.Fatal("a waiting request is not pending")
	}
	srv := &Server{}
	if w := postOperatorAnswer(t, srv, sid, `{"requestId":"oc_1","action":"stop"}`); w.Code != http.StatusConflict {
		t.Fatalf("stop before run: %d %s", w.Code, w.Body)
	}
	if w := postOperatorAnswer(t, srv, sid, `{"requestId":"oc_1","action":"run"}`); w.Code != http.StatusNoContent {
		t.Fatalf("run: %d %s", w.Code, w.Body)
	}
	d := <-decided
	if d == nil || d.Action != acp.OperatorActionRun {
		t.Fatalf("decision = %+v", d)
	}
	if w := postOperatorAnswer(t, srv, sid, `{"requestId":"oc_1","action":"run"}`); w.Code != http.StatusConflict {
		t.Fatalf("second run: %d %s", w.Code, w.Body)
	}
	if w := postOperatorAnswer(t, srv, sid, `{"requestId":"oc_1","action":"stop"}`); w.Code != http.StatusNoContent {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	<-d.Stop
	d.Done()
	deadline := time.Now().Add(2 * time.Second)
	for OperatorCommandPending(sid) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if OperatorCommandPending(sid) {
		t.Fatal("an ended request is still pending")
	}
	if w := postOperatorAnswer(t, srv, sid, `{"requestId":"oc_1","action":"stop"}`); w.Code != http.StatusNotFound {
		t.Fatalf("stop after the end: %d %s", w.Code, w.Body)
	}
}

func TestOperatorCommandAnswerBodyIsStrict(t *testing.T) {
	rec := &syncBuffer{}
	sender := NewSender(&config.Config{}, rec, true, "m")
	sender.EnableOperatorCommands(true)
	sid := "sess_abcdef0123456789abcdef01"
	decided := startOperatorRequest(t, sender, rec, acp.OperatorCommandRequest{SessionID: sid, RequestID: "oc_2", ToolCallID: "call_2", Command: "ls"})
	srv := &Server{}
	for _, body := range []string{
		`{"requestId":"oc_2","action":"run","command":"rm -rf /"}`,
		`{"requestId":"oc_2","action":"edit"}`,
		`{"action":"run"}`,
		`not json`,
	} {
		if w := postOperatorAnswer(t, srv, sid, body); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := postOperatorAnswer(t, srv, sid, `{"requestId":"oc_unknown","action":"run"}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown request: %d", w.Code)
	}
	if w := postOperatorAnswer(t, srv, sid, `{"requestId":"oc_2","action":"decline","note":"not now"}`); w.Code != http.StatusNoContent {
		t.Fatalf("decline: %d %s", w.Code, w.Body)
	}
	d := <-decided
	if d.Action != acp.OperatorActionDecline || d.Note != "not now" {
		t.Fatalf("decision = %+v", d)
	}
}

// panicWriter stands for the ResponseWriter of a request whose handler has
// returned: writing to it crashes the server.
type panicWriter struct{}

func (panicWriter) Write([]byte) (int, error) { panic("write after the handler returned") }

// TestDetachedSenderWritesNothing: a turn's tool can outlive the turn (an
// operator command that outlives a cancel); once the handler is gone its
// sender must not touch the response.
func TestDetachedSenderWritesNothing(t *testing.T) {
	sender := NewSender(&config.Config{}, panicWriter{}, true, "m")
	sender.EnableOperatorCommands(true)
	sender.Detach()
	_ = sender.SendSessionUpdate("s", acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: "c", Status: "in_progress"})
	_ = sender.SendSessionUpdate("s", acp.MessageChunkUpdate{SessionUpdate: acp.UpdateTypeAgentMessageChunk, Content: acp.ContentBlock{Type: "text", Text: "x"}})
	_ = sender.writeNamedEventJSON("operator_command", map[string]string{"a": "b"})
	_ = sender.FinishStream()
	_ = sender.SendError("x")
	if acp.OperatorCommandsOf(sender) != nil {
		t.Fatal("a detached sender still offers operator commands")
	}
}

func TestOperatorCommandAnswerRefusesTrailingJSON(t *testing.T) {
	rec := &syncBuffer{}
	sender := NewSender(&config.Config{}, rec, true, "m")
	sender.EnableOperatorCommands(true)
	sid := "sess_00112233445566778899aabb"
	_ = startOperatorRequest(t, sender, rec, acp.OperatorCommandRequest{SessionID: sid, RequestID: "oc_t", ToolCallID: "c", Command: "ls"})
	srv := &Server{}
	if w := postOperatorAnswer(t, srv, sid, `{"requestId":"oc_t","action":"decline"}{"requestId":"oc_t","action":"run"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a second object after the body: %d %s", w.Code, w.Body)
	}
	_ = postOperatorAnswer(t, srv, sid, `{"requestId":"oc_t","action":"decline"}`)
}
