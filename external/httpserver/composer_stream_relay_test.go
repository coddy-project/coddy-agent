//go:build http

package httpserver

import (
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
)

func TestComposerStreamRelayReplayAndLive(t *testing.T) {
	r := newComposerStreamRelay()
	// The relay records whole SSE frames, so the test speaks in frames too.
	if _, err := r.Write([]byte("data: alpha\n\n")); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := r.serveSubscriber(context.Background(), rec); err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := r.Write([]byte("data: beta\n\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	r.Close()
	wg.Wait()
	body := rec.Body.String()
	if !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") {
		t.Fatalf("body %q", body)
	}
}

func TestCoddySessionComposerStreamDeliversRelay(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	ctx := context.Background()
	sn, err := srv.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := sn.SessionID

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	rel := srv.beginComposerRelay(sid)
	if _, err := rel.Write([]byte("data: [DONE]\n\n")); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(40 * time.Millisecond)
		srv.endComposerRelay(sid, rel)
	}()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/coddy/sessions/"+url.PathEscape(sid)+"/composer-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Coddy-Session-ID", sid)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "[DONE]") {
		t.Fatalf("missing done: %s", b)
	}
}
