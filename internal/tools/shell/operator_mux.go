package shell

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// operatorMux runs operator commands inside a terminal multiplexer: the
// command gets a real terminal, and the operator can attach to type into it.
type operatorMux interface {
	// Name is "tmux" or "screen".
	Name() string
	// AttachCommand is how the operator reaches the command's terminal.
	AttachCommand() string
	// Start runs the command in a window of its own.
	Start(command, cwd string, commandShell platform.Shell) (operatorRun, error)
}

const (
	// operatorMuxSocket is the tmux server socket of Coddy's own server
	// (tmux -L): the operator's default server is never touched.
	operatorMuxSocket = "coddy"
	// operatorMuxPoll is how often a multiplexed command is looked at.
	operatorMuxPoll = 150 * time.Millisecond
	// operatorLogLimit is the size past which the capture log is truncated;
	// the tail buffer has already taken what it holds.
	operatorLogLimit = 1 << 20
)

// operatorMultiplexer returns the multiplexer tools.operator_command names
// for this request, nil to run the command directly. Windows has neither, so
// the setting is ignored there.
func operatorMultiplexer(name string, env *tooling.Env, req acp.OperatorCommandRequest) (operatorMux, error) {
	if name == "" || runtime.GOOS == "windows" {
		return nil, nil
	}
	switch name {
	case "tmux":
		return newTmuxMux(env, req, operatorMuxSocket)
	case "screen":
		return newScreenMux(env, req, operatorMuxSocket)
	default:
		return nil, fmt.Errorf("tools.operator_command.multiplexer: %q is not one of tmux, screen", name)
	}
}

// safeMuxPath is a path that may be written inside a quoted shell word the
// multiplexer itself runs (tmux pipe-pane, the screen launcher).
var safeMuxPath = regexp.MustCompile(`^[A-Za-z0-9/._-]+$`)

// operatorLogDir is the private folder the capture logs of a session live
// in: under its bundle when that path is plain, else a temporary folder.
//
// It is made when a command starts, not when the request is shown, and a
// temporary one (temp true) is the run's own, removed when the run ends.
func operatorLogDir(sessionDir string) (dir string, temp bool, err error) {
	if sessionDir != "" {
		dir := filepath.Join(sessionDir, "operator-commands")
		if safeMuxPath.MatchString(dir) {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return "", false, err
			}
			if err := os.Chmod(dir, 0o700); err != nil {
				return "", false, err
			}
			return dir, false, nil
		}
	}
	dir, err = os.MkdirTemp("", "coddy-operator-")
	if err != nil {
		return "", false, err
	}
	if !safeMuxPath.MatchString(dir) {
		_ = os.Remove(dir)
		return "", false, fmt.Errorf("the temporary folder %q has characters a multiplexer command cannot carry", dir)
	}
	return dir, true, nil
}

// muxSessionTag names the multiplexer session of a Coddy session.
func muxSessionTag(sessionID string) string {
	id := strings.TrimPrefix(sessionID, "sess_")
	var b strings.Builder
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > 12 {
		s = s[len(s)-12:]
	}
	if s == "" {
		s = "default"
	}
	return "coddy-" + s
}

func windowName(requestID string) string {
	id := strings.TrimPrefix(requestID, "oc_")
	if len(id) > 12 {
		id = id[:12]
	}
	return "oc-" + id
}

// underService reports whether Coddy runs as a systemd unit, whose control
// group would take a multiplexer server it starts down with it on a restart.
func underService() bool {
	return os.Getenv("INVOCATION_ID") != "" || os.Getenv("CODDY_SERVE_ROLE") == "service"
}

// startServer starts a multiplexer server: in a systemd scope of its own when
// Coddy runs as a unit and systemd-run is there, so a long run survives a
// restart of the service, and plainly when that scope cannot be had (a system
// unit has no user bus to ask) or when Coddy is no unit at all.
func startServer(bin string, args ...string) ([]byte, error) {
	if underService() {
		if run, err := exec.LookPath("systemd-run"); err == nil {
			cmd := exec.Command(run, append([]string{"--user", "--scope", "--collect", "--quiet", "--", bin}, args...)...) // #nosec G204 -- fixed multiplexer argv
			platform.AdaptCommand(cmd)
			if out, err := cmd.CombinedOutput(); err == nil {
				return out, nil
			}
		}
	}
	cmd := exec.Command(bin, args...) // #nosec G204 -- fixed multiplexer argv
	platform.AdaptCommand(cmd)
	return cmd.CombinedOutput()
}

