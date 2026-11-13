//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

const (
	terminalOutputLimit = 512 << 10
	// terminalIdleLimit stops a shell nobody has looked at since. The window can
	// be closed and reopened without losing the shell, so nothing else would
	// ever end one: without this a long-lived server keeps a process per chat
	// that was open once.
	terminalIdleLimit = 30 * time.Minute
	// terminalSweepEvery is how often the sweeper looks, which decides how long
	// past the limit a shell can live, not whether it is stopped at all.
	terminalSweepEvery = 5 * time.Minute
)

// terminalSession is a long-lived command interpreter for one chat. Pipes are
// deliberate here: the browser supplies the prompt and renders plain output,
// while the shell itself retains cd, exports, functions and other state across
// commands without requiring a platform-specific PTY dependency. What it cannot
// do is run a program that draws a screen (an editor, top): such a program sees
// no terminal and the panel says so rather than pretending otherwise.
type terminalSession struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	shell   platform.Shell
	output  strings.Builder
	running bool
	exitErr string
	seen    time.Time
}

// The two ways a write is refused: one is the caller's command, the other the
// state of the shell, and the handler answers them with different statuses.
var (
	errTerminalNotOneLine = errors.New("a command is one line")
	errTerminalNotRunning = errors.New("terminal is not running")
)

type terminalSnapshot struct {
	Object  string `json:"object"`
	Shell   string `json:"shell"`
	Output  string `json:"output"`
	Running bool   `json:"running"`
	ExitErr string `json:"exit_error,omitempty"`
}

func (s *Server) terminalCWD(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	sid := strings.TrimSpace(r.PathValue("id"))
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	clone.Header.Set("X-Coddy-Session-ID", sid)
	cwd, ok := s.resolveSessionCWD(w, clone)
	return sid, cwd, ok
}

// terminalFor answers with the chat's shell, starting one when there is none.
// replace asks for a fresh shell in place of one that has ended: a read leaves
// a finished shell alone, so its last words and the reason it stopped stay on
// screen, and the next command the person runs starts over instead of being
// refused by a shell nobody can revive.
func (s *Server) terminalFor(sid, cwd string, replace bool) (*terminalSession, error) {
	s.terminalMu.Lock()
	if s.terminalsClosed {
		s.terminalMu.Unlock()
		return nil, errors.New("the server is shutting down")
	}
	idle := s.reapIdleTerminalsLocked(sid)
	var ended *terminalSession
	if term := s.terminals[sid]; term != nil {
		if !replace || term.alive() {
			term.touch()
			s.terminalMu.Unlock()
			stopTerminals(idle)
			return term, nil
		}
		ended = term
		delete(s.terminals, sid)
	}
	s.terminalMu.Unlock()
	stopTerminals(idle)
	if ended != nil {
		ended.stop()
	}

	shell := platform.CurrentShell()
	// The host shell is what this endpoint is for: the request is authenticated
	// like every other route of this server, and the session's workspace is the
	// working directory.
	cmd := exec.Command(shell.Path, shell.InteractiveArgs()...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1")
	// After the environment: on Android AdaptCommand puts a variable of its own
	// in it, and an assignment afterwards would throw that away.
	platform.AdaptCommand(cmd)
	platform.DetachProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	term := &terminalSession{cmd: cmd, stdin: stdin, shell: shell, running: true, seen: time.Now()}
	// The streams are writers rather than StdoutPipe readers on purpose: Wait
	// closes a pipe the moment the process exits, so the last lines of a shell
	// that ends - the error that ended it, most of all - raced the reader and
	// were lost. A writer makes Wait wait for the copy instead. Each stream
	// decodes on its own, so a sequence split between two reads of one stream
	// is never joined to bytes of the other.
	out, errs := &terminalWriter{term: term}, &terminalWriter{term: term}
	cmd.Stdout, cmd.Stderr = out, errs
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if prologue := shell.SessionPrologue(); prologue != "" {
		if _, err := io.WriteString(stdin, prologue); err != nil {
			_ = platform.TerminateProcessGroup(cmd, 500*time.Millisecond)
			return nil, err
		}
	}
	go func() {
		err := cmd.Wait()
		// Wait returns once both copies have ended, so whatever each stream was
		// holding back for the read that never came belongs on screen now.
		out.flush()
		errs.flush()
		term.mu.Lock()
		term.running = false
		if err != nil {
			term.exitErr = err.Error()
		}
		term.mu.Unlock()
	}()

	s.terminalMu.Lock()
	// Another request may have started a shell for this chat while this one was
	// starting its own; the first one in the map is the chat's, and this one is
	// stopped rather than left running with nobody holding it. A drain that got
	// in between takes this one too, rather than leaving a shell behind that
	// nothing will ever stop.
	if live := s.terminals[sid]; live != nil || s.terminalsClosed {
		closed := s.terminalsClosed
		s.terminalMu.Unlock()
		term.stop()
		if closed {
			return nil, errors.New("the server is shutting down")
		}
		live.touch()
		return live, nil
	}
	s.terminals[sid] = term
	s.startTerminalSweeperLocked()
	s.terminalMu.Unlock()
	return term, nil
}

// alive reports whether the shell can still take a command.
func (t *terminalSession) alive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// startTerminalSweeperLocked starts the one goroutine that stops idle shells,
// if it is not running already. Reaping from the request path alone never
// reaches the last chat: a person who opened one terminal and closed it sends
// no further terminal request, and that shell would outlive the day. Called
// under terminalMu.
func (s *Server) startTerminalSweeperLocked() {
	if s.terminalSweep != nil || s.terminalsClosed {
		return
	}
	stop := make(chan struct{})
	s.terminalSweep = stop
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		tick := time.NewTicker(terminalSweepEvery)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				s.terminalMu.Lock()
				idle := s.reapIdleTerminalsLocked("")
				s.terminalMu.Unlock()
				stopTerminals(idle)
			}
		}
	}()
}

