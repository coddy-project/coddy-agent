//go:build http

package httpserver

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// readEventFrames reads SSE frames until one containing want has been read whole - up to
// its blank-line terminator, so the caller sees the frame's data line too.
func readEventFrames(t *testing.T, body *bufio.Reader, want string) string {
	t.Helper()
	var seen strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		line, err := body.ReadString('\n')
		if err != nil {
			t.Fatalf("read events (seen %q): %v", seen.String(), err)
		}
		seen.WriteString(line)
		if strings.Contains(seen.String(), want) && strings.HasSuffix(seen.String(), "\n\n") {
			return seen.String()
		}
	}
	t.Fatalf("timed out waiting for %q, seen %q", want, seen.String())
	return ""
}

func subscribeEvents(t *testing.T, ts *httptest.Server, query string) (*bufio.Reader, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/coddy/events"+query, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("events status %d", res.StatusCode)
	}
	return bufio.NewReader(res.Body), func() {
		cancel()
		res.Body.Close()
	}
}

// An idle client must be told a turn began in a session it is not driving; that is the
// whole reason the route exists.
func TestCoddyEventsStreamAnnouncesTurnLifecycle(t *testing.T) {
	turn := &watchedTurn{started: make(chan struct{}), release: make(chan struct{})}
	_, srv, _ := testHTTPServerPersistWithRunner(t, turn.runner())
	sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body, closeEvents := subscribeEvents(t, ts, "")
	defer closeEvents()
	readEventFrames(t, body, "event: ready")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = srv.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: sn.SessionID,
			Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
		})
	}()
	defer wg.Wait()

	<-turn.started
	started := readEventFrames(t, body, "turn_started")
	if !strings.Contains(started, sn.SessionID) {
		t.Fatalf("turn_started did not name the session: %s", started)
	}

	close(turn.release)
	readEventFrames(t, body, "turn_ended")
}

// A client that connects while a turn is already running would otherwise stay blind
// until that turn ended.
func TestCoddyEventsStreamSnapshotsRunningTurns(t *testing.T) {
	turn := &watchedTurn{started: make(chan struct{}), release: make(chan struct{})}
	_, srv, _ := testHTTPServerPersistWithRunner(t, turn.runner())
	sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = srv.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: sn.SessionID,
			Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
		})
	}()
	defer wg.Wait()
	defer close(turn.release)
	<-turn.started

	body, closeEvents := subscribeEvents(t, ts, "")
	defer closeEvents()

	snapshot := readEventFrames(t, body, "event: ready")
	if !strings.Contains(snapshot, "turn_started") || !strings.Contains(snapshot, sn.SessionID) {
		t.Fatalf("connect-time snapshot missed the running turn: %s", snapshot)
	}
}

// EventSource cannot set an Authorization header, so this route accepts the token the
// same way the composer stream does.
func TestCoddyEventsStreamAcceptsAccessTokenQuery(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	srv.extraAuthTokens = []string{"secret-token"}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := ts.Client().Get(ts.URL + "/coddy/events")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d without a token, want 401", res.StatusCode)
	}

	body, closeEvents := subscribeEvents(t, ts, "?access_token=secret-token")
	defer closeEvents()
	readEventFrames(t, body, "event: ready")
}

func TestTurnEventFrameShape(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	frame := string(turnEventFrame(session.TurnEvent{
		SessionID: "sess_x",
		Phase:     session.TurnPhaseEnded,
		At:        at,
	}))
	if !strings.HasPrefix(frame, "event: turn_ended\ndata: ") || !strings.HasSuffix(frame, "\n\n") {
		t.Fatalf("frame %q is not a well-formed SSE frame", frame)
	}
	for _, want := range []string{`"object":"coddy.turn_event"`, `"sessionId":"sess_x"`, `"phase":"ended"`, "2026-08-10T12:00:00Z"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame %q missing %s", frame, want)
		}
	}
}
