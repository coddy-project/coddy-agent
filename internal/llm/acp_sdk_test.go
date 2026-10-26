package llm

// What the client on the protocol's Go SDK has to keep of the one before it:
// the order of what goes to the agent and of what comes from it, an answer
// the agent exits right after, a cancelled call, and the lines the SDK's
// reader and the provider's router read alike.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/EvilFreelancer/coddy-agent/internal/llm/acptest"
)

func startTestProcess(t *testing.T, script acptest.Script) (*acpProcess, string) {
	t.Helper()
	in, logPath := acpRow(t, script)
	spec, err := resolveACPSpec(in, "s1", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := startACPProcess(context.Background(), spec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.stop)
	return p, logPath
}

func promptRequest(sid, text string) acpsdk.PromptRequest {
	return acpsdk.PromptRequest{SessionId: acpsdk.SessionId(sid), Prompt: acpSDKBlocks([]acpContentBlock{{Type: "text", Text: text}})}
}

// A cancellation sent right after the prompt reaches the agent after it,
// however long the prompt: an agent ignores a cancel with no prompt running,
// and would then run the whole prompt.
func TestACPProcessSendsTheCancelAfterThePrompt(t *testing.T) {
	for _, size := range []int{5, 4 << 20} {
		runs := 10
		if size > 1<<20 {
			runs = 4
		}
		for i := 0; i < runs; i++ {
			p, logPath := startTestProcess(t, acptest.Script{Turns: []acptest.Turn{{WaitCancel: true}}})
			sid, err := p.newSession(context.Background(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ch, err := p.startPrompt(context.Background(), promptRequest(sid, strings.Repeat("y", size)))
			if err != nil {
				t.Fatal(err)
			}
			p.notify("session/cancel", acpsdk.CancelNotification{SessionId: acpsdk.SessionId(sid)})
			select {
			case <-ch:
			case <-time.After(10 * time.Second):
				t.Fatal("the cancelled prompt never ended")
			}
			var order []string
			for _, r := range acptest.ReadLog(t, logPath) {
				if r.Method == "session/prompt" || r.Method == "session/cancel" {
					order = append(order, r.Method)
				}
			}
			if len(order) != 2 || order[0] != "session/prompt" {
				t.Fatalf("prompt of %d bytes, run %d: the agent read %v", size, i, order)
			}
		}
	}
}

// A call whose context ends while its line waits behind a write the agent
// does not take returns at its context, not at the write bound.
func TestACPProcessCancelledCallReturnsAtItsContext(t *testing.T) {
	old := acpWriteTimeout
	acpWriteTimeout = 8 * time.Second
	t.Cleanup(func() { acpWriteTimeout = old })
	p, _ := startTestProcess(t, acptest.Script{DeafAfterNew: true})
	sid, err := p.newSession(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A prompt the deaf agent never takes holds the writer.
	go func() { _, _ = p.startPrompt(context.Background(), promptRequest(sid, strings.Repeat("x", 1<<20))) }()
	time.Sleep(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = p.call(ctx, "session/new", acpsdk.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acpsdk.McpServer{}}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the call returned %v after its 300ms context", d)
	}
}

// The agent took the prompt, answered and exited before the caller looked:
// the exit and the line going out are both ready, and the answer the router
// read still reaches the caller, a result or an error alike. Without the
// widened window this ordering comes up about once in a few hundred runs.
func TestACPProviderKeepsAnAnswerItsExitRacedThePrompt(t *testing.T) {
	wait := func() { time.Sleep(50 * time.Millisecond) }
	acpBeforePromptWait.Store(&wait)
	t.Cleanup(func() { acpBeforePromptWait.Store(nil) })
	for i := 0; i < 3; i++ {
		in, _ := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Text: []string{"done"}, ExitAfterReply: true}}})
		resp, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil)
		if err != nil || !strings.HasPrefix(resp.Content, "done") {
			t.Fatalf("run %d: err = %v, resp = %+v", i, err, resp)
		}
		StopACPAgents()
		in, _ = acpRow(t, acptest.Script{Turns: []acptest.Turn{{Error: "upstream failed", ExitAfterReply: true}}})
		_, err = newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil)
		var acpErr *ACPError
		if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorRPC {
			t.Fatalf("run %d: err = %v, want the agent's rpc error", i, err)
		}
		StopACPAgents()
	}
}

