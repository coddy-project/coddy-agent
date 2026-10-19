package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

const (
	// acpMaxLine bounds one JSON-RPC line from the agent: a session/new result
	// with a long model catalog or a tool call carrying a file can be large.
	acpMaxLine = 32 << 20
	// acpStartTimeout bounds spawning the agent, initialize and authenticate:
	// an agent that installs a package on its first start (OpenCode fetching a
	// provider module) takes tens of seconds.
	acpStartTimeout = 2 * time.Minute
	// acpExitGrace is how long an agent gets to exit after its stdin closed
	// before its process group is terminated, and acpTermGrace how long the
	// group gets after the signal.
	acpExitGrace = 3 * time.Second
	acpTermGrace = 3 * time.Second
	// acpStderrTail is how much of the agent's stderr an error quotes.
	acpStderrTail = 4 << 10
)

var (
	// acpWriteGrace is how long a line already with the writer gets to go
	// out after its caller gave up: an agent that reads takes it in that
	// time, and one that does not is stopped.
	acpWriteGrace = 2 * time.Second
	// acpWriteTimeout bounds one line's write whoever waits for it, a reply
	// written in the background included: an agent that has not taken a
	// line in that time has stopped reading and is stopped.
	acpWriteTimeout = time.Minute
)

// acpProcess is one running ACP agent: the process, the JSON-RPC connection
// over its stdio and the routing of what the agent sends.
type acpProcess struct {
	spec acpSpec
	log  *slog.Logger

	cmd      *exec.Cmd
	stdin    io.WriteCloser
	closeJob func()
	stderr   *acpTail
	// writes feeds the one goroutine that writes to the agent's stdin, so no
	// caller - and never the reader - blocks on an agent that stopped reading.
	writes chan acpWrite

	nextID atomic.Int64

	mu sync.Mutex
	// pending holds the calls waiting for their response, by request id.
	pending map[int64]chan acpMessage
	// sinks route session/update notifications and permission requests to the
	// prompt running on that ACP session.
	sinks map[string]*acpSink
	// states keeps the modes, models and config options of each ACP session
	// as the agent last reported them; defaults what session/new reported,
	// the values a call that names none goes back to.
	states   map[string]*acpSessionState
	defaults map[string]acpSessionDefaults
	// thoughtSet is the reasoning level a call set on a session, the only
	// one a later call that names none goes back from.
	thoughtSet map[string]string
	// opened counts the sessions this process opened.
	opened atomic.Int64

	init acpInitializeResult

	// done is closed when the reader stopped: the agent exited or its stdout
	// closed. exitErr says why.
	done    chan struct{}
	exitErr error
	closing atomic.Bool
	stopped chan struct{}
	stopOne sync.Once
}