// muxRun is a command running in a multiplexer window, its output read from
// the capture log into the same bounded tail as a direct run.
type muxRun struct {
	logPath string
	tail    *tailBuffer
	done    chan struct{}

	mu       sync.Mutex
	offset   int64
	exitCode int
	pid      int

	interrupt func()
	closeWin  func()

	// gone says why the end was read as "the window disappeared", for the
	// log and the tests: the exit code is unknown then.
	gone string
}

func (r *muxRun) noteGone(why string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gone = why
}

func (r *muxRun) goneReason() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gone
}

func (r *muxRun) Output() string {
	raw, truncated := r.tail.Snapshot()
	return platform.SanitizeOutput(platform.DecodeOutput(trimPartialUTF8(raw, truncated, !r.finished())))
}

func (r *muxRun) Dropped() int64        { return r.tail.Dropped() }
func (r *muxRun) Done() <-chan struct{} { return r.done }
func (r *muxRun) Interrupt()            { r.interrupt() }
func (r *muxRun) finished() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}
func (r *muxRun) ExitCode() int { r.mu.Lock(); defer r.mu.Unlock(); return r.exitCode }

// Kill terminates the window's process group - the pane's process leads a
// session of its own. The window stays: the watcher reads how the command
// ended from it and closes it then.
func (r *muxRun) Kill(grace time.Duration) {
	r.mu.Lock()
	pid := r.pid
	r.mu.Unlock()
	if pid > 0 {
		_ = platform.TerminateProcessGroupByPID(pid, time.Time{}, grace)
	}
}

// drain copies what the log gained since the last read into the tail, and
// truncates a log past operatorLogLimit: both writers append, so the next
// write lands at the start again.
func (r *muxRun) drain() {
	f, err := os.Open(r.logPath)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	r.mu.Lock()
	defer r.mu.Unlock()
	fi, err := f.Stat()
	if err != nil {
		return
	}
	if fi.Size() < r.offset {
		r.offset = 0
	}
	if _, err := f.Seek(r.offset, io.SeekStart); err != nil {
		return
	}
	n, _ := io.Copy(r.tail, f)
	r.offset += n
	if r.offset > operatorLogLimit {
		if err := os.Truncate(r.logPath, 0); err == nil {
			r.offset = 0
		}
	}
}

// watch polls until dead reports the end, then drains the log a last time,
// closes the window and removes the log before Done closes.
func (r *muxRun) watch(dead func() (bool, int)) {
	ticker := time.NewTicker(operatorMuxPoll)
	defer ticker.Stop()
	for range ticker.C {
		r.drain()
		ended, code := dead()
		if !ended {
			continue
		}
		// What the command printed last may still be on its way to the log.
		time.Sleep(operatorMuxPoll)
		r.drain()
		r.mu.Lock()
		r.exitCode = code
		r.mu.Unlock()
		r.closeWin()
		_ = os.Remove(r.logPath)
		close(r.done)
		return
	}
}

