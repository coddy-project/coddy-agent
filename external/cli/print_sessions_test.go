//go:build cli

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Edge cases of print runs and their sessions; the happy path is
// features/print_sessions.feature.

func TestEphemeralIsRefusedWhereThereIsNoRunOfItsOwnToDelete(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"interactive console", []string{"--ephemeral"}, "-p"},
		{"continue", []string{"--ephemeral", "-c", "-p", "hi"}, "--continue"},
		{"named session", []string{"--ephemeral", "--session-id", "sess_kept", "-p", "hi"}, "--session-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newPromptInputRun(t)
			err := r.run(tc.args...)
			if err == nil || !strings.Contains(err.Error(), "--ephemeral") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want a refusal naming --ephemeral and %s", err, tc.want)
			}
			if calls := r.model.calls(); len(calls) != 0 {
				t.Fatalf("a refused run reached the model %d times", len(calls))
			}
			if entries, _ := os.ReadDir(filepath.Join(r.home, "sessions")); len(entries) != 0 {
				t.Fatalf("a refused run left %d entries in the sessions root", len(entries))
			}
		})
	}
}

// A run that fails after its session exists still removes it: the cleanup is
// not on the success path only.
func TestEphemeralRunRemovesItsSessionWhenASettingFails(t *testing.T) {
	r := newPromptInputRun(t)
	err := r.run("--ephemeral", "--model", "nosuch/model", "-p", "hi")
	if err == nil || !strings.Contains(err.Error(), "--model") {
		t.Fatalf("err = %v, want the --model failure", err)
	}
	store := &session.FileStore{Root: filepath.Join(r.home, "sessions")}
	rows, lerr := store.ListSnapshotsWith(session.ListOptions{IncludePrintRuns: true, Archived: session.ArchiveAll})
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(rows) != 0 {
		t.Fatalf("the failed ephemeral run left %d sessions", len(rows))
	}
}

// A print run that is not ephemeral keeps its session even when it fails, so
// a script can look at it or continue it.
func TestAFailedPrintRunKeepsItsMarkedSession(t *testing.T) {
	r := newPromptInputRun(t)
	if err := r.run("--model", "nosuch/model", "-p", "hi"); err == nil {
		t.Fatal("want the --model failure")
	}
	store := &session.FileStore{Root: filepath.Join(r.home, "sessions")}
	rows, err := store.ListSnapshotsWith(session.ListOptions{Origin: session.OriginPrint})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("print runs stored = %d, want 1", len(rows))
	}
}

// latestRemoteStub stands in for *remote.Handler: no local store, and the
// newest session asked of the server.
type latestRemoteStub struct {
	backend
	asked []bool
}

func (r *latestRemoteStub) FileStore() *session.FileStore { return nil }

func (r *latestRemoteStub) LatestSessionID(_ context.Context, includePrint bool) (string, error) {
	r.asked = append(r.asked, includePrint)
	return "sess_remote_latest", nil
}

func TestRemoteContinueAsksForPrintRunsOnlyFromAPrintRun(t *testing.T) {
	stub := &latestRemoteStub{}
	for _, include := range []bool{true, false} {
		id, err := latestBackendSessionID(context.Background(), stub, "/ignored/on/a/remote", include)
		if err != nil {
			t.Fatal(err)
		}
		if id != "sess_remote_latest" {
			t.Fatalf("id = %q", id)
		}
	}
	if len(stub.asked) != 2 || !stub.asked[0] || stub.asked[1] {
		t.Fatalf("asked the server with includePrint %v, want [true false]", stub.asked)
	}
}
