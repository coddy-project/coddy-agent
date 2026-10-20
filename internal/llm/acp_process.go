package llm

import (
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

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

const (
	// acpMaxLine bounds one line from the agent, as the SDK's reader does: a
	// longer line ends the connection there, and the router below stops
	// keeping it whole too.
	acpMaxLine = 10 << 20
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
	// acpWriteGrace is how long a write may still hang once the prompt it
	// serves was cancelled: an agent that has not taken a line in that time
	// does not read its input, and it is stopped rather than waited for.
	acpWriteGrace = 2 * time.Second
	// acpWriteTimeout bounds one line's write whoever waits for it, a reply
	// written in the background included: an agent that has not taken a
	// line in that time has stopped reading and is stopped.
	acpWriteTimeout = time.Minute
)

// acpProcess is one running ACP agent: the process, the JSON-RPC connection
// over its stdio (the SDK's) and the routing of what the agent sends about
// its sessions.
type acpProcess struct {
	spec acpSpec
	log  *slog.Logger

	cmd      *exec.Cmd
	stdin    io.WriteCloser
	closeJob func()
	stderr   *acpTail
	conn     *acpsdk.Connection
	// writes feeds the one goroutine that writes to the agent's stdin, so a
	// write the agent does not take is bounded rather than held forever.
	writes chan acpWrite
	// writing is when the write under way was handed over (Unix nanoseconds),
	// 0 while none is.
	writing atomic.Int64
	// writeErr is why a write failed: the connection is unusable then.
	writeErr atomic.Pointer[error]

	mu sync.Mutex
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

	init acpsdk.InitializeResponse

	// done is closed when the connection ended and the agent is reaped (or
	// given up on). exitErr says why.
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
		sinks:      map[string]*acpSink{},
		states:     map[string]*acpSessionState{},
		defaults:   map[string]acpSessionDefaults{},
		thoughtSet: map[string]string{},
		done:       make(chan struct{}),
		stopped:    make(chan struct{}),
		writes:     make(chan acpWrite),
	}
	p.conn = acpsdk.NewConnection(p.handle, acpStdin{p}, &acpRouter{r: stdout, p: p})
	p.conn.SetLogger(log.With("agent", spec.label()))
	go p.writeLoop()
	go p.watch()

	hctx, cancel := context.WithTimeout(ctx, acpStartTimeout)
	defer cancel()
	err = p.call(hctx, "initialize", acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientInfo:      &acpsdk.Implementation{Name: "coddy", Title: acpsdk.Ptr("Coddy"), Version: version.Version},
	}, &p.init)
	if err == nil && p.init.ProtocolVersion != acpsdk.ProtocolVersionNumber {
		err = &ACPError{Kind: ACPErrorStart, Agent: spec.label(), Message: fmt.Sprintf("the agent speaks ACP protocol version %d; Coddy speaks %d", p.init.ProtocolVersion, acpsdk.ProtocolVersionNumber)}
	}
	if err == nil && strings.TrimSpace(spec.authMethod) != "" {
		err = p.call(hctx, "authenticate", acpsdk.AuthenticateRequest{MethodId: spec.authMethod}, nil)
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

// alive reports whether the agent is still there.
func (p *acpProcess) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// watch ends the agent's side once the connection is over: the agent closed
// its stdout (it is exiting), or the SDK stopped reading it. The agent gets a
// grace to exit, WaitDelay keeps a child holding its stderr from holding
// Wait, and what is left of its process group is ended.
func (p *acpProcess) watch() {
	<-p.conn.Done()
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
	switch werr := p.writeErr.Load(); {
	case p.closing.Load() && werr == nil:
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: errors.New("the agent was stopped")}
	case werr != nil:
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: *werr, Stderr: p.stderr.String()}
	case waitErr != nil:
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: waitErr, Stderr: p.stderr.String()}
	default:
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: errors.New("the agent exited"), Stderr: p.stderr.String()}
	}
	sinks := make([]*acpSink, 0, len(p.sinks))
	for _, s := range p.sinks {
		sinks = append(sinks, s)
	}
	close(p.done)
	p.mu.Unlock()
	for _, s := range sinks {
		s.wake()
	}
}

// acpRouter is the agent's stdout as the SDK reads it. On the way it routes
// every session/update notification itself, in the order the agent wrote
// it: the SDK hands notifications to one queue and requests each to a
// goroutine of its own, so a permission request could otherwise reach its
// prompt ahead of the tool call it asks about.
type acpRouter struct {
	r       io.Reader
	p       *acpProcess
	partial []byte
	skip    bool
}

