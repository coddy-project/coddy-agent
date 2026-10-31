package hooks

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func approvedScriptSource(t *testing.T, cwd, command string, async bool) *Source {
	t.Helper()
	data := []byte(fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":%q,"async":%t}]}]}}`, command, async))
	def, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return &Source{Scope: ScopeProject, Trust: TrustTrusted, Definition: def, data: data, cwd: cwd, Digest: digestWithScripts(data, def, cwd)}
}

func TestQueuedHookRechecksApprovalAfterItsSlotOpens(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook here is a POSIX shell line")
	}
	cwd := t.TempDir()
	script, marker := filepath.Join(cwd, "check.sh"), filepath.Join(cwd, "ran")
	if err := os.WriteFile(script, []byte("exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := approvedScriptSource(t, cwd, "sh ./check.sh", true)
	logged := make(chan struct{}, 1)
	r := Runner{Sources: []*Source{src}, Session: Session{CWD: cwd}, TimeoutSeconds: 1, Log: slog.New(slog.NewTextHandler(approvalLogSignal{logged}, nil))}
	for range cap(asyncSlots) {
		asyncSlots <- struct{}{}
	}
	released := false
	defer func() {
		if !released {
			<-asyncSlots
		}
		// Recover the queued worker's slot before draining the held permits.
		select {
		case asyncSlots <- struct{}{}:
		case <-time.After(5 * time.Second):
			t.Error("the queued hook did not release its slot")
			return
		}
		for range cap(asyncSlots) {
			<-asyncSlots
		}
	}()
	if out := r.Run(context.Background(), Event{Name: EventSessionStart}); out.Ran != 1 {
		t.Fatalf("queued handlers = %d, want 1", out.Ran)
	}
	// A failure also logs on the vulnerable path, so the test waits for the
	// worker to finish deciding rather than inferring success from a delay.
	if err := os.WriteFile(script, []byte("echo changed > ./ran\nsleep 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	<-asyncSlots
	released = true
	select {
	case <-logged:
	case <-time.After(5 * time.Second):
		t.Fatal("the queued hook neither refused nor completed")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the changed queued script executed: %v", err)
	}
	if src.Runnable() {
		t.Fatal("the changed source kept its approval")
	}
}

type approvalLogSignal struct{ logged chan<- struct{} }

func (s approvalLogSignal) Write(p []byte) (int, error) {
	select {
	case s.logged <- struct{}{}:
	default:
	}
	return len(p), nil
}

func TestEarlierHandlerCannotReplaceALaterApprovedScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook here is a POSIX shell line")
	}
	cwd := t.TempDir()
	script, marker := filepath.Join(cwd, "check.sh"), filepath.Join(cwd, "ran")
	if err := os.WriteFile(script, []byte("exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := approvedScriptSource(t, cwd, "sh ./check.sh", false)
	// The first source is operator-owned. Both sources pass the initial
	// match, but its handler changes the project script before dispatch.
	first := &Source{Trust: TrustTrusted, Definition: &Definition{Events: map[string][]Group{
		EventSessionStart: {{Handlers: []Handler{{Command: "printf 'touch ./ran\\n' > ./check.sh"}}}},
	}}}
	r := Runner{Sources: []*Source{first, src}, Session: Session{CWD: cwd}}
	if out := r.Run(context.Background(), Event{Name: EventSessionStart}); out.Ran != 1 || len(out.Errors) != 0 {
		t.Fatalf("only the first handler should run: %+v", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the changed later script executed: %v", err)
	}
	if src.Runnable() {
		t.Fatal("the changed source kept its approval")
	}
}
