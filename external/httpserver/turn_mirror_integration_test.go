//go:build http

package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type mirrorIntegrationTurn struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (t *mirrorIntegrationTurn) finish() {
	t.once.Do(func() { close(t.release) })
}

// Two HTTP surfaces of one desktop process share a manager and the runtime's
// mirror set. A turn posted to either one must be watchable through the other,
// without replacing or duplicating the posting server's own relay.
func TestHTTPTurnMirrorsBetweenTwoRealServers(t *testing.T) {
	home := t.TempDir()
	log := slog.New(slog.DiscardHandler)
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: home},
		Providers: []config.ProviderConfig{{Name: "stub", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "stub/model"}},
		Agent:     config.Agent{Model: "stub/model"},
	}
	turn := &mirrorIntegrationTurn{started: make(chan struct{}, 1), release: make(chan struct{})}
	runner := func(ctx context.Context, st *session.State, _ []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		select {
		case turn.started <- struct{}{}:
		default:
		}
		select {
		case <-turn.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "mirrored answer"})
		if err := sender.SendSessionUpdate(st.ID, acp.MessageChunkUpdate{
			SessionUpdate: "agent_message_chunk",
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "mirrored answer"},
		}); err != nil {
			return "", err
		}
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, log, home, &session.FileStore{Root: home})
	rt := &serve.Runtime{Mgr: mgr, Log: log}
	defer turn.finish()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type runningServer struct {
		srv  *Server
		addr string
		done <-chan error
	}
	servers := make([]runningServer, 0, 2)
	for i := 0; i < 2; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}
		ready := make(chan *Server, 1)
		done := make(chan error, 1)
		withdraw := func() {}
		go func() {
			done <- Serve(ctx, Options{
				Cfg: cfg, Mgr: mgr, Log: log, DefaultCWD: home, Home: home,
				ListenAddr: addr, Mirror: rt, Wakes: rt,
				OnServer: func(s *Server) {
					if s == nil {
						withdraw()
						return
					}
					withdraw = rt.AddTurnMirror(s)
					ready <- s
				},
			})
		}()
		var srv *Server
		select {
		case srv = <-ready:
		case err := <-done:
			t.Fatalf("server %d stopped during startup: %v", i, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("server %d did not start", i)
		}
		servers = append(servers, runningServer{srv: srv, addr: addr, done: done})
	}
	defer func() {
		cancel()
		turn.finish()
		for i, srv := range servers {
			select {
			case err := <-srv.done:
				if err != nil {
					t.Errorf("server %d: %v", i, err)
				}
			case <-time.After(5 * time.Second):
				t.Errorf("server %d did not stop", i)
			}
		}
	}()
	for i, srv := range servers {
		deadline := time.Now().Add(5 * time.Second)
		for {
			res, err := http.Get("http://" + srv.addr + "/v1/models")
			if err == nil {
				_ = res.Body.Close()
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("server %d is not accepting requests: %v", i, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: home})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := created.SessionID
	postDone := make(chan struct{})
	var postBody string
	var postErr error
	go func() {
		defer close(postDone)
		req, err := http.NewRequest(http.MethodPost, "http://"+servers[0].addr+"/v1/responses", strings.NewReader(`{"model":"agent","input":"hello","stream":false}`))
		if err != nil {
			postErr = err
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", sessionID)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			postErr = err
			return
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			postErr = err
			return
		}
		if res.StatusCode != http.StatusOK {
			postErr = &httpStatusError{status: res.StatusCode, body: string(body)}
			return
		}
		postBody = string(body)
	}()
	select {
	case <-turn.started:
	case <-postDone:
		t.Fatalf("the turn ended before the provider blocked: %v", postErr)
	case <-time.After(5 * time.Second):
		t.Fatal("the provider was not called")
	}

	rel := servers[1].srv.peekComposerRelay(sessionID)
	if rel == nil {
		turn.finish()
		<-postDone
		t.Fatal("the second real server has no relay for the first server's HTTP turn")
	}
	watchDone := make(chan struct{})
	var watched string
	var watchErr error
	go func() {
		defer close(watchDone)
		res, err := http.Get("http://" + servers[1].addr + "/coddy/sessions/" + sessionID + "/composer-stream")
		if err != nil {
			watchErr = err
			return
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		watched, watchErr = string(body), err
	}()
	if err := waitForSubscriber(rel); err != nil {
		turn.finish()
		t.Fatal(err)
	}
	turn.finish()
	select {
	case <-postDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the posting HTTP turn did not finish")
	}
	if postErr != nil {
		t.Fatal(postErr)
	}
	select {
	case <-watchDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the second server's watcher did not finish")
	}
	if watchErr != nil {
		t.Fatal(watchErr)
	}
	if got := strings.Count(watched, "mirrored answer"); got != 1 {
		t.Fatalf("the second server received the answer %d times, want once; stream:\n%s", got, watched)
	}
	if got := strings.Count(postBody, "mirrored answer"); got != 1 {
		t.Fatalf("the posting server received the answer %d times, want once; response: %s", got, postBody)
	}
	for i, srv := range servers {
		if rel := srv.srv.peekComposerRelay(sessionID); rel != nil {
			t.Errorf("server %d kept a relay after the turn ended", i)
		}
	}
}

type httpStatusError struct {
	status int
	body   string
}

func (e *httpStatusError) Error() string {
	return http.StatusText(e.status) + ": " + e.body
}
