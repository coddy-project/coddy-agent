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

// acpMaxLine bounds one line from the agent, as the SDK's reader does: a
// longer line ends the connection there, and the router below gives it up
// too, naming why.
var acpMaxLine = 10 << 20

// errACPDeaf is the write bound running out: the agent stopped reading.
var errACPDeaf = errors.New("the agent stopped reading its input")

// errACPPromptGivenUp refuses the line of a prompt its caller gave up before
// the writer had it.
var errACPPromptGivenUp = errors.New("the prompt was given up before it was sent")

const (
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
	// writeTimeout is acpWriteTimeout as the process started with it.
	writeTimeout time.Duration
	// writing is when the write under way was handed over (Unix nanoseconds),
	// 0 while none is.
	writing atomic.Int64
	// writeErr is why a write failed: the connection is unusable then.
	writeErr atomic.Pointer[error]
	// lineErr is why the router gave up on a line of the agent's: the SDK
	// refuses it too and the connection ends.
	lineErr atomic.Pointer[error]

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
	// prompts tracks the session/prompt in flight on each ACP session.
	prompts map[string]*acpPromptTrack
	// permReplies holds the permission requests the router bound to a
	// prompt, by their params, until the SDK's handler takes them.
	permReplies map[string][]acpPermissionRoute
	// sent names the requests Coddy wrote (session/prompt aside, which its
	// track follows) that the agent has not answered yet, by id; reqSeq
	// numbers them in the order they went out. lateErrors keeps the error
	// answers the router read for them, the last few, for a call the SDK
	// told the connection ended before it handed the answer over.
	sent       map[string]acpSentRequest
	reqSeq     uint64
	lateErrors []acpLateError

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
		spec:         spec,
		log:          log,
		cmd:          cmd,
		stdin:        stdin,
		closeJob:     closeJob,
		stderr:       tail,
		sinks:        map[string]*acpSink{},
		states:       map[string]*acpSessionState{},
		defaults:     map[string]acpSessionDefaults{},
		thoughtSet:   map[string]string{},
		prompts:      map[string]*acpPromptTrack{},
		permReplies:  map[string][]acpPermissionRoute{},
		sent:         map[string]acpSentRequest{},
		writeTimeout: acpWriteTimeout,
		done:         make(chan struct{}),
		stopped:      make(chan struct{}),
		writes:       make(chan acpWrite),
	}
	p.conn = acpsdk.NewConnection(p.handle, acpStdin{p}, &acpRouter{r: stdout, p: p})
	p.conn.SetLogger(log.With("agent", spec.label()))
	go p.writeLoop()
	go p.watch()

	hctx, cancel := context.WithTimeout(ctx, acpStartTimeout)
	defer cancel()
	var initRaw json.RawMessage
	err = p.call(hctx, "initialize", acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientInfo:      &acpsdk.Implementation{Name: "coddy", Title: acpsdk.Ptr("Coddy"), Version: version.Version},
	}, &initRaw)
	if err == nil {
		if p.init, err = decodeACPInitialize(initRaw); err != nil {
			err = &ACPError{Kind: ACPErrorStart, Agent: spec.label(), Method: "initialize", Message: "undecodable result: " + err.Error()}
		}
	}
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
	// The most telling reason first: a line the connection could not take,
	// an agent that stopped reading, Coddy stopping it, then how it exited,
	// and only then a write that failed on its way out.
	werr, lerr := p.writeErr.Load(), p.lineErr.Load()
	switch {
	case lerr != nil:
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: *lerr, Stderr: p.stderr.String()}
	case werr != nil && errors.Is(*werr, errACPDeaf):
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: *werr, Stderr: p.stderr.String()}
	case p.closing.Load():
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: errors.New("the agent was stopped")}
	case waitErr != nil:
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: waitErr, Stderr: p.stderr.String()}
	case werr != nil:
		p.exitErr = &ACPError{Kind: ACPErrorExited, Agent: p.spec.label(), Err: *werr, Stderr: p.stderr.String()}
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

// acpRouter is the agent's stdout as the SDK reads it. On the way it reads
// every line itself, in the order the agent wrote it, before the SDK does:
// session/update notifications and session/request_permission requests go
// to the prompt running on their session then, and the answer of a prompt
// is kept. The SDK hands notifications to one queue and requests each to a
// goroutine of its own, so a permission request could otherwise reach its
// prompt ahead of the tool call it asks about, or after the prompt ended.
type acpRouter struct {
	r       io.Reader
	p       *acpProcess
	partial []byte
	skip    bool
}

