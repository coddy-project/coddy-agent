//go:build gateway || gateway.pachca

package pachca

// The turn's output in the chat: one message, posted with the first text or
// tool, edited in place while the answer is written (edits are outside
// Pachca's send limit), and replaced by the rendered answer at the end. An
// answer longer than one message continues in further messages.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	// editInterval is the shortest pause between two edits of the live message.
	editInterval = 2 * time.Second
	// previewRunes is how much of the tail the live message shows.
	previewRunes = 1500
	// messageRunes is the budget of one message. Pachca documents no limit;
	// a refusal as too long splits the piece further.
	messageRunes = 8000
	// previewTimeout bounds one live edit: a slow API delays the preview,
	// never the agent.
	previewTimeout = 5 * time.Second
	// flushTimeout bounds the delivery of the final answer.
	flushTimeout = 2 * time.Minute
)

// Sender implements acp.UpdateSender for one turn in one chat.
type Sender struct {
	b      *Bot
	ctx    context.Context
	c      *Client
	target Target
	// parent is the message the first post replies to; zero after it.
	parent int64

	// refuseApprovals is set for the turn of somebody who is not the bot's
	// admin: every permission request is refused, the agent's own included.
	refuseApprovals bool

	mu          sync.Mutex
	responseBuf strings.Builder
	currentTool string
	liveID      int64
	liveText    string
	lastEdit    time.Time
}

func (b *Bot) newSender(ctx context.Context, c *Client, target Target, parent int64) *Sender {
	return &Sender{b: b, ctx: ctx, c: c, target: target, parent: parent}
}

// SendSessionUpdate handles the agent's streaming events.
func (s *Sender) SendSessionUpdate(_ string, update interface{}) error {
	switch u := update.(type) {
	case acp.BackgroundWakeUpdate:
		// A turn nobody typed opens with what woke the agent.
		s.post("🔔 " + session.BackgroundWakeNote(u))
	case acp.MessageChunkUpdate:
		if u.Content.Type != acp.ContentTypeText || u.SessionUpdate == acp.UpdateTypeUserMessageChunk {
			return nil
		}
		s.mu.Lock()
		s.responseBuf.WriteString(u.Content.Text)
		s.currentTool = ""
		want := s.dueLocked()
		s.mu.Unlock()
		if want {
			s.stream()
		}
	case acp.ToolCallUpdate:
		title := u.Title
		if title == "" {
			title = u.Kind
		}
		if title == "" {
			return nil
		}
		s.mu.Lock()
		s.currentTool = title
		want := s.dueLocked()
		s.mu.Unlock()
		if want {
			s.stream()
		}
	}
	return nil
}

// dueLocked reports whether the live message may be updated now. Caller holds s.mu.
func (s *Sender) dueLocked() bool {
	now := time.Now()
	if now.Sub(s.lastEdit) < editInterval {
		return false
	}
	s.lastEdit = now
	return true
}

func (s *Sender) previewLocked() string {
	text := plainPreview(s.responseBuf.String(), previewRunes)
	if s.currentTool != "" {
		line := "⚙️ " + s.currentTool + "…"
		if text == "" {
			return line
		}
		return text + "\n\n" + line
	}
	return text
}

// stream posts or edits the live message.
func (s *Sender) stream() {
	s.mu.Lock()
	text := s.previewLocked()
	liveID := s.liveID
	unchanged := text == s.liveText
	s.mu.Unlock()
	if strings.TrimSpace(text) == "" || unchanged {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, previewTimeout)
	defer cancel()
	if liveID == 0 {
		m, err := s.c.SendMessage(ctx, OutgoingMessage{Target: s.target, Content: text, ParentID: s.takeParent()})
		if err != nil {
			s.b.log.Debug("pachca: live message", "err", err)
			return
		}
		s.b.posted.add(m.ID)
		s.mu.Lock()
		s.liveID, s.liveText = m.ID, text
		s.mu.Unlock()
		return
	}
	if err := s.c.EditMessage(ctx, liveID, text, nil); err != nil {
		// A refused preview edit is skipped; the next one carries more.
		s.b.log.Debug("pachca: live edit", "err", err)
		return
	}
	s.mu.Lock()
	s.liveText = text
	s.mu.Unlock()
}

