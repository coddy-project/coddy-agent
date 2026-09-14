package agent

import (
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// readQueuedMessages folds the follow-ups written during this turn into the
// conversation and reports whether there were any.
//
// A queued message becomes an ordinary user message: appended to the slice the
// next request is built from, added to the transcript (so the answer that
// follows has a visible cause, and a compaction rebuild keeps it), and
// published to the clients watching - as the message itself, so it appears in
// the conversation where it was read, and as the shorter queue, so the composer
// that is holding it stops showing it as pending.
func (a *Agent) readQueuedMessages(messages *[]llm.Message) bool {
	queued := a.state.TakeQueuedMessages()
	if len(queued) == 0 {
		return false
	}
	sessionID := a.state.GetID()
	for _, q := range queued {
		msg := llm.Message{
			Role:      llm.RoleUser,
			Content:   q.Text,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		*messages = append(*messages, msg)
		a.state.AddMessage(msg)
		_ = a.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeUserMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: q.Text},
		})
	}
	a.log.Info("read queued messages", "session_id", sessionID, "messages", len(queued))
	_ = a.server.SendSessionUpdate(sessionID, acp.MessageQueueUpdate{
		SessionUpdate: acp.UpdateTypeMessageQueue,
		Messages:      session.QueuedMessagesWire(a.state.QueuedMessages()),
	})
	a.refreshConversationContextUsage(true)
	return true
}
