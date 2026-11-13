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
	// terminalIdleLimit stops a shell nobody has looked at since. The panel can
	// be closed and reopened without losing the shell, so nothing else would
	// ever end one: without this a long-lived server keeps a process per chat
	// that was open once.
	terminalIdleLimit = 30 * time.Minute
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

func (s *Server) terminalFor(sid, cwd string) (*terminalSession, error) {
	s.terminalMu.Lock()
	idle := s.reapIdleTerminalsLocked(sid)
	if term := s.terminals[sid]; term != nil {
		term.touch()
		s.terminalMu.Unlock()
		stopTerminals(idle)
		return term, nil
	}
	s.terminalMu.Unlock()
	stopTerminals(idle)

	shell := platform.CurrentShell()
	// The host shell is what this endpoint is for: the request is authenticated
	// and the session's workspace is the working directory.
	cmd := exec.Command(shell.Path, shell.InteractiveArgs()...)
	platform.AdaptCommand(cmd)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1")
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
	cmd.Stdout = &terminalWriter{term: term}
	cmd.Stderr = &terminalWriter{term: term}
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
	// stopped rather than left running with nobody holding it.
	if live := s.terminals[sid]; live != nil {
		s.terminalMu.Unlock()
		term.stop()
		live.touch()
		return live, nil
	}
	s.terminals[sid] = term
	s.terminalMu.Unlock()
	return term, nil
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
	carry []byte
}

func (w *terminalWriter) Write(p []byte) (int, error) {
	chunk := p
	if len(w.carry) > 0 {
		chunk = append(w.carry, p...)
		w.carry = nil
	}
	head, carry := platform.SplitPartialRune(chunk)
	if len(carry) > 0 {
		w.carry = append([]byte(nil), carry...)
	}
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
		return errors.New("terminal is not running")
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
		return errors.New("a command is one line")
	}
	t.mu.Unlock()
	t.append("\n❯ " + input + "\n")
	// A shell reading a pipe takes "\n" on every platform, cmd.exe included.
	_, err := io.WriteString(stdin, input+"\n")
	return err
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
	term, err := s.terminalFor(sid, cwd)
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
		http.Error(w, `{"error":{"message":"invalid terminal input"}}`, http.StatusBadRequest)
		return
	}
	term, err := s.terminalFor(sid, cwd)
	if err == nil {
		err = term.write(body.Input)
	}
	if err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusConflict)
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