// reapIdleTerminalsLocked takes the shells nobody has read or written for
// terminalIdleLimit out of the map and returns them to be stopped outside the
// lock. The chat being asked for is never reaped: its request is the use.
func (s *Server) reapIdleTerminalsLocked(keep string) []*terminalSession {
	var idle []*terminalSession
	cutoff := time.Now().Add(-terminalIdleLimit)
	for id, term := range s.terminals {
		if id == keep || term == nil || !term.idleSince(cutoff) {
			continue
		}
		delete(s.terminals, id)
		idle = append(idle, term)
	}
	return idle
}

func stopTerminals(terms []*terminalSession) {
	for _, term := range terms {
		term.stop()
	}
}

// terminalWriter decodes one stream of the shell into the session's buffer.
// It never reports an error: os/exec would stop copying, and a chat losing its
// output is worse than a write that could not be stored.
type terminalWriter struct {
	term  *terminalSession
	mu    sync.Mutex
	carry []byte
}

// flush writes out a sequence that was held back for bytes that never came.
// Without it the last character of a shell that ended would be dropped.
func (w *terminalWriter) flush() {
	w.mu.Lock()
	carry := w.carry
	w.carry = nil
	w.mu.Unlock()
	if len(carry) > 0 {
		w.term.append(platform.DecodeOutput(carry))
	}
}

func (w *terminalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	chunk := p
	if len(w.carry) > 0 {
		chunk = append(w.carry, p...)
		w.carry = nil
	}
	head, carry := platform.SplitPartialRune(chunk)
	if len(carry) > 0 {
		w.carry = append([]byte(nil), carry...)
	}
	w.mu.Unlock()
	if len(head) > 0 {
		w.term.append(platform.DecodeOutput(head))
	}
	return len(p), nil
}

// append adds decoded text to the retained tail of the output, dropping as much
// from its front as the new text needs.
func (t *terminalSession) append(s string) {
	if s == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(s) >= terminalOutputLimit {
		t.output.Reset()
		t.output.WriteString(trimToRuneStart(s[len(s)-terminalOutputLimit:]))
		return
	}
	if over := t.output.Len() + len(s) - terminalOutputLimit; over > 0 {
		kept := trimToRuneStart(t.output.String()[over:])
		t.output.Reset()
		t.output.WriteString(kept)
	}
	t.output.WriteString(s)
}

// trimToRuneStart drops the continuation bytes of a character the cut beheaded,
// so the retained tail is still valid UTF-8 and the browser is not handed a
// replacement character at the top of every screen.
func trimToRuneStart(s string) string {
	for i := 0; i < len(s) && i < 3; i++ {
		if utf8.RuneStart(s[i]) {
			return s[i:]
		}
	}
	return s
}

func (t *terminalSession) touch() {
	t.mu.Lock()
	t.seen = time.Now()
	t.mu.Unlock()
}