func (a *acpRouter) Read(b []byte) (int, error) {
	n, err := a.r.Read(b)
	a.scan(b[:n])
	if err == io.EOF && len(a.partial) > 0 && !a.skip {
		// A last line with no newline: the SDK's reader takes it too.
		a.p.route(a.partial)
		a.partial = nil
	}
	return n, err
}

func (a *acpRouter) scan(chunk []byte) {
	for len(chunk) > 0 {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			if !a.skip {
				a.partial = append(a.partial, chunk...)
				if len(a.partial) > acpMaxLine {
					a.partial, a.skip = nil, true
					a.p.lineTooLong()
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
			if len(line) > acpMaxLine {
				a.p.lineTooLong()
			} else {
				a.p.route(line)
			}
		}
		a.skip = false
		chunk = chunk[i+1:]
	}
}

// lineTooLong ends an agent whose line the SDK refuses: it would block on
// its stdout while the connection is gone.
func (p *acpProcess) lineTooLong() {
	err := fmt.Errorf("the agent wrote a JSON-RPC line longer than %d bytes", acpMaxLine)
	if p.lineErr.CompareAndSwap(nil, &err) && p.cmd != nil {
		go func() { _ = platform.TerminateProcessGroup(p.cmd, acpTermGrace) }()
	}
}

// route takes one line of the agent's, read before the SDK reads it.
func (p *acpProcess) route(line []byte) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *acpRPCError    `json:"error"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return
	}
	hasID := len(m.ID) > 0 && string(m.ID) != "null"
	switch {
	case m.Method == "session/update" && !hasID:
		p.routeUpdate(m.Params)
	case m.Method == "session/request_permission" && hasID:
		p.routePermission(m.Params)
	case m.Method == "" && hasID:
		p.noteAnswer(m.ID, acpMessage{Result: m.Result, Error: m.Error})
	}
}

func (p *acpProcess) routeUpdate(params json.RawMessage) {
	var n acpSessionNotification
	if err := json.Unmarshal(params, &n); err != nil {
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

// acpPermissionRoute is a permission request as the router bound it: the
// prompt's reply channel, or none when no prompt ran on its session.
type acpPermissionRoute struct {
	reply  chan acpPermissionOutcome
	routed bool
}

// routePermission binds a permission request to the prompt running on its
// session now, and files the binding for the SDK's handler, which answers it.
func (p *acpProcess) routePermission(params json.RawMessage) {
	var req acpPermissionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return // the handler answers invalid params
	}
	reply := make(chan acpPermissionOutcome, 1)
	key := string(params)
	p.mu.Lock()
	sink := p.sinks[req.SessionID]
	if sink != nil {
		sink.push(acpEvent{permission: &req, reply: reply})
	}
	p.permReplies[key] = append(p.permReplies[key], acpPermissionRoute{reply: reply, routed: sink != nil})
	p.mu.Unlock()
}

// noteAnswer keeps the answer of a prompt in flight: the SDK gives up an
// answer the agent's exit follows too closely.
func (p *acpProcess) noteAnswer(id json.RawMessage, m acpMessage) {
	key := string(bytes.TrimSpace(id))
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, tr := range p.prompts {
		if tr.id == key && tr.answer == nil {
			tr.answer = &m
		}
	}
	if req, ok := p.sent[key]; ok {
		delete(p.sent, key)
		if m.Error != nil {
			p.lateErrors = append(p.lateErrors, acpLateError{method: req.method, seq: req.seq, err: m.Error})
			if n := len(p.lateErrors); n > acpLateErrorsKept {
				p.lateErrors = append([]acpLateError(nil), p.lateErrors[n-acpLateErrorsKept:]...)
			}
		}
	}
}

// acpBeforePromptWait, when a test sets it, runs between handing a prompt to
// its goroutine and waiting for its line to go out: it widens the window in
// which the agent answers and exits first.
var acpBeforePromptWait atomic.Pointer[func()]

// promptAfterExit answers a prompt whose agent is gone before startPrompt
// saw its line go out: an agent that took the line, answered and exited at
// once leaves both ready, and select picks either. watch closes done only
// after the connection ended, so after the router read every line the agent
// wrote: an answer it wrote is on the track by now, and the prompt's
// goroutine puts it on ch. Without one the prompt ends with the exit.
func (p *acpProcess) promptAfterExit(tr *acpPromptTrack, ch <-chan acpMessage) (<-chan acpMessage, error) {
	p.mu.Lock()
	answered := tr.answer != nil
	p.mu.Unlock()
	if answered {
		return ch, nil
	}
	return nil, p.exitError()
}

// keptAnswer returns the answer the router read for the prompt in flight on
// a session, if it read one: the prompt's line is out and its track still
// stands (once startPrompt's goroutine let go of it, the answer is on that
// goroutine's channel).
func (p *acpProcess) keptAnswer(sessionID string) (acpMessage, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tr := p.prompts[sessionID]
	if tr == nil || tr.answer == nil {
		return acpMessage{}, false
	}
	return *tr.answer, true
}

// acpSentRequest is a request Coddy wrote: its method and its place in the
// order requests went out.
type acpSentRequest struct {
	method string
	seq    uint64
}

// acpLateError is the error answer of a sent request, as the router read it.
type acpLateError struct {
	method string
	seq    uint64
	err    *acpRPCError
}

// acpLateErrorsKept bounds lateErrors: they matter only while the calls of a
// connection that just ended return.
const acpLateErrorsKept = 16

// noteRequest records a request line on its way to the agent, before it is
// written, so its answer cannot arrive first. A prompt's line is its
// track's business (handPrompt).
func (p *acpProcess) noteRequest(b []byte) {
	if !bytes.Contains(b, []byte(`"method"`)) {
		return
	}
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(b, &m); err != nil || m.Method == "" || m.Method == "session/prompt" {
		return
	}
	id := string(bytes.TrimSpace(m.ID))
	if id == "" || id == "null" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqSeq++
	p.sent[id] = acpSentRequest{method: m.Method, seq: p.reqSeq}
}

// requestSeq is the number of the last request that went out: a call takes
// it before its own request, and answers to later requests may be its.
func (p *acpProcess) requestSeq() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reqSeq
}

// lateError takes the first error answer the router read for a request of
// method sent after the request numbered after: the answer of a call the
// SDK gave up for the connection's end. Two calls of one method on a
// connection that ends may swap their errors; neither gets a bare exit.
func (p *acpProcess) lateError(method string, after uint64) *acpRPCError {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, e := range p.lateErrors {
		if e.method == method && e.seq > after {
			p.lateErrors = append(p.lateErrors[:i:i], p.lateErrors[i+1:]...)
			return e.err
		}
	}
	return nil
}

// handle answers what the agent asks of the client through the SDK. Its
// session/update notifications were routed on the way in, and its
// permission requests bound to their prompt there.
func (p *acpProcess) handle(ctx context.Context, method string, params json.RawMessage) (any, *acpsdk.RequestError) {
	switch method {
	case "session/update":
		return nil, nil
	case "session/request_permission":
		key := string(params)
		p.mu.Lock()
		routes := p.permReplies[key]
		var rt acpPermissionRoute
		found := len(routes) > 0
		if found {
			rt = routes[0]
			if len(routes) == 1 {
				delete(p.permReplies, key)
			} else {
				p.permReplies[key] = routes[1:]
			}
		}
		p.mu.Unlock()
		if !found {
			var req acpPermissionRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
			}
		}
		if !rt.routed {
			// No prompt ran on that session: nobody can answer.
			return acpPermissionResponse(acpPermissionOutcome{Outcome: "cancelled"}), nil
		}
		select {
		case o := <-rt.reply:
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
// It returns when its context ends, whatever the agent does with the line.
func (p *acpProcess) call(ctx context.Context, method string, params, out interface{}) error {
	// A caller that already gave up hands the agent nothing.
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.alive() {
		return p.exitError()
	}
	type result struct {
		raw json.RawMessage
		err error
	}
	done := make(chan result, 1)
	start := p.requestSeq()
	go func() {
		raw, err := acpsdk.SendRequest[json.RawMessage](p.conn, ctx, method, params)
		done <- result{raw, err}
	}()
	var r result
	select {
	case r = <-done:
	case <-ctx.Done():
		// The SDK writes without a context: a line that cannot go out holds
		// the request. An agent that has not taken a line for the grace does
		// not read its input and is stopped, which ends the request too.
		go func() {
			grace := time.NewTimer(acpWriteGrace)
			defer grace.Stop()
			select {
			case <-done:
			case <-grace.C:
				if p.writeStuck(acpWriteGrace) {
					p.log.Warn("acp agent does not read its input; stopping it", "agent", p.spec.label())
					p.stop()
				}
			}
		}()
		return ctx.Err()
	}
	if r.err != nil {
		return p.callError(ctx, method, start, r.err)
	}
	if out != nil && len(r.raw) > 0 && string(r.raw) != "null" {
		if err := json.Unmarshal(r.raw, out); err != nil {
			return &ACPError{Kind: ACPErrorRPC, Agent: p.spec.label(), Method: method, Message: "undecodable result: " + err.Error()}
		}
	}
	return nil
}

// callError tells apart why a request of ours failed: our caller gave up,
// the connection is gone, or the agent answered with an error. It goes by
// the error itself, never by whether the agent has exited since: an agent
// may answer with an error and exit right after. When the SDK reports the
// connection's end, the error answer the router read for a request of the
// call's method sent after start (requestSeq before the call) is the call's
// answer: the SDK gives up an answer the exit follows too closely.
func (p *acpProcess) callError(ctx context.Context, method string, start uint64, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	var re *acpsdk.RequestError
	if errors.As(err, &re) && !p.disconnected(re) {
		return p.rpcError(method, acpRPCErrorOf(re))
	}
	if re != nil {
		if late := p.lateError(method, start); late != nil {
			return p.rpcError(method, late)
		}
		return p.exitError()
	}
	return &ACPError{Kind: ACPErrorRPC, Agent: p.spec.label(), Method: method, Err: err}
}

// disconnected reports whether an error the SDK returned for a request is
// the connection's end (or a write that failed) rather than the agent's
// answer. Besides the SDK's own "peer disconnected", that is our exit error:
// a write that saw the agent end before the writer reported the line written
// returns it, and the SDK fails the request with its text. The process is
// done only after the SDK read everything the agent wrote, so its answers
// are with the router by then.
func (p *acpProcess) disconnected(re *acpsdk.RequestError) bool {
	if p.writeErr.Load() != nil {
		return true
	}
	if re.Code != -32603 {
		return false
	}
	d, ok := re.Data.(map[string]any)
	if !ok {
		return false
	}
	msg, _ := d["error"].(string)
	if strings.HasPrefix(msg, "peer disconnected") {
		return true
	}
	if !p.alive() {
		if exit := p.exitError(); exit != nil && msg == exit.Error() {
			return true
		}
	}
	return false
}

// acpPromptTrack follows the session/prompt in flight on one ACP session.
type acpPromptTrack struct {
	// sent closes once the prompt's line is out, or will never be.
	sent     chan struct{}
	sentOnce sync.Once
	// handed: the writer has the line. given: the caller gave up first,
	// and the line is never written.
	handed, given bool
	// id is the request's JSON-RPC id once written; answer is its response
	// as the router read it.
	id     string
	answer *acpMessage
}

func (t *acpPromptTrack) markSent() { t.sentOnce.Do(func() { close(t.sent) }) }

// startPrompt sends a session/prompt and returns once its line is out: the
// returned channel yields its response, or closes when the agent is gone. A
// cancellation sent after it reaches the agent after the prompt. The prompt
// belongs to the agent from then on - no caller's cancellation takes it back
// - so a prompt that ignores its cancellation still yields its answer to
// whoever waits for it. A caller that gives up before the line is out sends
// nothing.
func (p *acpProcess) startPrompt(ctx context.Context, req acpsdk.PromptRequest) (<-chan acpMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.alive() {
		return nil, p.exitError()
	}
	sid := string(req.SessionId)
	tr := &acpPromptTrack{sent: make(chan struct{})}
	p.mu.Lock()
	p.prompts[sid] = tr
	p.mu.Unlock()
	ch := make(chan acpMessage, 1)
	go func() { //nolint:gosec // G118: once its line is out the prompt belongs to the agent, see startPrompt; a cancellation goes as session/cancel
		defer tr.markSent()
		defer func() {
			p.mu.Lock()
			if p.prompts[sid] == tr {
				delete(p.prompts, sid)
			}
			p.mu.Unlock()
		}()
		raw, err := acpsdk.SendRequest[json.RawMessage](p.conn, context.Background(), "session/prompt", req)
		if err == nil {
			ch <- acpMessage{Result: raw}
			return
		}
		var re *acpsdk.RequestError
		if errors.As(err, &re) && !p.disconnected(re) {
			ch <- acpMessage{Error: acpRPCErrorOf(re)}
			return
		}
		// The SDK gives up an answer the agent's exit followed too closely;
		// the router read it.
		p.mu.Lock()
		ans := tr.answer
		p.mu.Unlock()
		if ans != nil {
			ch <- *ans
			return
		}
		close(ch)
	}()
	if wait := acpBeforePromptWait.Load(); wait != nil {
		(*wait)()
	}
	select {
	case <-tr.sent:
		return ch, nil
	case <-p.done:
		return p.promptAfterExit(tr, ch)
	case <-ctx.Done():
	}
	p.mu.Lock()
	handed := tr.handed
	if !handed {
		tr.given = true
	}
	p.mu.Unlock()
	if !handed {
		return nil, ctx.Err()
	}
	// The line is with the writer: an agent that reads takes it within the
	// grace, and the caller then cancels it the protocol's way.
	grace := time.NewTimer(acpWriteGrace)
	defer grace.Stop()
	select {
	case <-tr.sent:
		return ch, nil
	case <-p.done:
		return p.promptAfterExit(tr, ch)
	case <-grace.C:
		p.log.Warn("acp agent does not read its input; stopping it", "agent", p.spec.label())
		go p.stop()
		return nil, ctx.Err()
	}
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
// stopped, and so is one whose stdin fails. The line of a prompt is marked
// out once written, and refused when its caller gave it up first.
func (p *acpProcess) write(b []byte) (int, error) {
	if err := p.writeErr.Load(); err != nil {
		return 0, *err
	}
	tr, err := p.handPrompt(b)
	if err != nil {
		return 0, err
	}
	if tr == nil {
		p.noteRequest(b)
	}
	w := acpWrite{line: append([]byte(nil), b...), done: make(chan error, 1)}
	bound := time.NewTimer(p.writeTimeout)
	defer bound.Stop()
	fail := func(err error) (int, error) {
		// A write that fails because Coddy is stopping the agent says
		// nothing of why the agent ended.
		if !p.closing.Load() {
			p.writeErr.CompareAndSwap(nil, &err)
		}
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
		p.log.Warn("acp agent took no input for the write bound; stopping it", "agent", p.spec.label(), "bound", p.writeTimeout)
		return fail(errACPDeaf)
	}
	select {
	case err := <-w.done:
		if err != nil {
			return fail(err)
		}
		if tr != nil {
			tr.markSent()
		}
		return len(b), nil
	case <-p.done:
		select {
		case err := <-w.done:
			if err == nil {
				if tr != nil {
					tr.markSent()
				}
				return len(b), nil
			}
		default:
		}
		return 0, p.exitError()
	case <-bound.C:
		p.log.Warn("acp agent took no input for the write bound; stopping it", "agent", p.spec.label(), "bound", p.writeTimeout)
		return fail(errACPDeaf)
	}
}

// handPrompt finds the prompt a line carries, if any: it notes the
// request's id and that the writer has the line, or refuses a line its
// caller gave up first.
func (p *acpProcess) handPrompt(b []byte) (*acpPromptTrack, error) {
	if !bytes.Contains(b, []byte(`"session/prompt"`)) {
		return nil, nil
	}
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			SessionID string `json:"sessionId"`
		} `json:"params"`
	}
	if err := json.Unmarshal(b, &m); err != nil || m.Method != "session/prompt" {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	tr := p.prompts[m.Params.SessionID]
	if tr == nil {
		return nil, nil
	}
	if tr.given {
		return nil, errACPPromptGivenUp
	}
	tr.handed = true
	tr.id = string(bytes.TrimSpace(m.ID))
	return tr, nil
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

// decodeACPInitialize reads an initialize result: the SDK's type when the
// result fits it, else the few fields the provider uses, read leniently, so
// a field of a shape the SDK's schema does not expect does not cost the
// agent.
func decodeACPInitialize(raw json.RawMessage) (acpsdk.InitializeResponse, error) {
	var out acpsdk.InitializeResponse
	if err := json.Unmarshal(raw, &out); err == nil {
		return out, nil
	}
	var loose struct {
		ProtocolVersion   json.RawMessage `json:"protocolVersion"`
		AgentCapabilities struct {
			PromptCapabilities struct {
				Image json.RawMessage `json:"image"`
			} `json:"promptCapabilities"`
			SessionCapabilities struct {
				Close json.RawMessage `json:"close"`
			} `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
		AuthMethods []json.RawMessage `json:"authMethods"`
	}
	if err := json.Unmarshal(raw, &loose); err != nil {
		return out, err
	}
	var version int
	if err := json.Unmarshal(loose.ProtocolVersion, &version); err != nil {
		return out, fmt.Errorf("protocolVersion: %w", err)
	}
	out.ProtocolVersion = acpsdk.ProtocolVersion(version)
	out.AgentCapabilities.PromptCapabilities.Image = string(loose.AgentCapabilities.PromptCapabilities.Image) == "true"
	switch c := string(bytes.TrimSpace(loose.AgentCapabilities.SessionCapabilities.Close)); c {
	case "", "null", "false":
	default:
		out.AgentCapabilities.SessionCapabilities.Close = &acpsdk.SessionCloseCapabilities{}
	}
	for _, m := range loose.AuthMethods {
		var a struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(m, &a) == nil && a.ID != "" {
			out.AuthMethods = append(out.AuthMethods, acpsdk.AuthMethod{Agent: &acpsdk.AuthMethodAgent{Id: a.ID, Name: a.Name}})
		}
	}
	return out, nil
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
