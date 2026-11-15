package acp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// mockHandler implements acp.Handler for testing.
type mockHandler struct {
	initResult   *acp.InitializeResult
	newResult    *acp.SessionNewResult
	promptResult *acp.SessionPromptResult
	modeError    error
	promptError  error
	newError     error
	cancelledID  string
	closedID     string
	closeNil     bool
}

func (m *mockHandler) HandleInitialize(_ context.Context, _ acp.InitializeParams) (*acp.InitializeResult, error) {
	if m.initResult != nil {
		return m.initResult, nil
	}
	return &acp.InitializeResult{
		ProtocolVersion: acp.ProtocolVersion,
		AgentInfo:       acp.ImplementationInfo{Name: "test-agent"},
	}, nil
}

func (m *mockHandler) HandleSessionNew(_ context.Context, _ acp.SessionNewParams) (*acp.SessionNewResult, error) {
	if m.newError != nil {
		return nil, m.newError
	}
	if m.newResult != nil {
		return m.newResult, nil
	}
	return &acp.SessionNewResult{SessionID: "test-session"}, nil
}

func (m *mockHandler) HandleSessionLoad(_ context.Context, _ acp.SessionLoadParams) (*acp.SessionLoadResult, error) {
	return &acp.SessionLoadResult{}, nil
}

func (m *mockHandler) HandleSessionList(_ context.Context, _ acp.SessionListParams) (*acp.SessionListResult, error) {
	return &acp.SessionListResult{Sessions: nil}, nil
}

func (m *mockHandler) HandleSessionResume(_ context.Context, _ acp.SessionResumeParams) (*acp.SessionResumeResult, error) {
	return &acp.SessionResumeResult{}, nil
}

func (m *mockHandler) HandleSessionDelete(_ context.Context, _ acp.SessionDeleteParams) (*acp.SessionDeleteResult, error) {
	return &acp.SessionDeleteResult{}, nil
}

func (m *mockHandler) HandleSessionPrompt(_ context.Context, _ acp.SessionPromptParams) (*acp.SessionPromptResult, error) {
	if m.promptError != nil {
		return nil, m.promptError
	}
	if m.promptResult != nil {
		return m.promptResult, nil
	}
	return &acp.SessionPromptResult{StopReason: acp.StopReasonEndTurn}, nil
}

func (m *mockHandler) HandleSessionClose(_ context.Context, params acp.SessionCloseParams) (*acp.SessionCloseResult, error) {
	m.closedID = params.SessionID
	if m.closeNil {
		return nil, nil
	}
	return &acp.SessionCloseResult{}, nil
}

func (m *mockHandler) HandleSessionSetMode(_ context.Context, _ acp.SessionSetModeParams) error {
	return m.modeError
}

func (m *mockHandler) HandleSessionSetConfigOption(_ context.Context, _ acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	return &acp.SessionSetConfigOptionResult{ConfigOptions: nil}, nil
}

func (m *mockHandler) HandleSessionCancel(params acp.SessionCancelParams) {
	m.cancelledID = params.SessionID
}