func (s *Sender) takeParent() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.parent
	s.parent = 0
	return p
}

// post sends a message of its own, outside the live one.
func (s *Sender) post(text string) {
	m, err := s.c.SendMessage(s.ctx, OutgoingMessage{Target: s.target, Content: text, ParentID: s.takeParent()})
	if err != nil {
		s.b.log.Warn("pachca: message not delivered", "err", err, "target", s.target.EntityID)
		return
	}
	s.b.posted.add(m.ID)
}

// Flush replaces the live message with the rendered answer.
func (s *Sender) Flush() {
	s.mu.Lock()
	text := s.responseBuf.String()
	s.responseBuf.Reset()
	s.currentTool = ""
	liveID := s.liveID
	s.liveID, s.liveText = 0, ""
	s.mu.Unlock()

	// The answer is delivered even when the turn's own context ended - a
	// turn cut off by its timeout still owes the chat what it wrote.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), flushTimeout)
	defer cancel()
	if strings.TrimSpace(text) == "" {
		if liveID != 0 {
			// Only tools ran: the indicator would stay as the last word.
			if err := s.c.EditMessage(ctx, liveID, "Done.", nil); err != nil {
				s.b.log.Debug("pachca: settle empty live message", "err", err)
			}
		}
		return
	}
	rendered := renderMarkdown(text)
	for i, chunk := range splitMessage(rendered, messageRunes) {
		editID := int64(0)
		if i == 0 {
			editID = liveID
		}
		s.deliver(ctx, chunk, editID)
	}
}

// deliver puts one piece of the answer into the chat: into editID when it is
// set, else as a new message. A piece refused as too long is split again,
// down to the smallest piece size.
func (s *Sender) deliver(ctx context.Context, text string, editID int64) {
	var err error
	if editID != 0 {
		err = s.c.EditMessage(ctx, editID, text, nil)
	} else {
		var m *Message
		m, err = s.c.SendMessage(ctx, OutgoingMessage{Target: s.target, Content: text, ParentID: s.takeParent()})
		if err == nil {
			s.b.posted.add(m.ID)
		}
	}
	if err == nil {
		return
	}
	if IsTooLong(err) {
		n := len([]rune(text))
		if n > minChunkRunes {
			parts := splitMessage(text, n/2)
			if len(parts) > 1 {
				for i, p := range parts {
					id := int64(0)
					if i == 0 {
						id = editID
					}
					s.deliver(ctx, p, id)
				}
				return
			}
		}
	}
	if IsRateLimited(err) {
		s.b.log.Warn("pachca: answer refused by the rate limit", "err", err, "target", s.target.EntityID)
		return
	}
	s.b.log.Warn("pachca: answer not delivered", "err", err, "target", s.target.EntityID)
}

// RequestPermission allows what the chat's own agent asks: the operator who
// configured the bot decided that. A subagent stamped below bypass is asked
// about in the chat, with buttons, and the click decides (permission.go).
func (s *Sender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if s.refuseApprovals {
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
	stamped := strings.TrimSpace(params.EffectivePermissionMode)
	if stamped == "" || stamped == "bypass" {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	res, err := s.b.permissions().ask(ctx, s.b, s.c, s.target, params.SessionID, params)
	if err != nil {
		s.b.log.Warn("pachca: permission request not delivered", "err", err, "target", s.target.EntityID)
	}
	if err != nil || res == nil {
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
	return res, nil
}

// RequestQuestion posts the question and returns an empty answer: a chat has
// no form for it, the person answers with their next message.
func (s *Sender) RequestQuestion(_ context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	if len(params.Questions) > 0 {
		s.post(params.Questions[0].Question)
	}
	return &acp.QuestionResult{}, nil
}