func newMuxRun(logDir, requestID string) (*muxRun, error) {
	logPath := filepath.Join(logDir, strings.TrimPrefix(requestID, "oc_")+".log")
	if !safeMuxPath.MatchString(logPath) {
		return nil, fmt.Errorf("the capture log path %q has characters a multiplexer command cannot carry", logPath)
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	return &muxRun{logPath: logPath, tail: &tailBuffer{limit: operatorOutputCap}, done: make(chan struct{}), exitCode: -1}, nil
}

// ---- the launcher both multiplexers run ----

// muxLauncher is the script a multiplexer window runs instead of the command:
// it records its pid, enters the folder, hands the operator shell its argv
// quoted for /bin/sh - so the command text reaches the shell untouched, which
// screen's own parsing would not allow - and writes the command's status to a
// file before it exits with that status. trap : (not trap ”) keeps SIGINT at
// its default in the command while the launcher lives on to record the status;
// the status a multiplexer reports for its pane can come late or not at all
// after a Ctrl+C (tmux on macOS), the file does not. It keeps a trace of its
// own steps on fd 9, closed for the command, so a window that ends without a
// status says how far it got.
type muxLauncher struct {
	path, pidFile, exitFile, traceFile string
}

func writeMuxLauncher(base, cwd string, commandShell platform.Shell, command string) (*muxLauncher, error) {
	l := &muxLauncher{path: base + ".sh", pidFile: base + ".pid", exitFile: base + ".exit", traceFile: base + ".trace"}
	exe, argv := commandShell.Command(command)
	quoted := []string{shellQuote(exe)}
	for _, a := range argv {
		quoted = append(quoted, shellQuote(a))
	}
	script := "#!/bin/sh\n" +
		"exec 9>>" + shellQuote(l.traceFile) + "\n" +
		"echo \"launcher $$ started\" >&9\n" +
		"echo $$ > " + shellQuote(l.pidFile) + " || exit 1\n" +
		"cd " + shellQuote(cwd) + " || exit 1\n" +
		"trap : INT\n" +
		strings.Join(quoted, " ") + " 9>&-\n" +
		"code=$?\n" +
		"echo \"command exited $code\" >&9\n" +
		"printf '%s' \"$code\" > " + shellQuote(l.exitFile+".tmp") + " && mv " + shellQuote(l.exitFile+".tmp") + " " + shellQuote(l.exitFile) + "\n" +
		"exit \"$code\"\n"
	if err := os.WriteFile(l.path, []byte(script), 0o600); err != nil {
		return nil, err
	}
	return l, nil
}

// status reads the command's status the launcher wrote, if it did.
func (l *muxLauncher) status() (int, bool) {
	b, err := os.ReadFile(l.exitFile)
	if err != nil {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return -1, true
	}
	return code, true
}

func (l *muxLauncher) trace() string {
	b, _ := os.ReadFile(l.traceFile)
	return strings.TrimSpace(string(b))
}

func (l *muxLauncher) cleanup() {
	for _, p := range []string{l.pidFile, l.exitFile, l.exitFile + ".tmp", l.path, l.traceFile} {
		_ = os.Remove(p)
	}
}

// ---- tmux ----

type tmuxMux struct {
	bin, socket, session, sessionDir, requestID string
	logDir                                      string
}

func newTmuxMux(env *tooling.Env, req acp.OperatorCommandRequest, socket string) (*tmuxMux, error) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		return nil, fmt.Errorf("tools.operator_command.multiplexer is tmux, which is not installed on this machine")
	}
	return &tmuxMux{bin: bin, socket: socket, session: muxSessionTag(req.SessionID), sessionDir: envSessionDir(env), requestID: req.RequestID}, nil
}

func (m *tmuxMux) Name() string         { return "tmux" }
func (m *tmuxMux) logDirectory() string { return m.logDir }

// envSessionDir is the session bundle the logs go under, if any.
func envSessionDir(env *tooling.Env) string {
	if env == nil {
		return ""
	}
	return env.SessionDir
}
func (m *tmuxMux) AttachCommand() string { return "tmux -L " + m.socket + " attach -t " + m.session }

// args puts Coddy's server and an empty configuration in front of every call:
// the operator's tmux.conf must not change remain-on-exit or the history.
func (m *tmuxMux) args(a ...string) []string {
	return append([]string{"-L", m.socket, "-f", "/dev/null"}, a...)
}