// The agent answers and exits at once: the answer stands, an error answer
// stays the agent's error, and an authentication error keeps its methods.
func TestACPProviderKeepsAnAnswerTheAgentExitedAfter(t *testing.T) {
	for i := 0; i < 15; i++ {
		in, _ := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Text: []string{"done"}, Chunks: 300, ExitAfterReply: true}}})
		resp, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil)
		if err != nil || !strings.HasPrefix(resp.Content, "done") {
			t.Fatalf("run %d: err = %v, resp = %+v", i, err, resp)
		}
		StopACPAgents()
	}
	for i := 0; i < 15; i++ {
		in, _ := acpRow(t, acptest.Script{Turns: []acptest.Turn{{Tools: []acptest.Tool{{ID: "t1", Title: "x"}}, Error: "upstream failed", ErrorAfterTools: true, ExitAfterReply: true}}})
		_, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil)
		var acpErr *ACPError
		if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorRPC {
			t.Fatalf("run %d: err = %v, want the agent's rpc error", i, err)
		}
		StopACPAgents()
	}
	for i := 0; i < 15; i++ {
		in, _ := acpRow(t, acptest.Script{AuthMethods: []string{"login"}, RequireAuth: "login", ExitAfterNewError: true})
		_, err := newACPTestProvider(t, in).Stream(conversation(t, "s1"), sys(u("one")), nil, nil)
		var acpErr *ACPError
		if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorAuth || strings.Join(acpErr.AuthMethods, ",") != "login" {
			t.Fatalf("run %d: err = %v, want the authentication error with its method", i, err)
		}
		StopACPAgents()
	}
}

// The SDK may hand a request's caller "peer disconnected" when the agent's
// answer and its exit arrive together. The router read the answer first: an
// error answer of a request of the call's method, written after the call
// began, is the call's error, the agent's own words (an authentication error
// keeps its methods) instead of a bare exit. One written before the call
// began answers an earlier request and is not taken.
func TestACPCallKeepsAnErrorAnswerTheExitOvertook(t *testing.T) {
	p := &acpProcess{spec: acpSpec{command: "agent"}, sent: map[string]acpSentRequest{}}
	p.init.AuthMethods = []acpsdk.AuthMethod{{Agent: &acpsdk.AuthMethodAgent{Id: "login", Name: "login"}}}
	disconnected := acpsdk.NewInternalError(map[string]any{"error": "peer disconnected before response"})

	p.noteRequest([]byte(`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/w"}}`))
	p.noteAnswer(json.RawMessage(`1`), acpMessage{Error: &acpRPCError{Code: -32602, Message: "stale"}})
	start := p.requestSeq()
	p.noteRequest([]byte(`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/w"}}`))
	p.noteAnswer(json.RawMessage(`2`), acpMessage{Error: &acpRPCError{Code: acpAuthRequired, Message: "Authentication required"}})

	err := p.callError(context.Background(), "session/new", start, disconnected)
	var acpErr *ACPError
	if !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorAuth || strings.Join(acpErr.AuthMethods, ",") != "login" {
		t.Fatalf("err = %v, want the authentication error with its method", err)
	}
	if len(p.sent) != 0 {
		t.Fatalf("answered requests are still kept: %v", p.sent)
	}
	// Taken once: the next disconnected call of the method finds nothing.
	p.done = make(chan struct{})
	close(p.done)
	p.exitErr = errors.New("exited")
	if err := p.callError(context.Background(), "session/new", start, disconnected); !errors.Is(err, p.exitErr) {
		t.Fatalf("err = %v, want the exit", err)
	}
}

// A request cancelAll took is answered cancelled, even when the person's
// answer lands between cancelAll taking it and answering it.
func TestACPPermissionsCancelAllWinsOverALateAnswer(t *testing.T) {
	perms := newACPPermissions(nil)
	reply := make(chan acpPermissionOutcome, 1)
	gate := make(chan struct{})
	perms.handle(context.Background(), reply, func(context.Context) acpPermissionOutcome {
		<-gate
		return acpPermissionOutcome{Outcome: "selected", OptionID: "allow"}
	})
	perms.mu.Lock()
	perms.closed = true
	pending := perms.pending
	perms.pending = map[chan<- acpPermissionOutcome]context.CancelFunc{}
	perms.mu.Unlock()
	close(gate)
	<-perms.settled
	for r, cancel := range pending {
		cancel()
		answer(r, acpPermissionOutcome{Outcome: "cancelled"})
	}
	if o := <-reply; o.Outcome != "cancelled" {
		t.Fatalf("a request cancelAll took was answered %q", o.Outcome)
	}
}

func routerProcess() (*acpProcess, *acpSink) {
	p := &acpProcess{
		sinks:       map[string]*acpSink{},
		states:      map[string]*acpSessionState{},
		permReplies: map[string][]acpPermissionRoute{},
		prompts:     map[string]*acpPromptTrack{},
		done:        make(chan struct{}),
	}
	sink := newACPSink()
	p.sinks["s"] = sink
	return p, sink
}

