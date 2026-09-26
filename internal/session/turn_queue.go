package session

import (
	"context"
	"encoding/base64"
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
	// ErrTurnScopedFollowUp refuses a follow-up that starts with a settings
	// command for a number of turns (--once, --count=N): a queued message can
	// change mode or be cancelled before delivery, so no turn can be selected.
	ErrTurnScopedFollowUp = errors.New("a --once or --count command with a queued message has no stable target turn: send the message after the turn, or send the command on its own")

	// ErrQueueFull is returned when the session already holds MaxQueuedMessages.
	ErrQueueFull = fmt.Errorf("the message queue is full (%d messages)", MaxQueuedMessages)

	// ErrQueuedMessageNotFound is returned when a cancel names a message the
	// queue no longer holds - usually because the agent has just read it.
	ErrQueuedMessageNotFound = errors.New("queued message not found")
)

// QueuedMessage is a follow-up written while a turn was running. Its mode
// decides whether the current turn reads it or a later run starts it.
type QueuedMessage struct {
	// ID identifies the message for the cancel path; unique within the process.
	ID string `json:"id"`
	// Text is what the operator wrote, verbatim.
	Text       string             `json:"text"`
	Mode       QueueMode          `json:"mode"`
	ImageParts []acp.ImagePartRef `json:"imageParts,omitempty"`
	// CreatedAt is when it was queued (RFC3339 UTC).
	CreatedAt string `json:"createdAt"`
}

type QueueMode string

const (
	QueueModeSteer     QueueMode = "steer"
	QueueModeAfterTurn QueueMode = "after_turn"
)

func ValidQueueMode(mode QueueMode) bool {
	return mode == QueueModeSteer || mode == QueueModeAfterTurn
}

// queuedMessageSeq numbers queued messages for their identifiers. Process-wide
// rather than per session, so an id is never ambiguous in a log line.
var queuedMessageSeq atomic.Uint64

// queueVersionSeq numbers queue changes.
//
// Process-wide rather than per session on purpose. A client keeps the highest
// version it has applied for a session; a counter that started again from zero
// whenever the session's live state was rebuilt (evicted and loaded again)
// would publish versions below what that client had already seen, and every
// frame of the new turn would be dropped as stale.
var queueVersionSeq atomic.Uint64

// Wire converts the message to the shape published over ACP and HTTP. Its
// images are described, not carried (acp.QueuedImage): the same list goes to
// every client on every change of the queue.
func (q QueuedMessage) Wire() acp.QueuedMessage {
	out := acp.QueuedMessage{ID: q.ID, Text: q.Text, Mode: string(q.Mode), CreatedAt: q.CreatedAt}
	for _, p := range q.ImageParts {
		out.ImageParts = append(out.ImageParts, queuedImage(p))
	}
	return out
}

// queuedImage describes an image part without its bytes: the type from the
// data URI's header, the size its base64 payload decodes to.
func queuedImage(p acp.ImagePartRef) acp.QueuedImage {
	img := acp.QueuedImage{Name: p.Name}
	header, payload, ok := strings.Cut(p.DataURL, ",")
	if !ok || !strings.HasPrefix(strings.ToLower(header), "data:") {
		return img
	}
	img.MimeType = strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(header), "data:"), ";base64")
	if strings.HasSuffix(strings.ToLower(header), ";base64") {
		img.SizeBytes = base64.StdEncoding.DecodedLen(len(payload)) - strings.Count(payload[max(0, len(payload)-2):], "=")
	}
	return img
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
	s.queueVersion = queueVersionSeq.Add(1)
	return s.queueVersion
}

// SetQueueNotifier registers what to run after every change of this session's
// queue. The manager installs its publisher here, so a change announces itself
// from one place - whoever made it, and whether it came from a request, a
// console keystroke or the turn's own drain.
func (s *State) SetQueueNotifier(fn func()) {
	s.queueMu.Lock()
	s.queueNotify = fn
	s.queueMu.Unlock()
}

// SetQueuedMentionResolver registers how a follow-up read from the queue has
// its "@" references resolved. The manager installs its resolver when a turn
// opens the queue, so a message written mid-turn attaches what it names the
// same way the prompt that started the turn did.
func (s *State) SetQueuedMentionResolver(fn func([]acp.ContentBlock) []acp.ContentBlock) {
	s.queueMu.Lock()
	s.queueMentions = fn
	s.queueMu.Unlock()
}

// ResolveQueuedMentions resolves the references of a follow-up with the
// resolver of the running turn; without one the blocks come back as they are.
func (s *State) ResolveQueuedMentions(blocks []acp.ContentBlock) []acp.ContentBlock {
	s.queueMu.Lock()
	fn := s.queueMentions
	s.queueMu.Unlock()
	if fn == nil {
		return blocks
	}
	return fn(blocks)
}

// notifyQueue runs the registered notifier. It is called with queueMu released,
// because the notifier reads the queue back through QueueSnapshot.
func (s *State) notifyQueue() {
	s.queueMu.Lock()
	fn := s.queueNotify
	s.queueMu.Unlock()
	if fn != nil {
		fn()
	}
}