// startACPProcess spawns the agent of spec and runs the handshake under ctx:
// initialize, then authenticate when the spec names a method.
func startACPProcess(ctx context.Context, spec acpSpec, log *slog.Logger) (*acpProcess, error) {
	if log == nil {
		log = slog.Default()
	}
	cmd := exec.Command(spec.command, spec.args...)
	cmd.Env = spec.env
	cmd.Dir = spec.dir
	platform.DetachProcessGroup(cmd)
	platform.AdaptCommand(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, &ACPError{Kind: ACPErrorStart, Agent: spec.label(), Err: err}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, &ACPError{Kind: ACPErrorStart, Agent: spec.label(), Err: err}
	}
	tail := &acpTail{}
	cmd.Stderr = tail
	// A child of the agent that keeps its stderr open must not keep Wait,
	// and with it the agent's end, from returning.
	cmd.WaitDelay = acpTermGrace
	if err := cmd.Start(); err != nil {
		return nil, &ACPError{Kind: ACPErrorStart, Agent: spec.label(), Err: err}
	}
	closeJob, err := platform.KillTreeOnClose(cmd)
	if err != nil {
		log.Debug("acp agent runs without a kill-on-close job", "agent", spec.label(), "error", err)
	}
	p := &acpProcess{
		spec:       spec,
		log:        log,
		cmd:        cmd,
		stdin:      stdin,
		closeJob:   closeJob,
		stderr:     tail,
		pending:    map[int64]chan acpMessage{},
		sinks:      map[string]*acpSink{},
		states:     map[string]*acpSessionState{},
		defaults:   map[string]acpSessionDefaults{},
		thoughtSet: map[string]string{},
		done:       make(chan struct{}),
		stopped:    make(chan struct{}),
		writes:     make(chan acpWrite),
	}
	go p.read(stdout)
	go p.writeLoop()

	hctx, cancel := context.WithTimeout(ctx, acpStartTimeout)
	defer cancel()
	err = p.call(hctx, "initialize", acpInitializeParams{
		ProtocolVersion: acpProtocolVersion,
		ClientInfo:      acpImplementation{Name: "coddy", Title: "Coddy", Version: version.Version},
	}, &p.init)
	if err == nil && p.init.ProtocolVersion != acpProtocolVersion {
		err = &ACPError{Kind: ACPErrorStart, Agent: spec.label(), Message: fmt.Sprintf("the agent speaks ACP protocol version %d; Coddy speaks %d", p.init.ProtocolVersion, acpProtocolVersion)}
	}
	if err == nil && strings.TrimSpace(spec.authMethod) != "" {
		err = p.call(hctx, "authenticate", map[string]string{"methodId": spec.authMethod}, nil)
	}
	if err != nil {
		p.stop()
		var acpErr *ACPError
		if errors.As(err, &acpErr) && acpErr.Kind == ACPErrorExited {
			// Died during the handshake: that is a failed start.
			acpErr.Kind = ACPErrorStart
			return nil, acpErr
		}
		if errors.As(err, &acpErr) {
			return nil, err
		}
		return nil, &ACPError{Kind: ACPErrorStart, Agent: spec.label(), Err: err, Stderr: p.stderr.String()}
	}
	return p, nil
}

// alive reports whether the reader still runs.
func (p *acpProcess) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// read routes every line the agent writes until its stdout closes.
func (p *acpProcess) read(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 64<<10)
	var readErr error
	for {
		line, err := readACPLine(r)
		if len(bytes.TrimSpace(line)) > 0 {
			var m acpMessage
			if jerr := json.Unmarshal(line, &m); jerr != nil {
				p.log.Debug("acp agent wrote a line that is not JSON-RPC", "agent", p.spec.label(), "line", truncateForLog(string(line), 200))
			} else {
				p.dispatch(m)
			}
		}
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
	}
	if readErr != nil {
		// The agent may still be writing into a pipe nobody reads: end it
		// before waiting for it.
		_ = platform.TerminateProcessGroup(p.cmd, acpTermGrace)
	}
	// Its stdout closed: the agent is ending. It gets a grace to exit, and
	// WaitDelay keeps a child holding its stderr from holding Wait.
	waitCh := make(chan error, 1)
	go func() { waitCh <- p.cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-time.After(acpExitGrace):
		_ = platform.TerminateProcessGroup(p.cmd, acpTermGrace)
		waitErr = <-waitCh
	}
	// What the agent started may outlive it in its process group: end that
	// too, now that the agent itself is reaped. Not on Windows: the reaped
	// agent's pid is released there and may name another process, and the
	// kill-on-close job below ends the tree instead.
	if runtime.GOOS != "windows" {
		_ = platform.TerminateProcessGroup(p.cmd, acpTermGrace)
	}
	if p.closeJob != nil {
		p.closeJob()
	}
	p.mu.Lock()
	switch {
	case p.closing.Load():
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: errors.New("the agent was stopped")}
	case readErr != nil:
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: readErr, Stderr: p.stderr.String()}
	case waitErr != nil:
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: waitErr, Stderr: p.stderr.String()}
	default:
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: errors.New("the agent exited"), Stderr: p.stderr.String()}
	}
	pending := p.pending
	p.pending = map[int64]chan acpMessage{}
	sinks := make([]*acpSink, 0, len(p.sinks))
	for _, s := range p.sinks {
		sinks = append(sinks, s)
	}
	// Closed under the lock: a call registers itself only while done is
	// open, so none is left in a map nobody closes.
	close(p.done)
	p.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
	for _, s := range sinks {
		s.wake()
	}
}

