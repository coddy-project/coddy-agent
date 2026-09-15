package remote

// Edges of the background-subagent prompts a console attached over --remote
// hears about on the server's event stream (detached_prompts.go). The happy
// path is in features/cli_remote.feature.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

type postedAnswer struct {
	sessionID  string
	toolCallID string
	optionID   string
}

// promptEventsServer serves GET /coddy/events from a channel of frames and
// records every permission answer posted back.
type promptEventsServer struct {
	ts      *httptest.Server
	frames  chan string
	answers chan postedAnswer
}

func newPromptEventsServer(t *testing.T) *promptEventsServer {
	t.Helper()
	s := &promptEventsServer{frames: make(chan string, 16), answers: make(chan postedAnswer, 16)}
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/coddy/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fl, _ := w.(http.Flusher)
			_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
			if fl != nil {
				fl.Flush()
			}
			for {
				select {
				case frame := <-s.frames:
					_, _ = fmt.Fprint(w, frame)
					if fl != nil {
						fl.Flush()
					}
				case <-r.Context().Done():
					return
				}
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/permission"):
			body, _ := io.ReadAll(r.Body)
			var in struct {
				ToolCallID string `json:"toolCallId"`
				OptionID   string `json:"optionId"`
			}
			_ = json.Unmarshal(body, &in)
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/coddy/sessions/"), "/permission")
			s.answers <- postedAnswer{sessionID: id, toolCallID: in.ToolCallID, optionID: in.OptionID}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.ts.Close)
	return s
}