func (m *tmuxMux) run(a ...string) (string, error) {
	cmd := exec.Command(m.bin, m.args(a...)...) // #nosec G204 -- fixed tmux argv
	platform.AdaptCommand(cmd)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (m *tmuxMux) Start(command, cwd string, commandShell platform.Shell) (operatorRun, error) {
	if _, err := m.run("has-session", "-t", "="+m.session); err != nil {
		// The session keeps an idle window, so closing the last command's
		// window never ends it.
		if out, err := startServer(m.bin, m.args("new-session", "-d", "-s", m.session, "-n", "idle", "cat")...); err != nil {
			return nil, fmt.Errorf("tmux new-session: %v: %s", err, bytes.TrimSpace(out))
		}
	}
	dir, temp, err := operatorLogDir(m.sessionDir)
	if err != nil {
		return nil, err
	}
	m.logDir = dir
	dropDir := func() {
		if temp {
			_ = os.RemoveAll(dir)
		}
	}
	r, err := newMuxRun(dir, m.requestID)
	if err != nil {
		dropDir()
		return nil, err
	}
	win := windowName(m.requestID)
	l, err := writeMuxLauncher(strings.TrimSuffix(r.logPath, ".log"), cwd, commandShell, command)
	if err != nil {
		_ = os.Remove(r.logPath)
		dropDir()
		return nil, err
	}
	// The window runs the launcher as argv, never through a shell of its own,
	// and the launcher enters the folder: tmux expands #{...} and #(...) in
	// a -c path, so a folder named #(touch x) would run a command of its own.
	// it outlives the command (remain-on-exit) so the pane can still be read,
	// and the capture starts in the same call that makes the window.
	// remain-on-exit is a window option: set-window-option names it so on
	// every tmux, where an older set-option -g would set nothing.
	args := []string{"set-window-option", "-g", "remain-on-exit", "on", ";",
		"new-window", "-d", "-t", "=" + m.session + ":", "-n", win, "-P", "-F", "#{pane_id} #{pane_pid}", "--", "/bin/sh", l.path,
		";", "pipe-pane", "-o", "-t", "=" + m.session + ":=" + win, "cat >> '" + r.logPath + "'"}
	out, err := m.run(args...)
	if err != nil {
		l.cleanup()
		_ = os.Remove(r.logPath)
		dropDir()
		return nil, fmt.Errorf("tmux new-window: %v: %s", err, out)
	}
	fields := strings.Fields(lastLine(out))
	if len(fields) != 2 {
		// The window may be up: it must not run on, unwatched.
		_, _ = m.run("kill-window", "-t", "="+m.session+":="+win)
		l.cleanup()
		_ = os.Remove(r.logPath)
		dropDir()
		return nil, fmt.Errorf("tmux new-window printed %q", out)
	}
	pane := fields[0]
	r.pid, _ = strconv.Atoi(fields[1])
	r.interrupt = func() { _, _ = m.run("send-keys", "-t", pane, "C-c") }
	r.closeWin = func() { _, _ = m.run("kill-pane", "-t", pane) }
	var paneEnd tmuxPaneEnd
	go func() {
		r.watch(func() (bool, int) {
			// The launcher's status first: it is there whatever tmux saw.
			if code, ok := l.status(); ok {
				return true, code
			}
			status, err := m.run("display-message", "-p", "-t", pane, "#{pane_dead} #{pane_dead_status} #{pane_dead_signal}")
			if err != nil || status == "" {
				// The window is gone: closed by the operator. tmux answers
				// a pane that no longer exists with nothing, not an error.
				if code, ok := l.status(); ok {
					return true, code
				}
				r.noteGone(fmt.Sprintf("tmux display-message %s: %q, %v; launcher trace: %s", pane, status, err, l.trace()))
				return true, -1
			}
			ended, code, why := paneEnd.read(status)
			if !ended {
				return false, 0
			}
			if c, ok := l.status(); ok {
				return true, c
			}
			if why != "" {
				r.noteGone(why + "; launcher trace: " + l.trace())
			}
			return true, code
		})
		l.cleanup()
		dropDir()
	}()
	return r, nil
}

// tmuxStatusPolls is how many polls a dead pane may go without a status
// before its end is called unknown.
const tmuxStatusPolls = 20

// tmuxPaneEnd reads "#{pane_dead} #{pane_dead_status} #{pane_dead_signal}"
// poll after poll. tmux marks a pane dead when its terminal closes, which can
// come before the process's status is collected, so a dead pane without a
// status is read again for a while instead of being called ended at once.
type tmuxPaneEnd struct {
	waits int
}

func (p *tmuxPaneEnd) read(status string) (ended bool, code int, why string) {
	// A signal leaves the status empty, so the fields go by position.
	f := strings.SplitN(status, " ", 3)
	if len(f) == 0 || f[0] != "1" {
		return false, 0, ""
	}
	if len(f) > 1 && f[1] != "" {
		if code, err := strconv.Atoi(f[1]); err == nil {
			return true, code, ""
		}
	}
	if len(f) > 2 && f[2] != "" {
		if sig, err := strconv.Atoi(f[2]); err == nil {
			return true, 128 + sig, ""
		}
	}
	p.waits++
	if p.waits < tmuxStatusPolls {
		return false, 0, ""
	}
	return true, -1, fmt.Sprintf("tmux reported the pane dead without a status for %d polls: %q", p.waits, status)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// ---- screen ----

type screenMux struct {
	bin, session, sessionDir, requestID string
	logDir                              string
}

// screenSessionName is the screen session of a Coddy session; prefix keeps a
// test's sessions apart from the real ones.
func screenSessionName(prefix, sessionID string) string {
	tag := muxSessionTag(sessionID)
	if prefix != operatorMuxSocket {
		tag = prefix + "-" + strings.TrimPrefix(tag, "coddy-")
	}
	return tag
}

func newScreenMux(env *tooling.Env, req acp.OperatorCommandRequest, prefix string) (*screenMux, error) {
	bin, err := exec.LookPath("screen")
	if err != nil {
		return nil, fmt.Errorf("tools.operator_command.multiplexer is screen, which is not installed on this machine")
	}
	return &screenMux{bin: bin, session: screenSessionName(prefix, req.SessionID), sessionDir: envSessionDir(env), requestID: req.RequestID}, nil
}

func (m *screenMux) Name() string          { return "screen" }
func (m *screenMux) logDirectory() string  { return m.logDir }
func (m *screenMux) AttachCommand() string { return "screen -r " + m.session }

func (m *screenMux) run(a ...string) (string, error) {
	cmd := exec.Command(m.bin, append([]string{"-S", m.session}, a...)...) // #nosec G204 -- fixed screen argv
	platform.AdaptCommand(cmd)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// shellQuote quotes one argv element for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (m *screenMux) Start(command, cwd string, commandShell platform.Shell) (operatorRun, error) {
	if _, err := m.run("-X", "select", "0"); err != nil {
		// -c /dev/null: the operator's ~/.screenrc can start windows and run
		// commands of its own, as a tmux.conf can (tmux runs with -f /dev/null).
		if out, err := startServer(m.bin, "-c", "/dev/null", "-dmS", m.session); err != nil {
			return nil, fmt.Errorf("screen -dmS: %v: %s", err, bytes.TrimSpace(out))
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := m.run("-X", "select", "0"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("the screen session %s did not come up", m.session)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	dir, temp, err := operatorLogDir(m.sessionDir)
	if err != nil {
		return nil, err
	}
	m.logDir = dir
	dropDir := func() {
		if temp {
			_ = os.RemoveAll(dir)
		}
	}
	r, err := newMuxRun(dir, m.requestID)
	if err != nil {
		dropDir()
		return nil, err
	}
	l, err := writeMuxLauncher(strings.TrimSuffix(r.logPath, ".log"), cwd, commandShell, command)
	if err != nil {
		_ = os.Remove(r.logPath)
		dropDir()
		return nil, err
	}
	// screen parses the words of a command it is handed (it expands $VAR,
	// ^C and backslashes), so only the launcher's path reaches it.
	pidFile, exitFile, launcher, cleanup := l.pidFile, l.exitFile, l.path, l.cleanup
	win := windowName(m.requestID)
	for _, step := range [][]string{
		{"-X", "logfile", r.logPath},
		{"-X", "logfile", "flush", "0"},
		{"-X", "screen", "-L", "-t", win, "/bin/sh", launcher},
	} {
		if out, err := m.run(step...); err != nil {
			cleanup()
			_ = os.Remove(r.logPath)
			dropDir()
			return nil, fmt.Errorf("screen %s: %v: %s", strings.Join(step, " "), err, out)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for r.pid == 0 {
		if b, err := os.ReadFile(pidFile); err == nil {
			r.pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if r.pid == 0 {
			if time.Now().After(deadline) {
				// The window may be up after all: it must not run on, unwatched.
				_, _ = m.run("-p", win, "-X", "kill")
				cleanup()
				_ = os.Remove(r.logPath)
				dropDir()
				return nil, fmt.Errorf("the screen window of the command did not start")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	// The Ctrl+C byte itself: an older screen (macOS ships 4.00) types the
	// two characters of "^C" rather than reading them as a control code.
	r.interrupt = func() { _, _ = m.run("-p", win, "-X", "stuff", "\x03") }
	r.closeWin = func() { _, _ = m.run("-p", win, "-X", "kill") }
	pid := r.pid
	go func() {
		r.watch(func() (bool, int) {
			ended, code, why := screenEnded(exitFile, func() bool { return platform.ProcessGroupAlive(pid, time.Time{}) })
			if why != "" {
				r.noteGone(why + "; launcher trace: " + l.trace())
			}
			return ended, code
		})
		cleanup()
		dropDir()
	}()
	return r, nil
}

// screenEnded reads how a screen window's command ended: the launcher's status
// file, else a window whose process group is gone (killed, or closed by the
// operator) as an unknown end. The launcher writes the status and then exits,
// so a status that is not there yet is looked for once more after the group
// was found gone: it may have landed between the two looks.
func screenEnded(exitFile string, groupAlive func() bool) (ended bool, code int, why string) {
	read := func() (int, bool) {
		b, err := os.ReadFile(exitFile)
		if err != nil {
			return 0, false
		}
		code, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			return -1, true
		}
		return code, true
	}
	if code, ok := read(); ok {
		return true, code, ""
	}
	if groupAlive() {
		return false, 0, ""
	}
	if code, ok := read(); ok {
		return true, code, ""
	}
	return true, -1, "the screen window ended without writing a status"
}
