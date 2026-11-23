//go:build http

package httpserver

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestBulkDeleteSanitizesWrappedConflicts(t *testing.T) {
	for _, cause := range []error{session.ErrSessionTurnBusy, session.ErrForeignSessionTask, bgtask.ErrStopNotSettled} {
		conflict := &session.SessionDeleteConflict{SessionID: "sess_child", Cause: cause}
		err := fmt.Errorf("/private/secret: %w", conflict)
		if got := sessionDeleteStatus(err); got != http.StatusConflict {
			t.Fatalf("status = %d", got)
		}
		_, _, failed := bulkDeleteSessions([]string{"sess_parent"}, func(string) error { return err })
		if len(failed) != 1 || !strings.Contains(failed[0]["error"], "sess_child") || strings.Contains(failed[0]["error"], "/private") {
			t.Fatalf("failed = %v", failed)
		}
	}
}

func TestSessionDeleteFailureMessage(t *testing.T) {
	underlying := fmt.Errorf("secret /private/path")
	if got := sessionDeleteFailureMessage(underlying); got != "delete failed" {
		t.Fatal(got)
	}
	err := fmt.Errorf("wrapped: %w", &session.SessionRemovalError{SessionID: "sess_x", Cause: underlying})
	if got := sessionDeleteFailureMessage(err); got != "delete failed; the session may be partially deleted" {
		t.Fatal(got)
	}
}