func askedFrame(parentID, childID, toolCallID, agentName string) string {
	body, _ := json.Marshal(map[string]interface{}{
		"object":          "coddy.subagent_permission",
		"phase":           "asked",
		"parentSessionId": parentID,
		"childSessionId":  childID,
		"taskId":          "bg_1",
		"toolCallId":      toolCallID,
		"agentName":       agentName,
		"request": acp.PermissionRequestParams{
			SessionID: childID,
			ToolCall:  acp.PermissionToolCall{ToolCallID: toolCallID, Title: "[subagent " + agentName + "] Run: echo checked"},
			Options: []acp.PermissionOption{
				{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
				{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
			},
		},
	})
	return "event: subagent_permission\ndata: " + string(body) + "\n\n"
}

func settledFrame(parentID, childID, toolCallID string) string {
	body, _ := json.Marshal(map[string]interface{}{
		"object": "coddy.subagent_permission", "phase": "settled",
		"parentSessionId": parentID, "childSessionId": childID, "toolCallId": toolCallID,
	})
	return "event: subagent_permission\ndata: " + string(body) + "\n\n"
}

// askingSender stands in for the console: it records what it was asked and
// either answers at once or waits for the test to release it.
type askingSender struct {
	collectSender
	asked   chan acp.PermissionRequestParams
	release chan *acp.PermissionResult
	gaveUp  chan struct{}
}

func newAskingSender(waits bool) *askingSender {
	s := &askingSender{asked: make(chan acp.PermissionRequestParams, 8), gaveUp: make(chan struct{}, 8)}
	if waits {
		s.release = make(chan *acp.PermissionResult, 1)
	}
	return s
}

func (s *askingSender) RequestPermission(ctx context.Context, p acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.asked <- p
	if s.release == nil {
		return &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}, nil
	}
	select {
	case res := <-s.release:
		return res, nil
	case <-ctx.Done():
		s.gaveUp <- struct{}{}
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
}

func startPromptHandler(t *testing.T, srv *promptEventsServer, sender acp.UpdateSender, known ...string) *Handler {
	t.Helper()
	h, err := NewHandler(Options{BaseURL: srv.ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	h.SetServer(sender)
	for _, id := range known {
		h.session(id)
	}
	h.StartEvents()
	t.Cleanup(h.Close)
	return h
}

const promptWait = 5 * time.Second

func TestReconnectRemovesOnlyPromptsMissingFromSnapshot(t *testing.T) {
	for _, tc := range []struct{ replay, complete bool }{{false, true}, {true, true}, {false, false}} {
		t.Run(fmt.Sprintf("replayed=%v/complete=%v", tc.replay, tc.complete), func(t *testing.T) {
			var streamMu sync.Mutex
			stream := askedFrame("sess_parent", "sess_child", "call_1", "writer") + "event: ready\ndata: {}\n\n"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/coddy/events" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				streamMu.Lock()
				defer streamMu.Unlock()
				_, _ = io.WriteString(w, stream)
			}))
			defer srv.Close()
			h, err := NewHandler(Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			sender := newAskingSender(true)
			h.SetServer(sender)
			h.session("sess_parent")
			if err := h.readEventsOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-sender.asked:
			case <-time.After(promptWait):
				t.Fatal("prompt not shown")
			}
			streamMu.Lock()
			if !tc.replay {
				stream = "event: ready\ndata: {}\n\n"
			}
			if !tc.complete {
				stream = ""
			}
			streamMu.Unlock()
			if err := h.readEventsOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			h.detachedMu.Lock()
			_, retained := h.detached[detachedPromptKey("sess_child", "call_1")]
			h.detachedMu.Unlock()
			wantRetained := tc.replay || !tc.complete
			if retained != wantRetained {
				t.Fatalf("prompt retained=%v, want=%v", retained, wantRetained)
			}
			if !wantRetained {
				select {
				case <-sender.gaveUp:
				case <-time.After(promptWait):
					t.Fatal("obsolete modal not cancelled")
				}
			}
			select {
			case <-sender.asked:
				t.Fatal("replay opened a duplicate prompt")
			default:
			}
		})
	}
}

// A prompt of a session this console opened is asked here and answered on the
// child session; a prompt of somebody else's session - and a replay of one
// already shown - is not asked at all.
func TestAPromptOfAnOpenedSessionIsAnsweredOnTheChild(t *testing.T) {
	srv := newPromptEventsServer(t)
	sender := newAskingSender(false)
	startPromptHandler(t, srv, sender, "sess_parent")

	srv.frames <- askedFrame("sess_stranger", "sess_other", "call_2", "reviewer")
	srv.frames <- askedFrame("sess_parent", "sess_child", "call_1", "writer")
	srv.frames <- askedFrame("sess_parent", "sess_child", "call_1", "writer")

	select {
	case p := <-sender.asked:
		if p.SessionID != "sess_child" || p.ToolCall.ToolCallID != "call_1" || !strings.Contains(p.ToolCall.Title, "[subagent writer]") {
			t.Fatalf("the console was asked %+v", p)
		}
	case <-time.After(promptWait):
		t.Fatal("the console was never asked")
	}
	select {
	case a := <-srv.answers:
		if a != (postedAnswer{sessionID: "sess_child", toolCallID: "call_1", optionID: "allow"}) {
			t.Fatalf("posted %+v", a)
		}
	case <-time.After(promptWait):
		t.Fatal("the answer was never posted")
	}
	select {
	case p := <-sender.asked:
		t.Fatalf("the console was asked a second time: %+v", p)
	case <-time.After(200 * time.Millisecond):
	}
}

// Answered in a browser first: the server announces the prompt settled, the
// console takes its modal down, and nothing is posted.
func TestAPromptSettledElsewhereIsTakenDownUnanswered(t *testing.T) {
	srv := newPromptEventsServer(t)
	sender := newAskingSender(true)
	startPromptHandler(t, srv, sender, "sess_parent")

	srv.frames <- askedFrame("sess_parent", "sess_child", "call_1", "writer")
	select {
	case <-sender.asked:
	case <-time.After(promptWait):
		t.Fatal("the console was never asked")
	}
	srv.frames <- settledFrame("sess_parent", "sess_child", "call_1")
	select {
	case <-sender.gaveUp:
	case <-time.After(promptWait):
		t.Fatal("the prompt stayed up after it was settled elsewhere")
	}
	select {
	case a := <-srv.answers:
		t.Fatalf("a settled prompt was answered: %+v", a)
	case <-time.After(200 * time.Millisecond):
	}
}

// The server announced the prompt before this console opened its session; it
// is shown once the session is opened.
func TestAPromptAskedBeforeTheSessionWasOpenedIsShownOnOpen(t *testing.T) {
	srv := newPromptEventsServer(t)
	sender := newAskingSender(false)
	h := startPromptHandler(t, srv, sender)

	srv.frames <- askedFrame("sess_later", "sess_child", "call_1", "writer")
	deadline := time.Now().Add(promptWait)
	for {
		h.detachedMu.Lock()
		_, recorded := h.detached[detachedPromptKey("sess_child", "call_1")]
		h.detachedMu.Unlock()
		if recorded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the announced prompt was never recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case p := <-sender.asked:
		t.Fatalf("a prompt of an unopened session was asked: %+v", p)
	default:
	}

	h.session("sess_later")
	h.offerDetachedPromptsFor("sess_later")
	select {
	case <-sender.asked:
	case <-time.After(promptWait):
		t.Fatal("opening the session did not show its waiting prompt")
	}
	select {
	case a := <-srv.answers:
		if a.sessionID != "sess_child" || a.optionID != "allow" {
			t.Fatalf("posted %+v", a)
		}
	case <-time.After(promptWait):
		t.Fatal("the answer was never posted")
	}
}
