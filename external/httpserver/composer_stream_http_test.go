//go:build http

package httpserver

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A client re-attaching to a session with nothing running must hear so at once. Waiting out
// the full pending window instead leaves the panel on a spinner for half a minute before it
// can fall back to the persisted transcript.
func TestComposerStreamAnswersImmediatelyWhenNoTurnRuns(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	start := time.Now()
	res, err := ts.Client().Get(ts.URL + "/coddy/sessions/" + url.PathEscape(sn.SessionID) + "/composer-stream")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("idle session waited %s before answering", elapsed)
	}
	if got := string(body); !strings.Contains(got, `"error"`) ||
		!strings.Contains(got, "no active composer stream") ||
		!strings.Contains(got, `"no_active_stream"`) {
		t.Fatalf("body %q: want an OpenAI-shaped error envelope carrying the no_active_stream code", body)
	}
}

// While a turn is running but has not registered a relay yet, the endpoint must hold the
// connection open and keep it alive rather than declaring there is nothing to watch.
func TestComposerStreamWaitsWhileTurnActive(t *testing.T) {
	runBlock := make(chan struct{})
	cont := make(chan struct{})
	var once sync.Once
	runner := func(_ context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		once.Do(func() { close(runBlock) })
		<-cont
		return string(acp.StopReasonEndTurn), nil
	}
	_, srv, _ := testHTTPServerPersistWithRunner(t, runner)
	sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := sn.SessionID

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = srv.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: sid,
			Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
		})
	}()
	defer func() {
		close(cont)
		wg.Wait()
	}()
	<-runBlock

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/coddy/sessions/"+url.PathEscape(sid)+"/composer-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	line, err := bufio.NewReader(res.Body).ReadString('\n')
	if err != nil {
		t.Fatalf("read first frame: %v", err)
	}
	if !strings.HasPrefix(line, ": composer stream pending") {
		t.Fatalf("first frame %q, want the pending comment while the turn runs", line)
	}
}