// readACPLine reads one newline-terminated line of at most acpMaxLine bytes.
func readACPLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		buf = append(buf, chunk...)
		if len(buf) > acpMaxLine {
			return nil, fmt.Errorf("a JSON-RPC line from the agent exceeds %d bytes", acpMaxLine)
		}
		if err != nil {
			return buf, err
		}
		if !isPrefix {
			return buf, nil
		}
	}
}

func (p *acpProcess) dispatch(m acpMessage) {
	hasID := len(m.ID) > 0 && string(m.ID) != "null"
	switch {
	case m.Method == "" && hasID:
		id, err := strconv.ParseInt(string(m.ID), 10, 64)
		if err != nil {
			return
		}
		p.mu.Lock()
		ch := p.pending[id]
		delete(p.pending, id)
		p.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	case m.Method == "session/update":
		var n acpSessionNotification
		if err := json.Unmarshal(m.Params, &n); err != nil {
			return
		}
		var u acpSessionUpdate
		if err := json.Unmarshal(n.Update, &u); err != nil {
			return
		}
		p.noteState(n.SessionID, u)
		p.mu.Lock()
		if sink := p.sinks[n.SessionID]; sink != nil {
			sink.push(acpEvent{update: &u})
		}
		p.mu.Unlock()
	case m.Method == "session/request_permission" && hasID:
		var req acpPermissionRequest
		if err := json.Unmarshal(m.Params, &req); err != nil {
			p.respondError(m.ID, -32602, "invalid params")
			return
		}
		// Looked up and pushed under the lock: a prompt that detaches its
		// sink either gets the request in its drain or leaves it to the
		// answer below.
		p.mu.Lock()
		sink := p.sinks[req.SessionID]
		if sink != nil {
			sink.push(acpEvent{permission: &req, permissionID: m.ID})
		}
		p.mu.Unlock()
		if sink == nil {
			// No prompt runs on that session: nobody can answer.
			p.respond(m.ID, acpPermissionResult{Outcome: acpPermissionOutcome{Outcome: "cancelled"}})
		}
	case hasID:
		// The client capabilities Coddy advertises are all off: the agent
		// reads, writes and runs with its own tools. Anything it asks of the
		// client is refused rather than left unanswered.
		p.respondError(m.ID, rpcMethodNotFound, "method not supported by this client: "+m.Method)
	}
}

// noteState keeps the session state current with the agent's notifications.
func (p *acpProcess) noteState(sessionID string, u acpSessionUpdate) {
	if u.SessionUpdate != "current_mode_update" && u.SessionUpdate != "config_option_update" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.states[sessionID]
	if st == nil {
		return
	}
	switch u.SessionUpdate {
	case "current_mode_update":
		if st.Modes != nil {
			st.Modes.CurrentModeID = u.CurrentModeID
		}
		// The mode config option says the same: keep it in step, so the
		// next selection reads the mode the agent is really in.
		if i := optionIndex(st.ConfigOptions, "mode"); i >= 0 {
			if v, err := json.Marshal(u.CurrentModeID); err == nil {
				st.ConfigOptions[i].CurrentValue = v
			}
		}
	case "config_option_update":
		if len(u.ConfigOptions) > 0 {
			st.ConfigOptions = u.ConfigOptions
			if opt, ok := optionOf(u.ConfigOptions, "mode"); ok && st.Modes != nil {
				st.Modes.CurrentModeID = opt.current()
			}
		}
	}
}