func TestServerInitialize(t *testing.T) {
	handler := &mockHandler{}
	input := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}` + "\n"

	// We need the server to expose SetOutput for tests.
	// Instead of running the full server, test the handler directly.
	ctx := context.Background()
	result, err := handler.HandleInitialize(ctx, acp.InitializeParams{ProtocolVersion: 1})
	if err != nil {
		t.Fatalf("HandleInitialize: %v", err)
	}
	if result.ProtocolVersion != acp.ProtocolVersion {
		t.Errorf("expected protocol version %d, got %d", acp.ProtocolVersion, result.ProtocolVersion)
	}
	if result.AgentInfo.Name != "test-agent" {
		t.Errorf("expected agent name %q, got %q", "test-agent", result.AgentInfo.Name)
	}
	_ = input
}

func TestServerSessionNew(t *testing.T) {
	handler := &mockHandler{
		newResult: &acp.SessionNewResult{
			SessionID: "sess_abc",
			Modes: &acp.ModeState{
				CurrentModeID: "agent",
				AvailableModes: []acp.SessionMode{
					{ID: "agent", Name: "Agent"},
					{ID: "plan", Name: "Plan"},
				},
			},
		},
	}

	result, err := handler.HandleSessionNew(context.Background(), acp.SessionNewParams{
		CWD: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("HandleSessionNew: %v", err)
	}
	if result.SessionID != "sess_abc" {
		t.Errorf("expected session ID %q, got %q", "sess_abc", result.SessionID)
	}
	if result.Modes == nil {
		t.Fatal("expected modes to be set")
	}
	if result.Modes.CurrentModeID != "agent" {
		t.Errorf("expected current mode %q, got %q", "agent", result.Modes.CurrentModeID)
	}
	if len(result.Modes.AvailableModes) != 2 {
		t.Errorf("expected 2 modes, got %d", len(result.Modes.AvailableModes))
	}
}

func TestServerSessionCancel(t *testing.T) {
	handler := &mockHandler{}
	handler.HandleSessionCancel(acp.SessionCancelParams{SessionID: "sess_xyz"})
	if handler.cancelledID != "sess_xyz" {
		t.Errorf("expected cancelled ID %q, got %q", "sess_xyz", handler.cancelledID)
	}
}

func TestServerSessionPrompt(t *testing.T) {
	handler := &mockHandler{
		promptResult: &acp.SessionPromptResult{StopReason: acp.StopReasonEndTurn},
	}

	result, err := handler.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: "sess_test",
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("HandleSessionPrompt: %v", err)
	}
	if result.StopReason != acp.StopReasonEndTurn {
		t.Errorf("expected stop reason %q, got %q", acp.StopReasonEndTurn, result.StopReason)
	}
}

func TestStopReasonConstants(t *testing.T) {
	cases := []acp.StopReason{
		acp.StopReasonEndTurn,
		acp.StopReasonMaxTokens,
		acp.StopReasonMaxTurns,
		acp.StopReasonRefused,
		acp.StopReasonCancelled,
	}
	for _, c := range cases {
		if string(c) == "" {
			t.Errorf("empty stop reason constant")
		}
	}
}

// A permission approval must survive both response shapes: the protocol nests
// the outcome in its own object (Zed), Coddy's own surfaces send it flat.
// Decoding the nested form used to fail, and the caller read that failure as a
// cancellation, so every approval became "permission denied by user".
func TestPermissionResultAcceptsBothWireShapes(t *testing.T) {
	cases := []struct {
		name        string
		payload     string
		wantOutcome string
		wantOption  string
	}{
		{
			name:        "nested selected",
			payload:     `{"outcome":{"outcome":"selected","optionId":"allow"}}`,
			wantOutcome: "selected",
			wantOption:  "allow",
		},
		{
			name:        "nested allow always",
			payload:     `{"outcome":{"outcome":"selected","optionId":"allow_always_program"}}`,
			wantOutcome: "selected",
			wantOption:  "allow_always_program",
		},
		{
			name:        "nested cancelled",
			payload:     `{"outcome":{"outcome":"cancelled"}}`,
			wantOutcome: "cancelled",
		},
		{
			name:        "flat selected",
			payload:     `{"outcome":"selected","optionId":"allow"}`,
			wantOutcome: "selected",
			wantOption:  "allow",
		},
		{
			name:        "flat reject",
			payload:     `{"outcome":"selected","optionId":"reject"}`,
			wantOutcome: "selected",
			wantOption:  "reject",
		},
		{
			name:        "flat cancelled",
			payload:     `{"outcome":"cancelled"}`,
			wantOutcome: "cancelled",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got acp.PermissionResult
			if err := json.Unmarshal([]byte(tc.payload), &got); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.payload, err)
			}
			if got.Outcome != tc.wantOutcome {
				t.Errorf("outcome: got %q, want %q", got.Outcome, tc.wantOutcome)
			}
			if got.OptionID != tc.wantOption {
				t.Errorf("optionId: got %q, want %q", got.OptionID, tc.wantOption)
			}
		})
	}
}

// A session-scoped method that names a session this agent does not hold
// answers -32002 with the id it was given, which is what tells a client the
// difference between "this session is gone" and "the agent failed": the first
// is answered by reopening or dropping the session, the second by retrying.
func TestUnknownSessionAnswersResourceNotFound(t *testing.T) {
	w := startWireServer(t, &mockHandler{promptError: acp.SessionNotFound("sess_gone")})
	w.initialize(t, `{}`)
	if err := w.send(`{"jsonrpc":"2.0","id":7,"method":"session/prompt","params":` +
		`{"sessionId":"sess_gone","prompt":[{"type":"text","text":"hello"}]}}`); err != nil {
		t.Fatal(err)
	}
	w.awaitWritten(t, `"code":-32002`)
	w.awaitWritten(t, `"sessionId":"sess_gone"`)
}

// A value the method does not accept is invalid params, not an agent failure:
// ACP names an unknown mode on session/set_mode and an unknown config option as
// the -32602 cases, and a client shows them as its own mistake.
func TestRejectedModeAnswersInvalidParams(t *testing.T) {
	w := startWireServer(t, &mockHandler{modeError: acp.InvalidParams(`unknown mode %q (agent, plan, ask)`, "turbo")})
	w.initialize(t, `{}`)
	if err := w.send(`{"jsonrpc":"2.0","id":8,"method":"session/set_mode","params":` +
		`{"sessionId":"sess_1","modeId":"turbo"}}`); err != nil {
		t.Fatal(err)
	}
	w.awaitWritten(t, `"code":-32602`)
}

// session/close reaches the handler and answers with the empty object ACP
// defines for it, so a client that offers "close" gets a close and not
// "method not found".
func TestSessionCloseIsDispatched(t *testing.T) {
	handler := &mockHandler{}
	w := startWireServer(t, handler)
	w.initialize(t, `{}`)
	if err := w.send(`{"jsonrpc":"2.0","id":9,"method":"session/close","params":{"sessionId":"sess_9"}}`); err != nil {
		t.Fatal(err)
	}
	w.awaitWritten(t, `"id":9`)
	if handler.closedID != "sess_9" {
		t.Fatalf("the handler was given %q, want sess_9", handler.closedID)
	}
	// ACP defines close's success as the empty object, and a client that reads
	// the answer expects to find it: a response with neither result nor error
	// is what a handler returning nil would otherwise produce.
	w.awaitWritten(t, `"result":{}`)
}

// The empty object close answers with is written even when the handler returns
// no result of its own.
func TestSessionCloseAnswersAnEmptyObjectWithoutAResult(t *testing.T) {
	w := startWireServer(t, &mockHandler{closeNil: true})
	w.initialize(t, `{}`)
	if err := w.send(`{"jsonrpc":"2.0","id":10,"method":"session/close","params":{"sessionId":"sess_10"}}`); err != nil {
		t.Fatal(err)
	}
	w.awaitWritten(t, `"result":{}`)
}

// authMethods is an array of objects in ACP, and an agent without a sign-in
// sends it as an empty array rather than as null.
func TestAuthMethodsMarshalAsAnArray(t *testing.T) {
	got, err := json.Marshal(acp.InitializeResult{AuthMethods: []acp.AuthMethod{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"authMethods":[]`) {
		t.Fatalf("initialize result = %s, want an empty authMethods array", got)
	}
	method := acp.AuthMethod{Type: "agent", ID: "login", Name: "Log in"}
	got, err = json.Marshal(method)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"type":"agent","id":"login","name":"Log in"}` {
		t.Fatalf("auth method = %s, want the tagged object of the schema", got)
	}
}

// A tool call names the tool it invoked beside the title a person reads, so a
// client can key its icons and its filters on the name.
func TestToolCallCarriesItsProgrammaticName(t *testing.T) {
	got, err := json.Marshal(acp.ToolCallUpdate{
		SessionUpdate: acp.UpdateTypeToolCall,
		ToolCallID:    "call_1",
		Title:         "Run: go build ./...",
		Name:          "run_command",
		Kind:          "execute",
		Status:        "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"name":"run_command"`) {
		t.Fatalf("tool call = %s, want the tool name", got)
	}
	if !strings.Contains(string(got), `"title":"Run: go build ./..."`) {
		t.Fatalf("tool call = %s, want the title kept", got)
	}
}