func (a *acpRouter) Read(b []byte) (int, error) {
	n, err := a.r.Read(b)
	a.scan(b[:n])
	return n, err
}

func (a *acpRouter) scan(chunk []byte) {
	for len(chunk) > 0 {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			if !a.skip {
				a.partial = append(a.partial, chunk...)
				if len(a.partial) > acpMaxLine {
					// The SDK refuses such a line and ends the connection.
					a.partial, a.skip = nil, true
				}
			}
			return
		}
		line := chunk[:i]
		if len(a.partial) > 0 {
			line = append(a.partial, line...)
			a.partial = nil
		}
		if !a.skip {
			a.p.route(line)
		}
		a.skip = false
		chunk = chunk[i+1:]
	}
}

// route takes one line of the agent's: a session/update notification goes
// to the session's state and to the prompt running on it; anything else is
// the SDK's.
func (p *acpProcess) route(line []byte) {
	if !bytes.Contains(line, []byte(`"session/update"`)) {
		return
	}
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &m); err != nil || m.Method != "session/update" || (len(m.ID) > 0 && string(m.ID) != "null") {
		return
	}
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
}

// handle answers what the agent asks of the client through the SDK. Its
// session/update notifications were routed on the way in (acpRouter).
func (p *acpProcess) handle(ctx context.Context, method string, params json.RawMessage) (any, *acpsdk.RequestError) {
	switch method {
	case "session/update":
		return nil, nil
	case "session/request_permission":
		var req acpPermissionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		reply := make(chan acpPermissionOutcome, 1)
		p.mu.Lock()
		sink := p.sinks[req.SessionID]
		if sink != nil {
			sink.push(acpEvent{permission: &req, reply: reply})
		}
		p.mu.Unlock()
		if sink == nil {
			// No prompt runs on that session: nobody can answer.
			return acpPermissionResponse(acpPermissionOutcome{Outcome: "cancelled"}), nil
		}
		select {
		case o := <-reply:
			return acpPermissionResponse(o), nil
		case <-ctx.Done():
		case <-p.done:
		}
		return acpPermissionResponse(acpPermissionOutcome{Outcome: "cancelled"}), nil
	}
	// The client capabilities Coddy advertises are all off: the agent reads,
	// writes and runs with its own tools. Anything it asks of the client is
	// refused rather than left unanswered.
	return nil, acpsdk.NewMethodNotFound(method)
}

// acpPermissionResponse is a permission outcome as the protocol carries it.
func acpPermissionResponse(o acpPermissionOutcome) acpsdk.RequestPermissionResponse {
	if o.Outcome == "selected" && o.OptionID != "" {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected(acpsdk.PermissionOptionId(o.OptionID))}
	}
	return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}
}

// answer hands a permission request its outcome; the first one stands.
func answer(reply chan<- acpPermissionOutcome, o acpPermissionOutcome) {
	if reply == nil {
		return
	}
	select {
	case reply <- o:
	default:
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
	// A caller that already gave up hands the agent nothing.
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.alive() {
		return p.exitError()
	}
	raw, err := acpsdk.SendRequest[json.RawMessage](p.conn, ctx, method, params)
	if err != nil {
		return p.callError(ctx, method, err)
	}
	if out != nil && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, out); err != nil {
			return &ACPError{Kind: ACPErrorRPC, Agent: p.spec.label(), Method: method, Message: "undecodable result: " + err.Error()}
		}
	}
	return nil
}

// callError tells apart why a request of ours failed: our caller gave up,
// the agent is gone, or the agent answered with an error.
func (p *acpProcess) callError(ctx context.Context, method string, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if p.gone() {
		return p.exitError()
	}
	var re *acpsdk.RequestError
	if errors.As(err, &re) {
		return p.rpcError(method, acpRPCErrorOf(re))
	}
	return &ACPError{Kind: ACPErrorRPC, Agent: p.spec.label(), Method: method, Err: err}
}

// gone reports whether the connection ended or a write failed: a request
// that failed then failed for that, whatever the SDK made of it.
func (p *acpProcess) gone() bool {
	if p.writeErr.Load() != nil {
		return true
	}
	select {
	case <-p.conn.Done():
		return true
	default:
		return false
	}
}

