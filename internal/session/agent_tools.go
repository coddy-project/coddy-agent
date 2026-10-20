package session

// The tool calls the agent behind an acp model runs on its own side
// (llm.AgentToolCall) are drawn as tool rows like Coddy's own: live by the
// agent loop, from the transcript by a session/load replay and the remote
// console's replay, which both use what is here.

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// agentToolIDPrefix sets the rows of an agent's tool calls apart from the
// calls of Coddy's own, so an id of one never updates a row of the other.
const agentToolIDPrefix = "acp_"

// AgentToolID is the id of the tool row of an agent's call agentID.
func AgentToolID(agentID string) string { return agentToolIDPrefix + agentID }

// AgentToolKind maps the kind of an agent's tool (ACP: read, edit, delete,
// move, search, execute, think, fetch, other) onto the kinds Coddy's
// surfaces draw its own tool rows with (ToolKind).
func AgentToolKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "read", "search", "fetch":
		return "read"
	case "edit", "delete", "move":
		return "write"
	case "execute":
		return "run_command"
	default:
		return "other"
	}
}

// AgentToolMeta marks the row of an agent's call (_meta.coddy.agentTool):
// its title names what it works on, so a surface that reads a target out of a
// call's input (the web UI) does not name it twice.
func AgentToolMeta() map[string]interface{} {
	return map[string]interface{}{"coddy": map[string]interface{}{"agentTool": true}}
}

// AgentToolEnded reports whether a status is the last of an agent's call.
func AgentToolEnded(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

// AgentToolRowUpdates are the ACP updates that draw a stored agent call as
// it ended: its row, its input, and its outcome with the first lines of its
// output (the rest is the session's tool_calls/<id>/result.md).
func AgentToolRowUpdates(t llm.AgentToolCall) []interface{} {
	out := []interface{}{acp.ToolCallUpdate{
		SessionUpdate: acp.UpdateTypeToolCall,
		ToolCallID:    t.ID,
		Title:         t.Title,
		Kind:          AgentToolKind(t.Kind),
		Status:        "pending",
		Meta:          AgentToolMeta(),
	}}
	if t.Input != "" {
		out = append(out, acp.ToolCallStatusUpdate{
			SessionUpdate: acp.UpdateTypeToolCallUpdate,
			ToolCallID:    t.ID,
			Status:        "in_progress",
			Content:       agentToolText(t.Input),
		})
	}
	status := t.Status
	if !AgentToolEnded(status) {
		// Stored only once its turn ended: a call still open then was
		// closed with it.
		status = "completed"
	}
	end := acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    t.ID,
		Status:        status,
		Content:       agentToolText(t.Output),
	}
	if t.OutputLines > 0 {
		end.Meta = toolResultPreviewMeta(true, t.OutputLines, ToolHTTPUserPreviewContentLines)
	}
	return append(out, end)
}

func agentToolText(s string) []acp.ToolCallResultItem {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: s}}}
}

// StoredAgentTool is what the transcript keeps of an agent's call: the first
// lines of its output, and how many it had when it had more.
func StoredAgentTool(t llm.AgentToolCall) llm.AgentToolCall {
	preview, truncated, total := PreviewToolResultSnippet("", t.Output)
	t.Output = preview
	t.OutputLines = 0
	if truncated {
		t.OutputLines = total
	}
	return t
}