func readAll(t *testing.T, r io.Reader) {
	t.Helper()
	if _, err := io.ReadAll(r); err != nil {
		t.Fatal(err)
	}
}

const routedUpdate = `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}}}`

// The router reads the agent's lines the way the SDK's reader does.
func TestACPRouterReadsEveryLineTheSDKReads(t *testing.T) {
	cases := map[string]string{
		"escaped slash in the method": strings.Replace(routedUpdate, "session/update", `session\/update`, 1) + "\n",
		"two lines in one read":       routedUpdate + "\n" + routedUpdate + "\n",
		"a CRLF line":                 routedUpdate + "\r\n",
		"a last line with no newline": routedUpdate,
	}
	want := map[string]int{"two lines in one read": 2}
	for name, input := range cases {
		p, sink := routerProcess()
		readAll(t, &acpRouter{r: strings.NewReader(input), p: p})
		n := want[name]
		if n == 0 {
			n = 1
		}
		if got := len(sink.drain()); got != n {
			t.Errorf("%s: routed %d events, want %d", name, got, n)
		}
	}
	p, sink := routerProcess()
	readAll(t, &acpRouter{r: iotest.OneByteReader(strings.NewReader(routedUpdate + "\n")), p: p})
	if got := len(sink.drain()); got != 1 {
		t.Errorf("a line read a byte at a time: routed %d events, want 1", got)
	}
}

// A line longer than the bound ends the connection: the router names why
// and does not hold the line, and a line after it is read again.
func TestACPRouterSkipsALineOverTheBound(t *testing.T) {
	old := acpMaxLine
	acpMaxLine = 1 << 10
	t.Cleanup(func() { acpMaxLine = old })
	p, sink := routerProcess()
	long := `{"jsonrpc":"2.0","method":"session/update","params":{"x":"` + strings.Repeat("z", 4<<10) + `"}}`
	readAll(t, &acpRouter{r: iotest.HalfReader(strings.NewReader(long + "\n" + routedUpdate + "\n")), p: p})
	if got := len(sink.drain()); got != 1 {
		t.Fatalf("routed %d events, want the line after the long one", got)
	}
	if err := p.lineErr.Load(); err == nil || !strings.Contains((*err).Error(), "longer than") {
		t.Fatalf("lineErr = %v", err)
	}
}

// A permission request is bound to the prompt that runs when the agent
// wrote it, in the order of the agent's lines.
func TestACPRouterRoutesAPermissionRequestInOrder(t *testing.T) {
	p, sink := routerProcess()
	request := `{"jsonrpc":"2.0","id":7,"method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"t1"},"options":[{"optionId":"yes","name":"Allow","kind":"allow_once"}]}}`
	readAll(t, &acpRouter{r: strings.NewReader(routedUpdate + "\n" + request + "\n" + routedUpdate + "\n"), p: p})
	events := sink.drain()
	if len(events) != 3 || events[0].update == nil || events[1].permission == nil || events[2].update == nil {
		t.Fatalf("events = %+v", events)
	}
	var m struct {
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal([]byte(request), &m)
	go answer(events[1].reply, acpPermissionOutcome{Outcome: "selected", OptionID: "yes"})
	res, rerr := p.handle(context.Background(), "session/request_permission", m.Params)
	if rerr != nil {
		t.Fatal(rerr)
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), `"optionId":"yes"`) {
		t.Fatalf("answer = %s", b)
	}
}

// The initialize result is read as leniently as before: a field of an
// unexpected shape does not cost the agent.
func TestACPInitializeIsReadLeniently(t *testing.T) {
	for _, raw := range []string{
		`{"protocolVersion":1,"agentCapabilities":{"promptCapabilities":{"image":true},"sessionCapabilities":{"close":true}},"authMethods":[{"id":"login","name":"Login"}]}`,
		`{"protocolVersion":1,"agentCapabilities":{"loadSession":"yes","promptCapabilities":{"image":true},"sessionCapabilities":{"close":{}}},"authMethods":[{"id":"login"}]}`,
		`{"protocolVersion":1,"agentCapabilities":{"promptCapabilities":{"image":true},"sessionCapabilities":{"close":{}},"nes":"on"},"authMethods":[{"id":"login","type":"oauth"}]}`,
	} {
		init, err := decodeACPInitialize(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		p := &acpProcess{init: init}
		ids := []string{}
		for _, m := range init.AuthMethods {
			ids = append(ids, acpAuthMethodID(m))
		}
		if init.ProtocolVersion != 1 || !init.AgentCapabilities.PromptCapabilities.Image || !p.canClose() || strings.Join(ids, ",") != "login" {
			t.Fatalf("%s: read as %+v", raw, init)
		}
	}
}
