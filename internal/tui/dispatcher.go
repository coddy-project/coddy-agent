package tui

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	tea "github.com/charmbracelet/bubbletea"
)

// AgentUpdateMsg is sent to the bubbletea program when the agent streams an update.
type AgentUpdateMsg struct {
	SessionID string
	Update    interface{}
}

// PermissionRequestMsg is sent to the bubbletea program when the agent needs user permission.
type PermissionRequestMsg struct {
	SessionID string
	Params    acp.PermissionRequestParams
	Response  chan<- *acp.PermissionResult
}

// AgentDoneMsg is sent when the agent turn finishes.
type AgentDoneMsg struct {
	SessionID  string
	StopReason string
	Err        error
}

// Dispatcher implements acp.UpdateSender by routing messages into the bubbletea program.
// The react agent calls Send/RequestPermission; the TUI receives them as tea.Msg values.
type Dispatcher struct {
	send func(tea.Msg)
}

// NewDispatcher creates a Dispatcher that forwards messages to the given program.
// send should be p.Send where p is a *tea.Program.
func NewDispatcher(send func(tea.Msg)) *Dispatcher {
	return &Dispatcher{send: send}
}

// SendSessionUpdate forwards an update notification to the TUI.
func (d *Dispatcher) SendSessionUpdate(sessionID string, update interface{}) error {
	d.send(AgentUpdateMsg{SessionID: sessionID, Update: update})
	return nil
}

// RequestPermission sends a permission request to the TUI and blocks until the
// user responds (or the context is cancelled).
func (d *Dispatcher) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	ch := make(chan *acp.PermissionResult, 1)
	d.send(PermissionRequestMsg{
		SessionID: params.SessionID,
		Params:    params,
		Response:  ch,
	})
	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		return &acp.PermissionResult{Outcome: "cancelled"}, nil
	}
}

// parseUpdateType extracts the sessionUpdate discriminator from an update value.
func parseUpdateType(update interface{}) string {
	switch v := update.(type) {
	case acp.MessageChunkUpdate:
		return v.SessionUpdate
	case acp.ToolCallUpdate:
		return v.SessionUpdate
	case acp.ToolCallStatusUpdate:
		return v.SessionUpdate
	case acp.ModeUpdate:
		return v.SessionUpdate
	case acp.ConfigOptionUpdate:
		return v.SessionUpdate
	case acp.PlanUpdate:
		return v.SessionUpdate
	case acp.TokenUsageUpdate:
		return v.SessionUpdate
	default:
		// Fallback: marshal and read discriminator field.
		data, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return ""
		}
		if su, ok := raw["sessionUpdate"]; ok {
			var s string
			if err := json.Unmarshal(su, &s); err == nil {
				return s
			}
		}
	}
	return ""
}

// extractAgentMessageChunk returns text from an agent_message_chunk and whether it is reasoning.
func extractAgentMessageChunk(update interface{}) (text string, isReasoning bool, ok bool) {
	u, ok2 := update.(acp.MessageChunkUpdate)
	if !ok2 || u.SessionUpdate != acp.UpdateTypeAgentMessageChunk {
		return "", false, false
	}
	if u.Content.Text == "" {
		return "", false, false
	}
	switch u.Content.Type {
	case acp.ContentTypeReasoning:
		return u.Content.Text, true, true
	case acp.ContentTypeText, "":
		return u.Content.Text, false, true
	default:
		return u.Content.Text, false, true
	}
}

// extractModeID returns the new mode from a current_mode_update.
func extractModeID(update interface{}) (string, bool) {
	if u, ok := update.(acp.ModeUpdate); ok {
		return u.ModeID, true
	}
	return "", false
}

// extractToolCall returns a tool call description from a tool_call update.
func extractToolCall(update interface{}) (id, title, status string, ok bool) {
	if u, ok2 := update.(acp.ToolCallUpdate); ok2 {
		return u.ToolCallID, u.Title, u.Status, true
	}
	return "", "", "", false
}

// extractToolCallStatus returns status info from a tool_call_update.
func extractToolCallStatus(update interface{}) (id, status string, ok bool) {
	if u, ok2 := update.(acp.ToolCallStatusUpdate); ok2 {
		return u.ToolCallID, u.Status, true
	}
	return "", "", false
}

// extractToolCallStatusFull returns id, status, and the first content text from a tool_call_update.
func extractToolCallStatusFull(update interface{}) (id, status, content string, ok bool) {
	if u, ok2 := update.(acp.ToolCallStatusUpdate); ok2 {
		text := ""
		if len(u.Content) > 0 {
			text = u.Content[0].Content.Text
		}
		return u.ToolCallID, u.Status, text, true
	}
	return "", "", "", false
}

// extractPlanUpdate returns plan entries from a PlanUpdate message.
func extractPlanUpdate(update interface{}) ([]acp.PlanEntry, bool) {
	if u, ok := update.(acp.PlanUpdate); ok {
		return u.Entries, true
	}
	return nil, false
}

// extractTokenUsage returns token counts from a TokenUsageUpdate message.
func extractTokenUsage(update interface{}) (input, output int, ok bool) {
	if u, ok2 := update.(acp.TokenUsageUpdate); ok2 {
		return u.InputTokens, u.OutputTokens, true
	}
	return 0, 0, false
}

// Ensure Dispatcher implements acp.UpdateSender at compile time.
var _ acp.UpdateSender = (*Dispatcher)(nil)

// Keep fmt import used.
var _ = fmt.Sprintf