// QueueVersion is the version of the queue as it now stands.
func (s *State) QueueVersion() uint64 {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return s.queueVersion
}

// OpenMessageQueue accepts follow-ups for the turn that is starting.
//
// A new turn accepts follow-ups. Unread steer messages from an earlier turn
// are dropped; after_turn messages retained by Stop remain waiting.
func (s *State) OpenMessageQueue() {
	s.queueMu.Lock()
	// Reopening an already-open queue drops old steer work. A closed queue can
	// hold a message retained by Stop whose mode was changed while idle; keep
	// that edit for the next turn.
	if s.queueOpen {
		s.queue = keepAfterTurn(s.queue)
	}
	s.queueOpen = true
	s.bumpQueueLocked()
	s.queueMu.Unlock()
	s.notifyQueue()
}

// CloseMessageQueue refuses follow-ups and returns unread steer messages.
// After-turn messages remain queued, but do not auto-start after Stop.
//
// It is idempotent, and a close that finds the queue already closed and empty
// changes nothing: the boundary drain closes it first on the ordinary path, and
// the turn's own release must not then spend a version and a frame on saying so
// a second time.
func (s *State) CloseMessageQueue() []QueuedMessage {
	s.queueMu.Lock()
	left := takeSteer(s.queue)
	changed := s.queueOpen || len(left) > 0
	s.queue = keepAfterTurn(s.queue)
	s.queueOpen = false
	if changed {
		s.bumpQueueLocked()
	}
	s.queueMu.Unlock()
	if changed {
		s.notifyQueue()
	}
	return left
}

// MessageQueueOpen reports whether the session is running a turn that would
// read a follow-up.
func (s *State) MessageQueueOpen() bool {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return s.queueOpen
}

// EnqueueMessage adds a steer follow-up for the running turn.
func (s *State) EnqueueMessage(text string) (msg QueuedMessage, err error) {
	return s.EnqueueMessageWithMode(text, QueueModeSteer, nil)
}

func (s *State) EnqueueMessageWithMode(text string, mode QueueMode, parts []acp.ImagePartRef) (msg QueuedMessage, err error) {
	body := strings.TrimSpace(text)
	if !ValidQueueMode(mode) {
		return QueuedMessage{}, fmt.Errorf("invalid queue mode %q", mode)
	}
	if body == "" && len(parts) == 0 {
		return QueuedMessage{}, fmt.Errorf("queued message is empty")
	}
	s.queueMu.Lock()
	queued := false
	defer func() {
		s.queueMu.Unlock()
		if queued {
			s.notifyQueue()
		}
	}()
	if !s.queueOpen {
		return QueuedMessage{}, ErrNoActiveTurn
	}
	if len(s.queue) >= MaxQueuedMessages {
		return QueuedMessage{}, ErrQueueFull
	}
	msg = QueuedMessage{
		ID:         fmt.Sprintf("q_%d", queuedMessageSeq.Add(1)),
		Text:       body,
		Mode:       mode,
		ImageParts: append([]acp.ImagePartRef(nil), parts...),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	s.queue = append(s.queue, msg)
	s.bumpQueueLocked()
	queued = true
	return msg, nil
}

// QueuedMessages returns a copy of what is waiting.
func (s *State) QueuedMessages() []QueuedMessage {
	msgs, _ := s.QueueSnapshot()
	return msgs
}

// QueueSnapshot returns what is waiting together with the version that names
// exactly that content, read under one lock.
//
// Reading the two separately is not equivalent: a drain landing between the
// reads would pair a stale list with a newer version, and a client that keeps
// the highest version it has seen would then accept the stale list and refuse
// the correction that followed it.
func (s *State) QueueSnapshot() ([]QueuedMessage, uint64) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	out := append([]QueuedMessage(nil), s.queue...)
	for i := range out {
		out[i].ImageParts = append([]acp.ImagePartRef(nil), out[i].ImageParts...)
	}
	return out, s.queueVersion
}

// CancelQueuedMessage removes a message the agent has not read yet and reports
// whether it was still there.
func (s *State) CancelQueuedMessage(id string) bool {
	_, found := s.TakeBackQueuedMessage(id)
	return found
}

// TakeBackQueuedMessage removes a message the agent has not read yet and hands
// it back whole, images included, so the surface that took it back can put it
// into its draft exactly as it was written.
func (s *State) TakeBackQueuedMessage(id string) (QueuedMessage, bool) {
	want := strings.TrimSpace(id)
	if want == "" {
		return QueuedMessage{}, false
	}
	s.queueMu.Lock()
	var taken QueuedMessage
	found := false
	for i, m := range s.queue {
		if m.ID != want {
			continue
		}
		taken = m
		s.queue = append(s.queue[:i:i], s.queue[i+1:]...)
		s.bumpQueueLocked()
		found = true
		break
	}
	s.queueMu.Unlock()
	if found {
		s.notifyQueue()
	}
	return taken, found
}

