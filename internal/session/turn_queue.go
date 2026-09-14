package session

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// MaxQueuedMessages bounds how many follow-ups one session may hold at once.
//
// The queue is a composer affordance, not a batch runner: an operator writes a
// correction or two while watching the work, and a cap keeps a stuck client (or
// a script pointed at the endpoint) from growing an unbounded slice that the
// next step would paste into the model's context in one go.
const MaxQueuedMessages = 20

var (
	// ErrNoActiveTurn is returned when a follow-up is queued on a session that
	// is not running a turn. There is nothing to inject it into, and holding it
	// for a turn that may start hours later would answer the operator's words
	// in a conversation they no longer belong to: the caller sends it as an
	// ordinary prompt instead.
	ErrNoActiveTurn = errors.New("no agent turn is running for this session")

	// ErrQueueFull is returned when the session already holds MaxQueuedMessages.
	ErrQueueFull = fmt.Errorf("the message queue is full (%d messages)", MaxQueuedMessages)

	// ErrQueuedMessageNotFound is returned when a cancel names a message the
	// queue no longer holds - usually because the agent has just read it.
	ErrQueuedMessageNotFound = errors.New("queued message not found")
)

// QueuedMessage is one follow-up written while a turn was running, waiting for
// the agent to read it at its next step.
type QueuedMessage struct {
	// ID identifies the message for the cancel path; unique within the process.
	ID string `json:"id"`
	// Text is what the operator wrote, verbatim.
	Text string `json:"text"`
	// CreatedAt is when it was queued (RFC3339 UTC).
	CreatedAt string `json:"createdAt"`
}

// queuedMessageSeq numbers queued messages for their identifiers. Process-wide
// rather than per session, so an id is never ambiguous in a log line.
var queuedMessageSeq atomic.Uint64

// Wire converts the message to the shape published over ACP and HTTP.
func (q QueuedMessage) Wire() acp.QueuedMessage {
	return acp.QueuedMessage{ID: q.ID, Text: q.Text, CreatedAt: q.CreatedAt}
}

// QueuedMessagesWire converts a queue snapshot for the wire.
func QueuedMessagesWire(msgs []QueuedMessage) []acp.QueuedMessage {
	out := make([]acp.QueuedMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Wire())
	}
	return out
}

// bumpQueueLocked stamps the queue with the next version. Callers hold queueMu.
//
// The same change reaches a client down more than one pipe - the turn's own
// stream and the server-wide event stream - and those are different
// connections, so the frames can arrive out of order. The version is what lets
// a client drop the older of two answers instead of rendering it.
func (s *State) bumpQueueLocked() uint64 {
	s.queueVersion++
	return s.queueVersion
}

// QueueVersion is the version of the queue as it now stands.
func (s *State) QueueVersion() uint64 {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return s.queueVersion
}

// OpenMessageQueue accepts follow-ups for the turn that is starting.
//
// The queue is turn-scoped: it opens when a turn is admitted and closes when
// that turn releases, so a message can only ever be read by the turn it was
// written during. Anything left from a previous turn is dropped here rather
// than inherited.
func (s *State) OpenMessageQueue() {
	s.queueMu.Lock()
	s.queue = nil
	s.queueOpen = true
	s.bumpQueueLocked()
	s.queueMu.Unlock()
}

// CloseMessageQueue refuses further follow-ups and returns whatever was still
// waiting. It is idempotent, so a turn that both defers it and calls it
// explicitly closes the queue once.
func (s *State) CloseMessageQueue() []QueuedMessage {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	left := s.queue
	s.queue = nil
	s.queueOpen = false
	s.bumpQueueLocked()
	return left
}

// MessageQueueOpen reports whether the session is running a turn that would
// read a follow-up.
func (s *State) MessageQueueOpen() bool {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return s.queueOpen
}

// EnqueueMessage adds a follow-up for the running turn to read at its next step.
func (s *State) EnqueueMessage(text string) (QueuedMessage, error) {
	body := strings.TrimSpace(text)
	if body == "" {
		return QueuedMessage{}, fmt.Errorf("queued message is empty")
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if !s.queueOpen {
		return QueuedMessage{}, ErrNoActiveTurn
	}
	if len(s.queue) >= MaxQueuedMessages {
		return QueuedMessage{}, ErrQueueFull
	}
	msg := QueuedMessage{
		ID:        fmt.Sprintf("q_%d", queuedMessageSeq.Add(1)),
		Text:      body,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.queue = append(s.queue, msg)
	s.bumpQueueLocked()
	return msg, nil
}

// QueuedMessages returns a copy of what is waiting.
func (s *State) QueuedMessages() []QueuedMessage {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return append([]QueuedMessage(nil), s.queue...)
}

// CancelQueuedMessage removes a message the agent has not read yet and reports
// whether it was still there.
func (s *State) CancelQueuedMessage(id string) bool {
	want := strings.TrimSpace(id)
	if want == "" {
		return false
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	for i, m := range s.queue {
		if m.ID != want {
			continue
		}
		s.queue = append(s.queue[:i:i], s.queue[i+1:]...)
		s.bumpQueueLocked()
		return true
	}
	return false
}

// ClearQueuedMessages drops everything waiting and returns what was dropped.
func (s *State) ClearQueuedMessages() []QueuedMessage {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	dropped := s.queue
	s.queue = nil
	s.bumpQueueLocked()
	return dropped
}

// TakeQueuedMessages drains the queue for the step that is about to run and
// leaves it open for the next one. This is the mid-turn read: it is what makes
// a follow-up land between the tool calls the model just made and the request
// that follows them.
func (s *State) TakeQueuedMessages() []QueuedMessage {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	taken := s.queue
	s.queue = nil
	s.bumpQueueLocked()
	return taken
}

// TakeQueuedMessagesOrClose is the turn boundary, and it is one atomic step on
// purpose: either it hands back a non-empty batch (and the queue stays open for
// the follow-up run), or it finds nothing and closes the queue in the same
// critical section.
//
// Draining and closing as two operations would leave a window between them in
// which a message is accepted by a turn that is already over - the one way a
// queued message could be silently lost.
func (s *State) TakeQueuedMessagesOrClose() ([]QueuedMessage, bool) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if len(s.queue) == 0 {
		s.queueOpen = false
		s.bumpQueueLocked()
		return nil, false
	}
	taken := s.queue
	s.queue = nil
	s.bumpQueueLocked()
	return taken, true
}

// QueuedPromptBlocks renders a batch of queued messages as the prompt of a run.
//
// Several follow-ups written while one step ran are one prompt, not several
// turns: they were typed against the same state of the work and read together,
// so joining them keeps the order the operator wrote them in without spending a
// round trip per line.
func QueuedPromptBlocks(msgs []QueuedMessage) []acp.ContentBlock {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(m.Text)
	}
	return []acp.ContentBlock{{Type: acp.ContentTypeText, Text: b.String()}}
}