// start sends a request without waiting for its response: the returned
// channel yields it, or closes when the agent is gone. The request belongs
// to the agent from then on - no caller's cancellation takes it back - so a
// prompt that ignores its cancellation still yields its answer to whoever
// waits for it.
func (p *acpProcess) start(ctx context.Context, method string, params interface{}) (<-chan acpMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.alive() {
		return nil, p.exitError()
	}
	ch := make(chan acpMessage, 1)
	go func() {
		raw, err := acpsdk.SendRequest[json.RawMessage](p.conn, context.Background(), method, params)
		if err == nil {
			ch <- acpMessage{Result: raw}
			return
		}
		var re *acpsdk.RequestError
		if !p.gone() && errors.As(err, &re) {
			ch <- acpMessage{Error: acpRPCErrorOf(re)}
			return
		}
		close(ch)
	}()
	return ch, nil
}

// notify sends a notification without waiting for the agent to read it.
func (p *acpProcess) notify(method string, params interface{}) {
	go func() { _ = p.conn.SendNotification(context.Background(), method, params) }()
}

// acpRPCErrorOf is the SDK's error object in the provider's terms.
func acpRPCErrorOf(re *acpsdk.RequestError) *acpRPCError {
	out := &acpRPCError{Code: re.Code, Message: re.Message}
	if re.Data != nil {
		if b, err := json.Marshal(re.Data); err == nil {
			out.Data = b
		}
	}
	return out
}

// acpWrite is one message for the writer goroutine.
type acpWrite struct {
	line []byte
	done chan error
}

// writeLoop writes the messages handed to it, one at a time, until the agent
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

// acpStdin is the agent's stdin as the SDK writes to it.
type acpStdin struct{ p *acpProcess }

func (s acpStdin) Write(b []byte) (int, error) { return s.p.write(b) }

// write hands one message to the writer and waits for it under the write
// bound. A message once with the writer cannot be taken back - half a line
// would corrupt the stream - so an agent that does not take it in time is
// stopped, and so is one whose stdin fails.
func (p *acpProcess) write(b []byte) (int, error) {
	if err := p.writeErr.Load(); err != nil {
		return 0, *err
	}
	w := acpWrite{line: append([]byte(nil), b...), done: make(chan error, 1)}
	bound := time.NewTimer(acpWriteTimeout)
	defer bound.Stop()
	fail := func(err error) (int, error) {
		p.writeErr.CompareAndSwap(nil, &err)
		go p.stop()
		return 0, err
	}
	p.writing.Store(time.Now().UnixNano())
	defer p.writing.Store(0)
	select {
	case p.writes <- w:
	case <-p.done:
		return 0, p.exitError()
	case <-bound.C:
		p.log.Warn("acp agent took no input for the write bound; stopping it", "agent", p.spec.label(), "bound", acpWriteTimeout)
		return fail(errors.New("the agent stopped reading its input"))
	}
	select {
	case err := <-w.done:
		if err != nil {
			return fail(err)
		}
		return len(b), nil
	case <-p.done:
		select {
		case err := <-w.done:
			if err == nil {
				return len(b), nil
			}
		default:
		}
		return 0, p.exitError()
	case <-bound.C:
		p.log.Warn("acp agent took no input for the write bound; stopping it", "agent", p.spec.label(), "bound", acpWriteTimeout)
		return fail(errors.New("the agent stopped reading its input"))
	}
}

// writeStuck reports whether a write has waited on the agent for longer
// than d.
func (p *acpProcess) writeStuck(d time.Duration) bool {
	since := p.writing.Load()
	return since != 0 && time.Since(time.Unix(0, since)) > d
}

// rpcError turns an error answer into an ACPError: -32000 is the agent
// asking for authentication, on whatever method it answers.
func (p *acpProcess) rpcError(method string, e *acpRPCError) *ACPError {
	out := &ACPError{Kind: ACPErrorRPC, Agent: p.spec.label(), Method: method, Code: e.Code, Message: e.Message, Data: string(e.Data)}
	if e.Code == acpAuthRequired {
		out.Kind = ACPErrorAuth
		for _, m := range p.init.AuthMethods {
			if id := acpAuthMethodID(m); id != "" {
				out.AuthMethods = append(out.AuthMethods, id)
			}
		}
	}
	return out
}

// acpAuthMethodID is the id of an authentication method, whichever kind.
func acpAuthMethodID(m acpsdk.AuthMethod) string {
	switch {
	case m.Agent != nil:
		return m.Agent.Id
	case m.EnvVar != nil:
		return m.EnvVar.Id
	case m.Terminal != nil:
		return m.Terminal.Id
	}
	return ""
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
				// Reaped: watch ended its group and job; its pid is no
				// longer ours to signal.
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
	update     *acpSessionUpdate
	permission *acpPermissionRequest
	// reply takes the permission request's outcome (buffered, the first
	// answer stands).
	reply chan<- acpPermissionOutcome
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
// so the router never waits on a slow consumer or on a person.
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

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
