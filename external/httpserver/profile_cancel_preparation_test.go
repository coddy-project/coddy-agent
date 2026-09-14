//go:build http

package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// pauseProfileHeaders parks the handler after its turn lock is acquired but
// before preparation and manager admission. The cancelling client has its own
// response writer, so it can send Stop while the first request remains open.
type pauseProfileHeaders struct {
	*httptest.ResponseRecorder
	started chan struct{}
	release chan struct{}
	once    sync.Once
	locked  func() bool
}

func (w *pauseProfileHeaders) Header() http.Header {
	header := w.ResponseRecorder.Header()
	if w.locked() {
		w.once.Do(func() {
			close(w.started)
			<-w.release
		})
	}
	return header
}

func TestCancelDuringProfilePreparation(t *testing.T) {
	for _, tc := range []struct{ name, path, body string }{
		{"responses", "/v1/responses", `{"model":"agent","input":"do not start model work","stream":true}`},
		{"chat_completions", "/v1/chat/completions", `{"model":"agent","messages":[{"role":"user","content":"do not start model work"}],"stream":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan error, 1)
			runner := func(ctx context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
				entered <- ctx.Err()
				if ctx.Err() != nil {
					return string(acp.StopReasonCancelled), nil
				}
				return string(acp.StopReasonEndTurn), nil
			}
			mgr, srv, _ := testHTTPServerPersistWithRunner(t, runner)
			sn, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			writer := &pauseProfileHeaders{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{})}
			writer.locked = func() bool {
				unlock, err := mgr.AcquireComposerTurnLock(sn.SessionID, mgr.SessionByID(sn.SessionID))
				if errors.Is(err, session.ErrSessionTurnBusy) {
					return true
				}
				if err != nil {
					t.Errorf("probe turn lock: %v", err)
					return false
				}
				unlock()
				return false
			}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(writer.release) }) }
			done := make(chan struct{})
			t.Cleanup(func() {
				release()
				mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: sn.SessionID})
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("profile handler did not finish")
				}
			})
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Coddy-Session-ID", sn.SessionID)
			go func() {
				defer close(done)
				srv.Handler().ServeHTTP(writer, req)
			}()
			select {
			case <-writer.started:
			case <-time.After(5 * time.Second):
				t.Fatal("profile never reached preparation")
			}

			cancelReply := httptest.NewRecorder()
			srv.Handler().ServeHTTP(cancelReply, httptest.NewRequest(http.MethodPost, "/coddy/sessions/"+sn.SessionID+"/cancel", nil))
			if cancelReply.Code != http.StatusOK {
				t.Fatalf("Stop answered %d: %s", cancelReply.Code, cancelReply.Body.String())
			}
			release()
			select {
			case err := <-entered:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Stop during preparation was lost: runner entered with context error %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("runner did not receive the admitted context")
			}
		})
	}
}
