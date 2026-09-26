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
// follows has a visible cause, and a compaction rebuild keeps it), and sent to
// the clients watching as the message itself, so it appears in the conversation
// where it was read.
//
// The shorter queue is not announced from here. The drain is a change like any
// other, and the session announces it through the notifier the manager
// installed, so every client of a shared session hears about it with the same
// payload and the same version - not only whoever is reading this turn.
func (a *Agent) readQueuedMessages(messages *[]llm.Message) bool {
	queued := a.state.TakeQueuedMessages()
	if len(queued) == 0 {
		return false
	}
	sessionID := a.state.GetID()
	for _, q := range queued {
		images := make([]llm.ImagePart, 0, len(q.ImageParts))
		for _, p := range q.ImageParts {
			images = append(images, llm.ImagePart{DataURL: p.DataURL, Name: p.Name})
		}
		if len(images) > 0 {
			if err := session.SavePartsToAssets(images, a.state.GetPersistedSessionDir()); err != nil {
				a.log.Warn("save queued files to assets", "error", err)
			}
		}
		content := a.resolveQueuedMessage(q.Text)
		if note := filePathsNote(images); note != "" {
			content += "\n\n" + note
		}
		// The follow-up's own mentions resolve now, as it enters the
		// conversation, and ride in its message (mentions.go).
		msg := llm.Message{
			Role:       llm.RoleUser,
			Content:    content,
			ImageParts: images,
			CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		}
		*messages = append(*messages, msg)
		// The frame goes out before the message is persisted, like every other
		// frame a message describes: a client that reloads between the two reads
		// the message in the transcript and is not replayed the frame on top of it.
		_ = a.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeUserMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: content},
		})
		a.state.AddMessage(msg)
	}
	a.log.Info("read queued messages", "session_id", sessionID, "messages", len(queued))
	a.refreshConversationContextUsage(true)
	return true
}
