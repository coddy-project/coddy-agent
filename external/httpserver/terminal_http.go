//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

const terminalOutputLimit = 512 << 10

// terminalSession is a long-lived command interpreter for one chat. Pipes are
// deliberate here: the browser supplies the prompt and renders plain output,
// while the shell itself retains cd, exports, functions and other state across
// commands without requiring a platform-specific PTY dependency.
type terminalSession struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	output  bytes.Buffer
	running bool
	exitErr string
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
	defer s.terminalMu.Unlock()
	if term := s.terminals[sid]; term != nil {
		return term, nil
	}
	shell := platform.CurrentShell()
	// The host shell is what this endpoint is for: the request is authenticated
	// and the session's workspace is the working directory.
	cmd := exec.Command(shell.Path)
	platform.AdaptCommand(cmd)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1")
	platform.DetachProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	term := &terminalSession{cmd: cmd, stdin: stdin, running: true}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	// Bash does not record commands received from a pipe unless history is
	// enabled explicitly. Turn it on before the first browser command so the
	// ordinary `history` builtin behaves as it does in a terminal. The browser
	// keeps its own chat-scoped Up/Down history for every shell kind as well.
	if shell.Kind == platform.ShellBash {
		if _, err := io.WriteString(stdin, "set -o history\n"); err != nil {
			_ = platform.TerminateProcessGroup(cmd, 500*time.Millisecond)
			return nil, err
		}
	}
	s.terminals[sid] = term
	go term.copy(stdout)
	go term.copy(stderr)
	go func() {
		err := cmd.Wait()
		term.mu.Lock()
		term.running = false
		if err != nil {
			term.exitErr = err.Error()
		}
		term.mu.Unlock()
	}()
	return term, nil
}

func (t *terminalSession) copy(r io.Reader) {
	buf := make([]byte, 16<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			t.append(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (t *terminalSession) append(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(p) >= terminalOutputLimit {
		t.output.Reset()
		_, _ = t.output.Write(p[len(p)-terminalOutputLimit:])
		return
	}
	if over := t.output.Len() + len(p) - terminalOutputLimit; over > 0 {
		kept := append([]byte(nil), t.output.Bytes()[over:]...)
		t.output.Reset()
		_, _ = t.output.Write(kept)
	}
	_, _ = t.output.Write(p)
}

func (t *terminalSession) write(input string) error {
	t.mu.Lock()
	if !t.running || t.stdin == nil {
		t.mu.Unlock()
		return errors.New("terminal is not running")
	}
	stdin := t.stdin
	input = strings.TrimRight(input, "\r\n")
	if input == "" {
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()
	t.append([]byte("\n❯ " + input + "\n"))
	_, err := io.WriteString(stdin, input+"\n")
	return err
}

func (t *terminalSession) snapshot(shell string) terminalSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return terminalSnapshot{
		Object: "coddy.terminal", Shell: shell, Output: t.output.String(),
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
	_ = json.NewEncoder(w).Encode(term.snapshot(platform.CurrentShell().Path))
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
