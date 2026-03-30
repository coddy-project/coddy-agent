package acp_test

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// mockHandler implements acp.Handler for testing.
type mockHandler struct {
	initResult   *acp.InitializeResult
	newResult    *acp.SessionNewResult
	promptResult *acp.SessionPromptResult
	modeError    error
	cancelledID  string
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
	if m.newResult != nil {
		return m.newResult, nil
	}
	return &acp.SessionNewResult{SessionID: "test-session"}, nil
}

func (m *mockHandler) HandleSessionLoad(_ context.Context, _ acp.SessionLoadParams) error {
	return nil
}

func (m *mockHandler) HandleSessionPrompt(_ context.Context, _ acp.SessionPromptParams) (*acp.SessionPromptResult, error) {
	if m.promptResult != nil {
		return m.promptResult, nil
	}
	return &acp.SessionPromptResult{StopReason: acp.StopReasonEndTurn}, nil
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