func (t *terminalSession) idleSince(cutoff time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.seen.Before(cutoff)
}

func (t *terminalSession) write(input string) error {
	t.mu.Lock()
	if !t.running || t.stdin == nil {
		t.mu.Unlock()
		return errTerminalNotRunning
	}
	stdin := t.stdin
	t.seen = time.Now()
	// A command is one line: a newline inside it would run its tail as a second
	// command the person never saw echoed.
	input = strings.TrimRight(input, "\r\n")
	if input == "" || strings.ContainsAny(input, "\r\n") {
		t.mu.Unlock()
		if input == "" {
			return nil
		}
		return errTerminalNotOneLine
	}
	t.mu.Unlock()
	// A shell reading a pipe takes "\n" on every platform, cmd.exe included.
	// The echo comes after the write, so a command the shell never received
	// does not stay in the output as one it ran.
	if _, err := io.WriteString(stdin, input+"\n"); err != nil {
		return err
	}
	// A shell that prints its own prompt and the line it read needs no echo
	// from here: two of them would show every command twice.
	if !t.shell.EchoesInput() {
		t.append("\n❯ " + input + "\n")
	}
	return nil
}

func (t *terminalSession) snapshot() terminalSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return terminalSnapshot{
		Object: "coddy.terminal", Shell: t.shell.Path,
		// Escape sequences carry no meaning in a <pre>, and a progress bar
		// redrawn with carriage returns would arrive as dozens of near-identical
		// lines. The stored bytes keep them; only what is read is cleaned.
		Output:  platform.SanitizeOutput(t.output.String()),
		Running: t.running, ExitErr: t.exitErr,
	}
}

func (t *terminalSession) stop() {
	t.mu.Lock()
	stdin, cmd, running := t.stdin, t.cmd, t.running
	t.stdin = nil
	// A snapshot taken between here and the Wait goroutine would otherwise
	// report a process that is already going down as running.
	t.running = false
	t.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if running && cmd != nil {
		_ = platform.TerminateProcessGroup(cmd, 500*time.Millisecond)
	}
}

func (s *Server) closeTerminals() {
	s.terminalMu.Lock()
	terms := s.terminals
	s.terminals = make(map[string]*terminalSession)
	// From here a shell another request is starting is stopped by that request
	// rather than added to a map nothing reads again.
	s.terminalsClosed = true
	if s.terminalSweep != nil {
		close(s.terminalSweep)
		s.terminalSweep = nil
	}
	s.terminalMu.Unlock()
	for _, term := range terms {
		term.stop()
	}
}

func (s *Server) writeTerminalSnapshot(w http.ResponseWriter, term *terminalSession) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(term.snapshot())
}

func (s *Server) coddyTerminalGet(w http.ResponseWriter, r *http.Request) {
	sid, cwd, ok := s.terminalCWD(w, r)
	if !ok {
		return
	}
	// A read leaves a shell that has ended alone, so its last words and the
	// reason it stopped stay on screen until the person acts on them.
	term, err := s.terminalFor(sid, cwd, false)
	if err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusInternalServerError)
		return
	}
	s.writeTerminalSnapshot(w, term)
}

func (s *Server) coddyTerminalPost(w http.ResponseWriter, r *http.Request) {
	sid, cwd, ok := s.terminalCWD(w, r)
	if !ok {
		return
	}
	var body struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, errorJSON("invalid terminal input"), http.StatusBadRequest)
		return
	}
	// A command typed into a shell that has ended starts a fresh one, rather
	// than being refused by a shell nobody can bring back.
	term, err := s.terminalFor(sid, cwd, true)
	if err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusInternalServerError)
		return
	}
	if err := term.write(body.Input); err != nil {
		status := http.StatusConflict
		if !errors.Is(err, errTerminalNotOneLine) {
			status = http.StatusInternalServerError
		}
		http.Error(w, errorJSON(err.Error()), status)
		return
	}
	s.writeTerminalSnapshot(w, term)
}

func (s *Server) coddyTerminalDelete(w http.ResponseWriter, r *http.Request) {
	sid, _, ok := s.terminalCWD(w, r)
	if !ok {
		return
	}
	s.terminalMu.Lock()
	term := s.terminals[sid]
	delete(s.terminals, sid)
	s.terminalMu.Unlock()
	if term != nil {
		term.stop()
	}
	w.WriteHeader(http.StatusNoContent)
}
