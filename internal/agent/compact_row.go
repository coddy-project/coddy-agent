package agent

// The transcript row a compaction draws while it runs.
//
// Compaction used to be invisible until it was over: the turn stopped, one
// summarization call went out, and the only thing a client saw was the summary
// row appearing afterwards. That was tolerable while it was one call. It is not
// once the fold takes several passes over a history that outgrew the window -
// the session sits silent for a minute with nothing to say why - and it is not
// once the model itself may ask for a compaction, because then the work has to
// be attributable in the transcript like any other tool call.
//
// So every compaction draws the same row a tool call draws: announced when it
// starts, updated with the pass it is on, closed with what it folded. A
// compaction the model asked for reuses the row the loop already announced for
// its compact_context call, so the work appears once, under the call that
// ordered it.

import (
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// compactionRow is the tool-call row one compaction draws.
type compactionRow struct {
	agent *Agent
	id    string
	// owned is false for the row the ReAct loop announced for a compact_context
	// call: the loop closes that one itself, so this row only reports progress.
	owned bool
}

// newCompactionRow announces a compaction in the transcript. When the
// compaction is the model's own compact_context call, the row it announced is
// reused instead of drawing a second one.
func (a *Agent) newCompactionRow() *compactionRow {
	if a == nil || a.server == nil {
		return &compactionRow{}
	}
	if id := strings.TrimSpace(a.currentToolCallID); id != "" {
		return &compactionRow{agent: a, id: id, owned: false}
	}
	row := &compactionRow{
		agent: a,
		// Not a model-issued call id, so it cannot collide with one.
		id:    fmt.Sprintf("compact_%d", time.Now().UTC().UnixNano()),
		owned: true,
	}
	row.persist("pending")
	_ = a.server.SendSessionUpdate(a.state.GetID(), acp.ToolCallUpdate{
		SessionUpdate: acp.UpdateTypeToolCall,
		ToolCallID:    row.id,
		Title:         tools.ToolCompactContext,
		Kind:          toolKind(tools.ToolCompactContext),
		Status:        "pending",
	})
	return row
}

// step reports the pass a multi-step fold is on. A compaction that takes one
// call says nothing: the row already says what is happening.
func (r *compactionRow) step(step, total int) {
	if r == nil || r.agent == nil || total <= 1 {
		return
	}
	r.status("in_progress", fmt.Sprintf("compacting context: pass %d of %d", step, total))
}

// done closes a row this compaction owns with what it folded.
func (r *compactionRow) done(text string) {
	if r == nil || r.agent == nil || !r.owned {
		return
	}
	r.persist("completed")
	r.status("completed", text)
}

// failed closes a row this compaction owns with the reason it stopped.
func (r *compactionRow) failed(err error) {
	if r == nil || r.agent == nil || !r.owned || err == nil {
		return
	}
	r.persist("failed")
	r.status("failed", fmt.Sprintf("error: %v", err))
}

func (r *compactionRow) status(status, text string) {
	var content []acp.ToolCallResultItem
	if strings.TrimSpace(text) != "" {
		content = []acp.ToolCallResultItem{
			{Type: "content", Content: acp.ContentBlock{Type: "text", Text: text}},
		}
	}
	_ = r.agent.server.SendSessionUpdate(r.agent.state.GetID(), acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    r.id,
		Status:        status,
		Content:       content,
	})
}

// persist writes the row's metadata beside the session, so a client that
// reloads the transcript after the compaction still finds it.
func (r *compactionRow) persist(status string) {
	st := sessionStatePtr(r.agent.state)
	if st == nil {
		return
	}
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	if sd == "" {
		return
	}
	_ = session.WriteToolCallMeta(sd, r.id, session.ToolCallMeta{
		ToolCallID: r.id,
		Name:       tools.ToolCompactContext,
		Kind:       toolKind(tools.ToolCompactContext),
		Status:     status,
	})
}
