//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// What a request is answered when its session cannot be resolved or created:
// each cause has its own status and message, and a full disk is told apart
// from every other failure by the code the operating system returned, however
// many layers the manager wrapped it in.
func TestWriteSessionErrorAnswersEachCauseWithItsOwnStatus(t *testing.T) {
	layoutFailure := func(errno error) error {
		return fmt.Errorf("session/new: layout: %w", &os.PathError{Op: "mkdir", Path: "/home/coddy/sessions/sess_x", Err: errno})
	}
	cases := []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"unknown session", errSessionNotFound, http.StatusNotFound, "session not found"},
		{"malformed id", fmt.Errorf("resolve: %w", errInvalidSessionHeader), http.StatusBadRequest, "invalid X-Coddy-Session-ID"},
		{"refused origin", errInvalidOriginHeader, http.StatusBadRequest, errInvalidOriginHeader.Error()},
		{"disk full", layoutFailure(diskFullErrno), http.StatusInsufficientStorage, session.DiskFullMessage},
		{"disk full, wrapped again", fmt.Errorf("session load failed: %w", layoutFailure(diskFullErrno)), http.StatusInsufficientStorage, session.DiskFullMessage},
		{"disk full among others", errors.Join(errors.New("first"), layoutFailure(diskFullErrno)), http.StatusInsufficientStorage, session.DiskFullMessage},
		{"permission denied", layoutFailure(syscall.EACCES), http.StatusInternalServerError, "session unavailable"},
		{"anything else", errors.New("boom"), http.StatusInternalServerError, "session unavailable"},
		{"text alone is not a code", errors.New("no space left on device"), http.StatusInternalServerError, "session unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeSessionError(rec, tc.err)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.status, rec.Body)
			}
			var body struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not the error envelope: %v\n%s", err, rec.Body)
			}
			if body.Error.Message != tc.message {
				t.Fatalf("message = %q, want %q", body.Error.Message, tc.message)
			}
		})
	}
}

// The message names the cause in the words a client can match on, on every
// platform, and is not the operating system's own text (Windows words it
// differently).
func TestDiskFullMessageNamesTheCause(t *testing.T) {
	if !strings.Contains(session.DiskFullMessage, "no space left on device") {
		t.Fatalf("DiskFullMessage = %q, want it to say no space is left on the device", session.DiskFullMessage)
	}
}
