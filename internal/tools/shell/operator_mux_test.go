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
	// The test's tmux socket lives and goes with the test.
	// A short folder: a Unix socket path has a limit t.TempDir's names reach.
	sock, err := os.MkdirTemp("", "cx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sock) })
	t.Setenv("TMUX_TMPDIR", sock)
	env := &tooling.Env{SessionDir: t.TempDir()}
	req := acp.OperatorCommandRequest{SessionID: "sess_" + tag, RequestID: newOperatorRequestID()}
	var mux operatorMux
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
			run, err := mux.Start("echo private", t.TempDir(), posixShell(t))
			if err != nil {
				t.Fatal(err)
			}
			dir := mux.(interface{ logDirectory() string }).logDirectory()
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

// TestScreenEndReadsAStatusThatLandsBetweenTheLooks: the launcher writes the
// status and then exits, so a watcher that finds no status and then a dead
// group must look for the status once more before it calls the end unknown.
func TestScreenEndReadsAStatusThatLandsBetweenTheLooks(t *testing.T) {
	exitFile := filepath.Join(t.TempDir(), "x.exit")
	ended, code, why := screenEnded(exitFile, func() bool {
		// The launcher finishes right between the two looks.
		if err := os.WriteFile(exitFile, []byte("3"), 0o600); err != nil {
			t.Fatal(err)
		}
		return false
	})
	if !ended || code != 3 || why != "" {
		t.Fatalf("ended=%v code=%d why=%q, want the status 3", ended, code, why)
	}
	ended, code, why = screenEnded(filepath.Join(t.TempDir(), "none.exit"), func() bool { return false })
	if !ended || code != -1 || why == "" {
		t.Fatalf("a window gone without a status: ended=%v code=%d why=%q", ended, code, why)
	}
	if ended, _, _ := screenEnded(filepath.Join(t.TempDir(), "none.exit"), func() bool { return true }); ended {
		t.Fatal("a live window was taken for ended")
	}
}

// TestTmuxPaneEndWaitsForTheStatus: tmux marks a pane dead when its terminal
// closes, which can come before the process's status; a dead pane without a
// status is read again, and only a pane that stays so is an unknown end.
func TestTmuxPaneEndWaitsForTheStatus(t *testing.T) {
	var p tmuxPaneEnd
	if ended, _, _ := p.read("0  "); ended {
		t.Fatal("a live pane was taken for ended")
	}
	for i := 0; i < tmuxStatusPolls-1; i++ {
		if ended, _, _ := p.read("1  "); ended {
			t.Fatalf("poll %d: a dead pane without a status ended at once", i)
		}
	}
	if ended, code, _ := p.read("1 3 "); !ended || code != 3 {
		t.Fatalf("the status that arrived late: ended=%v code=%d", ended, code)
	}
	var q tmuxPaneEnd
	if ended, code, _ := q.read("1  2"); !ended || code != 130 {
		t.Fatalf("a signal: ended=%v code=%d", ended, code)
	}
	var r tmuxPaneEnd
	var ended bool
	var code int
	var why string
	for i := 0; i < tmuxStatusPolls; i++ {
		ended, code, why = r.read("1  ")
	}
	if !ended || code != -1 || why == "" {
		t.Fatalf("a pane that never got a status: ended=%v code=%d why=%q", ended, code, why)
	}
}

// TestOperatorMuxTakesTheFolderAndTheCommandLiterally: a folder whose name
// tmux would read as a format, and a command whose last word ends in \; (which
// tmux takes for its own separator), run as written.
func TestOperatorMuxTakesTheFolderAndTheCommandLiterally(t *testing.T) {
	for _, name := range []string{"tmux", "screen"} {
		t.Run(name, func(t *testing.T) {
			mux := muxUnderTest(t, name)
			root := t.TempDir()
			// tmux runs #(...) in the process's own folder: look there too.
			t.Chdir(t.TempDir())
			here, _ := os.Getwd()
			cwd := filepath.Join(root, "#(touch PWN) #{session_name} #H")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			run, err := mux.Start(`pwd; echo done \;`, cwd, posixShell(t))
			if err != nil {
				t.Fatal(err)
			}
			waitRun(t, run)
			out := normalizeOutput(run.Output())
			if !strings.Contains(out, cwd) || !strings.Contains(out, "done ;") || run.ExitCode() != 0 {
				t.Fatalf("output %q exit %d (%s), want the folder %q and done ;", out, run.ExitCode(), goneReason(run), cwd)
			}
			for _, dir := range []string{root, cwd, here, os.Getenv("HOME")} {
				if _, err := os.Stat(filepath.Join(dir, "PWN")); err == nil {
					t.Fatalf("a folder name ran a command: %s/PWN exists", dir)
				}
			}
		})
	}
}

// TestOperatorMuxIgnoresTheOperatorsRcFiles: a ~/.screenrc (or tmux.conf) can
// start windows and run commands; the multiplexer of operator commands reads
// none of them.
func TestOperatorMuxIgnoresTheOperatorsRcFiles(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(home, "RCRAN")
	if err := os.WriteFile(filepath.Join(home, ".screenrc"), []byte("screen -t rc /bin/sh -c 'touch "+marker+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte("run-shell 'touch "+marker+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	for _, name := range []string{"tmux", "screen"} {
		t.Run(name, func(t *testing.T) {
			mux := muxUnderTest(t, name)
			run, err := mux.Start("echo ok", t.TempDir(), posixShell(t))
			if err != nil {
				t.Fatal(err)
			}
			waitRun(t, run)
			if _, err := os.Stat(marker); err == nil {
				t.Fatalf("%s read the operator's rc file", name)
			}
		})
	}
}

// TestOperatorMuxRemovesItsTemporaryLogFolder: without a session bundle the
// log goes to a temporary folder, which goes with the run.
func TestOperatorMuxRemovesItsTemporaryLogFolder(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, name := range []string{"tmux", "screen"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("no multiplexer")
			}
			if _, err := exec.LookPath(name); err != nil {
				t.Skipf("%s is not installed", name)
			}
			tag := fmt.Sprintf("coddy-test-%d-%d", os.Getpid(), time.Now().UnixNano()%1e9)
			req := acp.OperatorCommandRequest{SessionID: "sess_" + tag, RequestID: newOperatorRequestID()}
			var mux operatorMux
			var err error
			if name == "tmux" {
				mux, err = newTmuxMux(&tooling.Env{}, req, tag)
				t.Cleanup(func() { _ = exec.Command("tmux", "-L", tag, "kill-server").Run() })
			} else {
				mux, err = newScreenMux(&tooling.Env{}, req, tag)
				t.Cleanup(func() { _ = exec.Command("screen", "-S", screenSessionName(tag, req.SessionID), "-X", "quit").Run() })
			}
			if err != nil {
				t.Fatal(err)
			}
			if left := operatorTempDirs(tmp); len(left) != 0 {
				t.Fatalf("a folder was made before the command started: %v", left)
			}
			run, err := mux.Start("echo ok", t.TempDir(), posixShell(t))
			if err != nil {
				t.Fatal(err)
			}
			waitRun(t, run)
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if len(operatorTempDirs(tmp)) == 0 {
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Fatalf("the temporary log folder stayed: %v", operatorTempDirs(tmp))
		})
	}
}

// operatorTempDirs lists the temporary log folders of operator commands in dir.
func operatorTempDirs(dir string) []string {
	var out []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "coddy-operator-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestOperatorMuxAttachCommandNamesTheCommandsWindow: the operator attaches to
// type into the command, not into the session's idle window.
func TestOperatorMuxAttachCommandNamesTheCommandsWindow(t *testing.T) {
	for _, name := range []string{"tmux", "screen"} {
		t.Run(name, func(t *testing.T) {
			mux := muxUnderTest(t, name)
			win := windowName(mux.(interface{ requestIDForTest() string }).requestIDForTest())
			if got := mux.AttachCommand(); !strings.Contains(got, win) {
				t.Fatalf("attach command %q does not name the window %s", got, win)
			}
		})
	}
}

// TestOperatorMuxLauncherReportsAFolderItCannotEnter: the status is written
// on every way out of the launcher, so a folder that went away is an exit
// code, not an unknown end.
func TestOperatorMuxLauncherReportsAFolderItCannotEnter(t *testing.T) {
	for _, name := range []string{"tmux", "screen"} {
		t.Run(name, func(t *testing.T) {
			mux := muxUnderTest(t, name)
			gone := filepath.Join(t.TempDir(), "gone")
			run, err := mux.Start("echo never", gone, posixShell(t))
			if err != nil {
				t.Fatal(err)
			}
			waitRun(t, run)
			if code := run.ExitCode(); code != 1 {
				t.Fatalf("exit code = %d (%s), want 1 for a folder it cannot enter", code, goneReason(run))
			}
		})
	}
}