// call sends a request and decodes its result into out (nil discards it).
func (p *acpProcess) call(ctx context.Context, method string, params, out interface{}) error {
	ch, id, err := p.request(ctx, method, params)
	if err != nil {
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return p.exitError()
		}
		if m.Error != nil {
			return p.rpcError(method, m.Error)
		}
		if out != nil && len(m.Result) > 0 {
			if err := json.Unmarshal(m.Result, out); err != nil {
				return &ACPError{Kind: ACPErrorRPC, Agent: p.spec.label(), Method: method, Message: "undecodable result: " + err.Error()}
			}
		}
		return nil
	case <-ctx.Done():
		p.forget(id)
		return ctx.Err()
	case <-p.done:
		return p.exitError()
	}
}

// start sends a request without waiting for its response: the returned
// channel yields it, or closes when the agent is gone.
func (p *acpProcess) start(ctx context.Context, method string, params interface{}) (<-chan acpMessage, error) {
	ch, _, err := p.request(ctx, method, params)
	return ch, err
}

// request registers a request and writes it.
func (p *acpProcess) request(ctx context.Context, method string, params interface{}) (chan acpMessage, int64, error) {
	id := p.nextID.Add(1)
	ch := make(chan acpMessage, 1)
	p.mu.Lock()
	if !p.alive() {
		p.mu.Unlock()
		return nil, 0, p.exitError()
	}
	p.pending[id] = ch
	p.mu.Unlock()
	if err := p.send(ctx, map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		p.forget(id)
		return nil, 0, err
	}
	return ch, id, nil
}

func (p *acpProcess) forget(id int64) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

// notify sends a notification without waiting for the agent to read it.
func (p *acpProcess) notify(method string, params interface{}) {
	p.sendAsync(map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params})
}

// respond answers a request of the agent. It never blocks: the reader calls
// it, and a reader waiting on the agent's stdin while the agent waits on its
// stdout would deadlock.
func (p *acpProcess) respond(id json.RawMessage, result interface{}) {
	p.sendAsync(map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result})
}

func (p *acpProcess) respondError(id json.RawMessage, code int, msg string) {
	p.sendAsync(map[string]interface{}{"jsonrpc": "2.0", "id": id, "error": map[string]interface{}{"code": code, "message": msg}})
}

// acpWrite is one line for the writer goroutine.
type acpWrite struct {
	line []byte
	done chan error
}

// writeLoop writes the lines callers hand it, one at a time, until the agent
// is gone.
func (p *acpProcess) writeLoop() {
	for {
		select {
		case w := <-p.writes:
			_, err := p.stdin.Write(w.line)
			w.done <- err
		case <-p.done:
			return
		}
	}
}

// send writes one message under ctx. A write that has started cannot be
// taken back - half a line would corrupt the stream - so a ctx that ends
// while the agent does not read its stdin stops the agent.
func (p *acpProcess) send(ctx context.Context, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// A caller that already gave up hands nothing over: a line once with
	// the writer cannot be taken back.
	if err := ctx.Err(); err != nil {
		return err
	}
	w := acpWrite{line: append(b, '\n'), done: make(chan error, 1)}
	select {
	case p.writes <- w:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return p.exitError()
	}
	written := func(err error) error {
		if err != nil {
			// The connection is unusable: retire the agent so the pool
			// replaces it.
			go p.stop()
			return &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: err, Stderr: p.stderr.String()}
		}
		return nil
	}
	bound := time.NewTimer(acpWriteTimeout)
	defer bound.Stop()
	var grace <-chan time.Time
	cancelled := ctx.Done()
	for {
		select {
		case err := <-w.done:
			// Out, whether or not the caller still waits: what follows
			// (a response, a cancellation) is the caller's business.
			return written(err)
		case <-p.done:
			select {
			case err := <-w.done:
				if err == nil {
					return nil
				}
			default:
			}
			return p.exitError()
		case <-cancelled:
			cancelled = nil
			t := time.NewTimer(acpWriteGrace)
			defer t.Stop()
			grace = t.C
		case <-grace:
			p.log.Warn("acp agent does not read its input; stopping it", "agent", p.spec.label())
			go p.stop()
			return ctx.Err()
		case <-bound.C:
			p.log.Warn("acp agent took no input for the write bound; stopping it", "agent", p.spec.label(), "bound", acpWriteTimeout)
			go p.stop()
			return &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: errors.New("the agent stopped reading its input"), Stderr: p.stderr.String()}
		}
	}
}

