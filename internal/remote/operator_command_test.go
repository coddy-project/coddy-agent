package remote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// operatorConsole is the local console of a remote session: it shows the
// request, runs it, and presses Stop a moment later.
type operatorConsole struct {
	collectSender
	mu     sync.Mutex
	shown  []acp.OperatorCommandRequest
	ticket *acp.OperatorCommandTicket
	ended  chan struct{}
}

func (c *operatorConsole) OperatorCommandsAvailable() bool { return true }

func (c *operatorConsole) RequestOperatorCommand(ctx context.Context, req acp.OperatorCommandRequest) (*acp.OperatorCommandDecision, error) {
	tk := acp.NewOperatorCommandTicket()
	c.mu.Lock()
	c.shown = append(c.shown, req)
	c.ticket = tk
	c.mu.Unlock()
	_ = tk.Answer(acp.OperatorActionRun, "")
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = tk.Answer(acp.OperatorActionStop, "")
		<-tk.Finished()
		close(c.ended)
	}()
	return tk.Wait(ctx)
}

// TestRemoteConsoleRunsAndStopsAnOperatorCommand: the remote console declares
// it answers operator commands, shows the server's request to its operator,
// posts Run, forwards the operator's Stop, and ends the local request when the
// server's tool call ends.
func TestRemoteConsoleRunsAndStopsAnOperatorCommand(t *testing.T) {
	remoteOperatorRunAndStop(t, http.StatusNoContent, 0)
}

// TestRemoteConsoleStopsARunWhoseAnswerStalls: a proxy holds the Run's answer
// while the command runs on the server; the operator's Stop is not held
// behind it.
func TestRemoteConsoleStopsARunWhoseAnswerStalls(t *testing.T) {
	old := operatorAnswerTimeout
	operatorAnswerTimeout = 200 * time.Millisecond
	t.Cleanup(func() { operatorAnswerTimeout = old })
	remoteOperatorRunAndStop(t, http.StatusNoContent, 8*time.Second)
}

// TestRemoteConsoleStopsARunWhoseAnswerWasLost: the server took the Run but
// its answer never came back (a dropped connection, a proxy's 502); the
// command may be running there, so the operator's Stop is still forwarded.
func TestRemoteConsoleStopsARunWhoseAnswerWasLost(t *testing.T) {
	remoteOperatorRunAndStop(t, http.StatusBadGateway, 0)
}

func remoteOperatorRunAndStop(t *testing.T, runStatus int, runDelay time.Duration) {
	t.Helper()
	answers := make(chan map[string]string, 4)
	declared := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Metadata map[string]string `json:"metadata"`
		}
		_ = json.Unmarshal(raw, &body)
		declared <- body.Metadata["coddy_operator_commands"]
		w.Header().Set("Content-Type", "text/event-stream")
		flush := func(s string) {
			_, _ = w.Write([]byte(s))
			w.(http.Flusher).Flush()
		}
		flush("event: tool_call\ndata: {\"sessionUpdate\":\"tool_call\",\"toolCallId\":\"call_1\",\"title\":\"operator_command\",\"status\":\"pending\"}\n\n")
		flush("event: operator_command\ndata: {\"sessionId\":\"sess_test\",\"requestId\":\"oc_1\",\"toolCallId\":\"call_1\",\"command\":\"make deploy\",\"shell\":\"bash\",\"host\":\"srv\",\"cwd\":\"/w\"}\n\n")
		for i := 0; i < 2; i++ {
			select {
			case a := <-answers:
				if i == 0 && a["action"] != "run" || i == 1 && a["action"] != "stop" {
					t.Errorf("answer %d = %v", i, a)
				}
			case <-time.After(5 * time.Second):
				t.Errorf("answer %d never came", i)
				return
			}
		}
		flush("event: tool_call_update\ndata: {\"sessionUpdate\":\"tool_call_update\",\"toolCallId\":\"call_1\",\"status\":\"completed\",\"content\":[{\"type\":\"content\",\"content\":{\"type\":\"text\",\"text\":\"The operator stopped the command\"}}]}\n\n")
		flush("data: [DONE]\n\n")
	})
	mux.HandleFunc("POST /coddy/sessions/{id}/operator-command", func(w http.ResponseWriter, r *http.Request) {
		var a map[string]string
		_ = json.NewDecoder(r.Body).Decode(&a)
		answers <- a
		if a["action"] == "run" {
			select {
			case <-time.After(runDelay):
			case <-r.Context().Done():
			}
			w.WriteHeader(runStatus)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	console := &operatorConsole{ended: make(chan struct{})}
	h.SetServer(console)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := h.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: "sess_test",
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "deploy"}},
	}, console, nil); err != nil {
		t.Fatal(err)
	}
	if got := <-declared; got != "1" {
		t.Fatalf("metadata.coddy_operator_commands = %q, want 1", got)
	}
	console.mu.Lock()
	shown := append([]acp.OperatorCommandRequest(nil), console.shown...)
	console.mu.Unlock()
	if len(shown) != 1 || shown[0].Command != "make deploy" || shown[0].Host != "srv" {
		t.Fatalf("shown = %+v", shown)
	}
	select {
	case <-console.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the local request did not end with the server's tool call")
	}
}

// TestRemoteConsoleWithoutOperatorDeclaresNothing: a surface that cannot
// answer never declares the capability, so the server never offers the tool.
func TestRemoteConsoleWithoutOperatorDeclaresNothing(t *testing.T) {
	declared := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Metadata map[string]string `json:"metadata"`
		}
		_ = json.Unmarshal(raw, &body)
		declared <- body.Metadata["coddy_operator_commands"]
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	if _, err := promptOnce(t, srv, &collectSender{}); err != nil {
		t.Fatal(err)
	}
	if got := <-declared; got != "" {
		t.Fatalf("metadata.coddy_operator_commands = %q, want none", got)
	}
}

// decliningConsole declines the server's request.
type decliningConsole struct {
	collectSender
}

func (c *decliningConsole) OperatorCommandsAvailable() bool { return true }

func (c *decliningConsole) RequestOperatorCommand(ctx context.Context, req acp.OperatorCommandRequest) (*acp.OperatorCommandDecision, error) {
	tk := acp.NewOperatorCommandTicket()
	_ = tk.Answer(acp.OperatorActionDecline, "")
	return tk.Wait(ctx)
}

// TestRemoteConsoleSendsALostDeclineAgain: a decline the server never got
// would leave the turn waiting with no prompt left on the console.
func TestRemoteConsoleSendsALostDeclineAgain(t *testing.T) {
	old := operatorDeclineRetry
	operatorDeclineRetry = 10 * time.Millisecond
	t.Cleanup(func() { operatorDeclineRetry = old })
	var mu sync.Mutex
	posts := 0
	declined := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flush := func(s string) {
			_, _ = w.Write([]byte(s))
			w.(http.Flusher).Flush()
		}
		flush("event: operator_command\ndata: {\"sessionId\":\"sess_test\",\"requestId\":\"oc_1\",\"toolCallId\":\"call_1\",\"command\":\"make deploy\"}\n\n")
		select {
		case <-declined:
		case <-time.After(5 * time.Second):
			t.Error("the decline never reached the server")
		}
		flush("data: [DONE]\n\n")
	})
	mux.HandleFunc("POST /coddy/sessions/{id}/operator-command", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		posts++
		n := posts
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		close(declined)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	console := &decliningConsole{}
	h.SetServer(console)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := h.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: "sess_test",
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "deploy"}},
	}, console, nil); err != nil {
		t.Fatal(err)
	}
}
