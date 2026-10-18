package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// muxUnderTest sets up a multiplexer of its own for one test: a tmux server on
// a socket no one else uses, a screen session name no one else has, both torn
// down afterwards, so the tests never touch the operator's own sessions.
func muxUnderTest(t *testing.T, name string) operatorMux {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no terminal multiplexer on Windows")
	}
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not installed", name)
	}
	tag := fmt.Sprintf("coddy-test-%d-%d", os.Getpid(), time.Now().UnixNano()%1e9)
	env := &tooling.Env{SessionDir: t.TempDir()}
	req := acp.OperatorCommandRequest{SessionID: "sess_" + tag, RequestID: newOperatorRequestID()}
	var mux operatorMux
	var err error
	switch name {
	case "tmux":
		mux, err = newTmuxMux(env, req, tag)
		t.Cleanup(func() { _ = exec.Command("tmux", "-L", tag, "kill-server").Run() })
	case "screen":
		mux, err = newScreenMux(env, req, tag)
		t.Cleanup(func() { _ = exec.Command("screen", "-S", screenSessionName(tag, req.SessionID), "-X", "quit").Run() })
	}
	if err != nil {
		t.Fatal(err)
	}
	return mux
}

func waitRun(t *testing.T, run operatorRun) {
	t.Helper()
	select {
	case <-run.Done():
	case <-time.After(20 * time.Second):
		run.Kill(0)
		t.Fatalf("the command did not end; output so far: %q", run.Output())
	}
}

func posixShell(t *testing.T) platform.Shell {
	t.Helper()
	sh, err := platform.ResolveShell("sh", runtime.GOOS, nil)
	if err != nil {
		t.Skip("no sh")
	}
	return sh
}

// adversarialCommand holds every character a second parser in the way would
// change: quotes, $(), backticks, a caret, backslashes, a tab escape and a
// newline. Run directly or in a multiplexer, it must print the same bytes.
const adversarialCommand = "printf '%s|' \"a  b\" 'c$HOME' '`x`' '$(y)' '^C' 'd\\\\e' 'f\\tg'\necho; exit 7"

func normalizeOutput(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(s)
}

func TestOperatorMuxRunsTheCommandAsWritten(t *testing.T) {
	direct, err := StartOperatorToolCommand(adversarialCommand, t.TempDir(), posixShell(t))
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, direct)
	want := normalizeOutput(direct.Output())
	if direct.ExitCode() != 7 || !strings.Contains(want, "c$HOME") {
		t.Fatalf("direct run: %q exit %d", want, direct.ExitCode())
	}
	for _, name := range []string{"tmux", "screen"} {
		t.Run(name, func(t *testing.T) {
			mux := muxUnderTest(t, name)
			run, err := mux.Start(adversarialCommand, t.TempDir(), posixShell(t))
			if err != nil {
				t.Fatal(err)
			}
			waitRun(t, run)
			if got := normalizeOutput(run.Output()); got != want {
				t.Fatalf("output in %s = %q, direct = %q", name, got, want)
			}
			if code := run.ExitCode(); code != 7 {
				t.Fatalf("exit code in %s = %d, want 7 (%s)", name, code, goneReason(run))
			}
		})
	}
}

// TestOperatorMuxGivesTheCommandATerminal is what the multiplexer is for.
func TestOperatorMuxGivesTheCommandATerminal(t *testing.T) {
	for _, name := range []string{"tmux", "screen"} {
		t.Run(name, func(t *testing.T) {
			mux := muxUnderTest(t, name)
			run, err := mux.Start("tty", t.TempDir(), posixShell(t))
			if err != nil {
				t.Fatal(err)
			}
			waitRun(t, run)
			if out := normalizeOutput(run.Output()); !strings.HasPrefix(out, "/dev/") || run.ExitCode() != 0 {
				t.Fatalf("tty in %s: %q exit %d (%s)", name, out, run.ExitCode(), goneReason(run))
			}
		})
	}
}

func TestOperatorMuxStopInterruptsAndCleansUp(t *testing.T) {
	for _, name := range []string{"tmux", "screen"} {
		t.Run(name, func(t *testing.T) {
			mux := muxUnderTest(t, name)
			cwd := t.TempDir()
			run, err := mux.Start("trap 'echo cleaned; exit 3' INT; echo started; while :; do sleep 0.1; done", cwd, posixShell(t))
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for !strings.Contains(run.Output(), "started") && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			run.Interrupt()
			waitRun(t, run)
			if out := run.Output(); !strings.Contains(out, "cleaned") {
				t.Fatalf("the trap did not run in %s: %q", name, out)
			}
			if code := run.ExitCode(); code != 3 {
				t.Fatalf("exit code in %s = %d, want 3 (%s)", name, code, goneReason(run))
			}
		})
	}
}

func TestOperatorMuxKillEndsAnIgnoringCommand(t *testing.T) {
	for _, name := range []string{"tmux", "screen"} {
		t.Run(name, func(t *testing.T) {
			mux := muxUnderTest(t, name)
			run, err := mux.Start("trap '' INT HUP; echo started; sleep 300", t.TempDir(), posixShell(t))
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for !strings.Contains(run.Output(), "started") && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			run.Kill(0)
			waitRun(t, run)
		})
	}
}

func TestOperatorMuxLogIsPrivateAndRemoved(t *testing.T) {
	for _, name := range []string{"tmux", "screen"} {
		t.Run(name, func(t *testing.T) {
			mux := muxUnderTest(t, name)
			dir := mux.(interface{ logDirectory() string }).logDirectory()
			run, err := mux.Start("echo private", t.TempDir(), posixShell(t))
			if err != nil {
				t.Fatal(err)
			}
			fi, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != 0o700 {
				t.Fatalf("log directory mode %v", fi.Mode().Perm())
			}
			waitRun(t, run)
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".log") {
					t.Fatalf("the log %s was left behind", filepath.Join(dir, e.Name()))
				}
			}
		})
	}
}

func goneReason(run operatorRun) string {
	if r, ok := run.(*muxRun); ok {
		return r.goneReason()
	}
	return ""
}