// sendAsync writes one message in the background.
func (p *acpProcess) sendAsync(v interface{}) {
	go func() { _ = p.send(context.Background(), v) }()
}

// rpcError turns an error answer into an ACPError: -32000 is the agent
// asking for authentication, on whatever method it answers.
func (p *acpProcess) rpcError(method string, e *acpRPCError) *ACPError {
	out := &ACPError{Kind: ACPErrorRPC, Agent: p.spec.label(), Method: method, Code: e.Code, Message: e.Message, Data: string(e.Data)}
	if e.Code == acpAuthRequired {
		out.Kind = ACPErrorAuth
		for _, m := range p.init.AuthMethods {
			out.AuthMethods = append(out.AuthMethods, m.ID)
		}
	}
	return out
}

func (p *acpProcess) exitError() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}

// attach routes the events of an ACP session to sink until the returned
// function detaches it.
func (p *acpProcess) attach(sessionID string, sink *acpSink) func() {
	p.mu.Lock()
	p.sinks[sessionID] = sink
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		if p.sinks[sessionID] == sink {
			delete(p.sinks, sessionID)
		}
		p.mu.Unlock()
	}
}

// stop ends the agent: stdin closed, a grace to exit on its own, then its
// process group terminated. It returns once the agent is gone.
func (p *acpProcess) stop() {
	p.stopOne.Do(func() {
		p.closing.Store(true)
		_ = p.stdin.Close()
		go func() {
			defer close(p.stopped)
			select {
			case <-p.done:
				// Reaped: the reader ended its group and job; its pid is
				// no longer ours to signal.
				return
			case <-time.After(acpExitGrace):
			}
			if err := platform.TerminateProcessGroup(p.cmd, acpTermGrace); err != nil {
				p.log.Warn("acp agent process group did not stop", "agent", p.spec.label(), "error", err)
			}
			if p.closeJob != nil {
				p.closeJob()
			}
			select {
			case <-p.done:
			case <-time.After(acpTermGrace):
			}
		}()
	})
	<-p.stopped
}

// acpEvent is one thing the agent sent about a session while a prompt runs.
type acpEvent struct {
	update       *acpSessionUpdate
	permission   *acpPermissionRequest
	permissionID json.RawMessage
}

// actedOn reports whether the event says the agent worked on the prompt:
// text, thought, a tool call, a plan, a permission request. A status the
// agent sends on its own (its commands, its usage, its title, its mode) does
// not, so a prompt that failed after one alone is still safe to send again.
func (e acpEvent) actedOn() bool {
	if e.permission != nil {
		return true
	}
	if e.update == nil {
		return false
	}
	switch e.update.SessionUpdate {
	case "agent_message_chunk", "agent_thought_chunk", "tool_call", "tool_call_update", "plan":
		return true
	}
	return false
}

// acpSink queues the events of one ACP session for the prompt reading them,
// so the reader never waits on a slow consumer or on a person.
type acpSink struct {
	mu     sync.Mutex
	events []acpEvent
	signal chan struct{}
}

func newACPSink() *acpSink { return &acpSink{signal: make(chan struct{}, 1)} }

func (s *acpSink) push(e acpEvent) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
	s.wake()
}

func (s *acpSink) wake() {
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

// drain returns the queued events.
func (s *acpSink) drain() []acpEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.events
	s.events = nil
	return out
}

// acpTail keeps the last acpStderrTail bytes the agent wrote to stderr.
type acpTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *acpTail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if over := len(t.buf) - acpStderrTail; over > 0 {
		t.buf = append([]byte(nil), t.buf[over:]...)
	}
	return len(b), nil
}

func (t *acpTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// acpSelfDepth is CODDY_ACP_DEPTH of this process: how many acp providers
// stand between it and the Coddy a person started.
func acpSelfDepth() int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(ACPDepthEnv)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// acpSessionDefaults are the model, mode and reasoning level a session
// started with.
type acpSessionDefaults struct {
	model, mode, thought string
}