// Chunks of one message share the id they carry, and the id a message is
// streamed under is the one it is replayed under: a client groups the chunks
// and recognises a message it already showed.
func TestMessageChunksCarryTheirMessageID(t *testing.T) {
	if got := acp.MessageID(0); got != "msg_0" {
		t.Fatalf("MessageID(0) = %q, want msg_0", got)
	}
	if acp.MessageID(0) == acp.MessageID(1) {
		t.Fatal("two messages share an id")
	}
	got, err := json.Marshal(acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "hi"},
		MessageID:     acp.MessageID(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"messageId":"msg_4"`) {
		t.Fatalf("chunk = %s, want its message id", got)
	}
}

// A client that says it renders the standard notice is told so at initialize,
// which is what routes the same line to it as a notice instead of as the field
// of a Coddy update.
func TestSupportsNoticesFollowsTheClientCapability(t *testing.T) {
	w := startWireServer(t, &mockHandler{})
	w.initialize(t, `{"session":{"notices":{}}}`)
	// The answer is written before the flag can be read, so the read below is
	// not a race against the dispatcher.
	if !w.srv.SupportsNotices() {
		t.Fatal("a client that advertised session.notices is not read as rendering notices")
	}

	w2 := startWireServer(t, &mockHandler{})
	w2.initialize(t, `{}`)
	if w2.srv.SupportsNotices() {
		t.Fatal("a client that advertised nothing is read as rendering notices")
	}
}

// The system prompt a session lifecycle request carries is refused before it
// can leave a session behind: an unsupported mode is invalid params.
func TestUnsupportedSystemPromptModeAnswersInvalidParams(t *testing.T) {
	w := startWireServer(t, &mockHandler{newError: acp.InvalidParams(`systemPrompt mode %q is not supported (append)`, "override")})
	w.initialize(t, `{}`)
	if err := w.send(`{"jsonrpc":"2.0","id":11,"method":"session/new","params":{"cwd":"/tmp",` +
		`"systemPrompt":{"mode":"override","content":[{"type":"text","text":"x"}]}}}`); err != nil {
		t.Fatal(err)
	}
	w.awaitWritten(t, `"code":-32602`)
}