// SetQueuedMessageMode changes the destination of a message still waiting.
func (s *State) SetQueuedMessageMode(id string, mode QueueMode) bool {
	if !ValidQueueMode(mode) {
		return false
	}
	s.queueMu.Lock()
	changed := false
	for i := range s.queue {
		if s.queue[i].ID == id {
			s.queue[i].Mode = mode
			s.bumpQueueLocked()
			changed = true
			break
		}
	}
	s.queueMu.Unlock()
	if changed {
		s.notifyQueue()
	}
	return changed
}

// ClearQueuedMessages drops everything waiting and returns what was dropped.
func (s *State) ClearQueuedMessages() []QueuedMessage {
	s.queueMu.Lock()
	dropped := s.queue
	s.queue = nil
	s.bumpQueueLocked()
	s.queueMu.Unlock()
	if len(dropped) > 0 {
		s.notifyQueue()
	}
	return dropped
}

// TakeQueuedMessages drains the queue for the step that is about to run and
// leaves it open for the next one. This is the mid-turn read: it is what makes
// a follow-up land between the tool calls the model just made and the request
// that follows them.
func (s *State) TakeQueuedMessages() []QueuedMessage {
	s.queueMu.Lock()
	taken := takeSteer(s.queue)
	s.queue = keepAfterTurn(s.queue)
	s.bumpQueueLocked()
	s.queueMu.Unlock()
	// The read is a change like any other: the composer holding those cards
	// has to stop showing them the moment the agent takes them.
	if len(taken) > 0 {
		s.notifyQueue()
	}
	return taken
}

// TakeQueuedMessagesOrClose is the turn boundary, and it is one atomic step on
// purpose. It hands back, in this order of preference:
//   - every steer message still waiting, as one batch: they were written for
//     the turn that is ending, so one more run of it answers them together;
//   - otherwise the oldest after_turn message on its own: a deferred prompt
//     gets a run of its own, one at a time, in the order they were queued;
//
// and the queue stays open for the run that follows. Finding nothing, it
// closes the queue in the same critical section.
//
// Draining and closing as two operations would leave a window between them in
// which a message is accepted by a turn that is already over - the one way a
// queued message could be silently lost. Choosing between the two kinds under
// the same lock means a row switched from one mode to the other a moment ago
// is taken as what it is now, never skipped.
func (s *State) TakeQueuedMessagesOrClose() ([]QueuedMessage, bool) {
	s.queueMu.Lock()
	taken := takeSteer(s.queue)
	switch {
	case len(taken) > 0:
		s.queue = keepAfterTurn(s.queue)
	case len(s.queue) > 0:
		// Only after_turn rows are left, oldest first.
		taken = []QueuedMessage{s.queue[0]}
		s.queue = append([]QueuedMessage(nil), s.queue[1:]...)
	default:
		s.queueOpen = false
		s.bumpQueueLocked()
		s.queueMu.Unlock()
		return nil, false
	}
	s.bumpQueueLocked()
	s.queueMu.Unlock()
	s.notifyQueue()
	return taken, true
}

// ReturnQueuedMessages puts messages the boundary took back at the head of the
// queue, in their order and with their modes. It is for a run that was
// stopped before its prompt entered the conversation: nothing read those
// messages, and a Stop keeps what the turn never read for later rather than
// losing it. The queue may already be closed; a retained message waits there
// the same way.
func (s *State) ReturnQueuedMessages(msgs []QueuedMessage) {
	if len(msgs) == 0 {
		return
	}
	s.queueMu.Lock()
	s.queue = append(append([]QueuedMessage(nil), msgs...), s.queue...)
	s.bumpQueueLocked()
	s.queueMu.Unlock()
	s.notifyQueue()
}

// promptEchoKey marks the context of a run the turn boundary started from
// queued messages (withPromptEcho).
type promptEchoKey struct{}

// withPromptEcho marks ctx as the run of a queued prompt on sessionID.
func withPromptEcho(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, promptEchoKey{}, sessionID)
}

// PromptEcho reports whether the run on ctx for sessionID must announce its
// prompt to the clients as the operator's message. An ordinary prompt is shown
// by the surface that sends it the moment it is sent; a queued one that the
// turn boundary starts was typed into no client's view of that run, so the
// runner announces it before answering it. The session id keeps the mark from
// reaching the run of a subagent, whose context derives from this one.
func PromptEcho(ctx context.Context, sessionID string) bool {
	id, _ := ctx.Value(promptEchoKey{}).(string)
	return id != "" && id == sessionID
}

func takeSteer(rows []QueuedMessage) []QueuedMessage {
	var out []QueuedMessage
	for _, row := range rows {
		if row.Mode != QueueModeAfterTurn {
			out = append(out, row)
		}
	}
	return out
}

func keepAfterTurn(rows []QueuedMessage) []QueuedMessage {
	var out []QueuedMessage
	for _, row := range rows {
		if row.Mode == QueueModeAfterTurn {
			out = append(out, row)
		}
	}
	return out
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
