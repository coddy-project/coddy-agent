package session

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// newConformanceManager is newTestManager with a logger, for the paths that
// record what they did.
func newConformanceManager(t *testing.T) *Manager {
	t.Helper()
	mgr, _ := newTestManager(t)
	mgr.log = slog.New(slog.DiscardHandler)
	return mgr
}

// codeOf returns the JSON-RPC code a handler error asks for.
func codeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("no error, want a coded one")
	}
	var coded *acp.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("error %v carries no code, want a *acp.CodedError", err)
	}
	return coded.Code
}

// A relative extra root is a malformed request, which ACP answers as invalid
// params rather than as an agent failure: the client got the shape wrong.
func TestSessionNewRejectsARelativeRootAsInvalidParams(t *testing.T) {
	mgr, _ := newTestManager(t)
	_, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{
		CWD:                   t.TempDir(),
		AdditionalDirectories: []string{"relative/root"},
	})
	if got := codeOf(t, err); got != acp.ErrInvalidParams {
		t.Fatalf("code = %d, want %d (%v)", got, acp.ErrInvalidParams, err)
	}
}

// An option the agent does not have is a value the method does not accept.
func TestSetConfigOptionRejectsAnUnknownOptionAsInvalidParams(t *testing.T) {
	mgr, _ := newTestManager(t)
	mgr.sessions = map[string]*State{"sess_1": {}}
	_, err := mgr.SetConfigOptionFrom(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: "sess_1",
		ConfigID:  "nonsense",
		Value:     "x",
	}, "acp")
	if got := codeOf(t, err); got != acp.ErrInvalidParams {
		t.Fatalf("code = %d, want %d (%v)", got, acp.ErrInvalidParams, err)
	}
}

// A session with no bundle on disk is the resource-not-found case ACP defines,
// and the client reads it as "reopen or drop this", not as an agent failure.
func TestSessionResumeOfAnUnknownSessionIsNotFound(t *testing.T) {
	mgr, _ := newTestManager(t)
	_, err := mgr.HandleSessionResume(context.Background(), acp.SessionResumeParams{
		SessionID: "sess_missing",
		CWD:       t.TempDir(),
	})
	if got := codeOf(t, err); got != acp.ErrResourceNotFound {
		t.Fatalf("code = %d, want %d (%v)", got, acp.ErrResourceNotFound, err)
	}
}

// Closing frees the live state and leaves the bundle alone, and it is
// idempotent: a session that is not live any more answers with the same empty
// object instead of an error.
func TestSessionCloseFreesTheStateAndIsIdempotent(t *testing.T) {
	mgr := newConformanceManager(t)
	mgr.sessions = map[string]*State{"sess_1": {}}

	if _, err := mgr.HandleSessionClose(context.Background(), acp.SessionCloseParams{SessionID: "sess_1"}); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if live := mgr.SessionByID("sess_1"); live != nil {
		t.Fatalf("session is still live after a close: %#v", live)
	}
	if _, err := mgr.HandleSessionClose(context.Background(), acp.SessionCloseParams{SessionID: "sess_1"}); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := mgr.HandleSessionClose(context.Background(), acp.SessionCloseParams{SessionID: "sess_never"}); err != nil {
		t.Fatalf("close of a session that was never open: %v", err)
	}
}

// An empty id is the one thing close refuses, as it names nothing to close.
func TestSessionCloseNeedsASessionID(t *testing.T) {
	mgr := newConformanceManager(t)
	_, err := mgr.HandleSessionClose(context.Background(), acp.SessionCloseParams{})
	if got := codeOf(t, err); got != acp.ErrInvalidParams {
		t.Fatalf("code = %d, want %d (%v)", got, acp.ErrInvalidParams, err)
	}
}
