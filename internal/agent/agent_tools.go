package agent

// The tool calls the agent behind an acp model runs on its own side reach
// the loop as llm.StreamChunk.AgentTool. Each is drawn as a tool row like
// Coddy's own - announced, its input, its outcome with the first lines of its
// output - its whole output is written where a surface reads a full result
// (tool_calls/<id>/result.md, what "More" and the console's expanded row
// read), and the assistant message keeps a preview of each
// (llm.Message.AgentTools), from which a reload and a session/load replay
// draw the rows again. None of it is a tool call of Coddy's: nothing runs
// it, and no model is sent it.

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// agentToolRows follows the agent's tool calls of one provider call.
type agentToolRows struct {
	server    acp.UpdateSender
	sessionID string
	// dir is the session's folder; empty when the session is not stored.
	dir   string
	order []string
	rows  map[string]*agentToolRow
}

type agentToolRow struct {
	call      llm.AgentToolCall
	inputSent string
	ended     bool
}

func (a *Agent) newAgentToolRows(sessionID string) *agentToolRows {
	r := &agentToolRows{server: a.server, sessionID: sessionID, rows: map[string]*agentToolRow{}}
	if st := sessionStatePtr(a.state); st != nil {
		r.dir = strings.TrimSpace(st.GetPersistedSessionDir())
	}
	return r
}

// report draws a call as the agent last reported it.
func (r *agentToolRows) report(c llm.AgentToolCall) {
	if strings.TrimSpace(c.ID) == "" {
		return
	}
	c.ID = session.AgentToolID(c.ID)
	row := r.rows[c.ID]
	if row == nil {
		row = &agentToolRow{}
		r.rows[c.ID] = row
		r.order = append(r.order, c.ID)
		r.send(acp.ToolCallUpdate{
			SessionUpdate: acp.UpdateTypeToolCall,
			ToolCallID:    c.ID,
			Title:         c.Title,
			Kind:          session.AgentToolKind(c.Kind),
			Status:        "pending",
			Meta:          session.AgentToolMeta(),
		})
		if r.dir != "" {
			_ = session.MarkToolCallStarted(r.dir, c.ID, c.Title, session.AgentToolKind(c.Kind), "pending")
		}
	}
	if row.ended {
		return
	}
	row.call = c
	if c.Input != "" && c.Input != row.inputSent {
		row.inputSent = c.Input
		r.send(acp.ToolCallStatusUpdate{
			SessionUpdate: acp.UpdateTypeToolCallUpdate,
			ToolCallID:    c.ID,
			Status:        "in_progress",
			Content:       agentToolContent(c.Input),
		})
		if r.dir != "" {
			_ = session.WriteToolCallArgs(r.dir, c.ID, c.Input)
		}
	}
	if session.AgentToolEnded(c.Status) {
		r.end(row)
	}
}

// close ends the calls the stream left open with status: completed when the
// provider call returned, cancelled when it was stopped or failed. A call
// cut off that way would otherwise spin as running forever.
func (r *agentToolRows) close(status string) {
	for _, id := range r.order {
		row := r.rows[id]
		if !row.ended {
			row.call.Status = status
			r.end(row)
		}
	}
}

// end draws the outcome of a call and writes its whole output.
func (r *agentToolRows) end(row *agentToolRow) {
	row.ended = true
	c := row.call
	display, meta := session.PreviewToolResultForSessionUpdate("", c.Output)
	r.send(acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    c.ID,
		Status:        c.Status,
		Content:       agentToolContent(display),
		Meta:          meta,
	})
	if r.dir != "" {
		if c.Output != "" {
			_ = session.WriteToolCallResult(r.dir, c.ID, c.Output)
		}
		_ = session.MarkToolCallFinished(r.dir, c.ID, c.Title, session.AgentToolKind(c.Kind), c.Status)
	}
}

// stored is what the assistant message keeps of the calls, in the order the
// agent announced them.
func (r *agentToolRows) stored() []llm.AgentToolCall {
	if len(r.order) == 0 {
		return nil
	}
	out := make([]llm.AgentToolCall, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, session.StoredAgentTool(r.rows[id].call))
	}
	return out
}

func (r *agentToolRows) send(u interface{}) {
	if r.server != nil {
		_ = r.server.SendSessionUpdate(r.sessionID, u)
	}
}

func agentToolContent(s string) []acp.ToolCallResultItem {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: s}}}
}

// agentToolsOfStep is what the transcript keeps of the agent's calls of the
// provider call under way; nil when there were none.
func (a *Agent) agentToolsOfStep() []llm.AgentToolCall {
	if a.stepAgentTools == nil {
		return nil
	}
	return a.stepAgentTools.stored()
}
