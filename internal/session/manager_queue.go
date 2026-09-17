package session

import (
	"fmt"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// EnqueueTurnMessage adds a follow-up to the turn running on sessionID and
// returns it together with the queue as it now stands.
//
// It refuses a session with no turn in flight (ErrNoActiveTurn): the caller
// sends that text as an ordinary prompt instead. A child session is read-only
// here for the same reason it is read-only everywhere - its only turn is the
// task its parent wrote.
func (m *Manager) EnqueueTurnMessage(sessionID, text string) (QueuedMessage, []QueuedMessage, error) {
	st, err := m.queueSession(sessionID)
	if err != nil {
		return QueuedMessage{}, nil, err
	}
	msg, err := st.EnqueueMessage(text)
	if err != nil {
		return QueuedMessage{}, st.QueuedMessages(), err
	}
	// The change announced itself through the notifier the manager installed;
	// nothing here publishes a second time.
	return msg, st.QueuedMessages(), nil
}

// QueuedTurnMessages lists what the session is holding for its running turn.
func (m *Manager) QueuedTurnMessages(sessionID string) ([]QueuedMessage, error) {
	st, err := m.queueSession(sessionID)
	if err != nil {
		return nil, err
	}
	return st.QueuedMessages(), nil
}

// CancelQueuedTurnMessage takes one queued message back and returns the rest.
func (m *Manager) CancelQueuedTurnMessage(sessionID, messageID string) ([]QueuedMessage, error) {
	st, err := m.queueSession(sessionID)
	if err != nil {
		return nil, err
	}
	if !st.CancelQueuedMessage(messageID) {
		return st.QueuedMessages(), ErrQueuedMessageNotFound
	}
	return st.QueuedMessages(), nil
}

// ClearQueuedTurnMessages drops everything the session is holding.
func (m *Manager) ClearQueuedTurnMessages(sessionID string) error {
	st, err := m.queueSession(sessionID)
	if err != nil {
		return err
	}
	st.ClearQueuedMessages()
	return nil
}

// queueSession resolves the session a queue call names and refuses the cases
// that can never hold a follow-up.
func (m *Manager) queueSession(sessionID string) (*State, error) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return nil, fmt.Errorf("session id is empty")
	}
	st := m.getSession(id)
	if st == nil {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	if err := readOnlyRefusal(st, id); err != nil {
		return nil, err
	}
	return st, nil
}

// AddMessageQueueObserver registers fn for every change of any session's
// message queue and returns the function that removes it again.
//
// The turn's own sender reaches the client that is driving the turn and anyone
// teed onto its stream, which is not everyone: a session is shared, and a
// second browser or a console attached over --remote may be watching it without
// reading that stream at all. An observer is how a surface fans the change out
// to every client it has - the HTTP server puts it on GET /coddy/events - so
// what one person queues is visible to all of them.
//
// fn runs on the goroutine that made the change and MUST NOT block: hand the
// update to a buffered channel and return, as the turn observers do.
func (m *Manager) AddMessageQueueObserver(fn func(acp.MessageQueueUpdate)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	m.queueObserverMu.Lock()
	if m.queueObservers == nil {
		m.queueObservers = make(map[int]func(acp.MessageQueueUpdate))
	}
	m.queueObserverSeq++
	id := m.queueObserverSeq
	m.queueObservers[id] = fn
	m.queueObserverMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.queueObserverMu.Lock()
			delete(m.queueObservers, id)
			m.queueObserverMu.Unlock()
		})
	}
}

// PublishMessageQueue tells everyone watching the session what the queue holds
// now: the running turn's sender (or the manager's own, between turns), and
// every registered observer.
//
// Both paths carry the whole queue and its version, so a client that is reached
// twice applies the same list twice rather than assembling it from deltas, and
// a client reached out of order keeps the newer one.
func (m *Manager) PublishMessageQueue(sessionID string, st *State) {
	if st == nil {
		return
	}
	msgs, version := st.QueueSnapshot()
	update := acp.MessageQueueUpdate{
		SessionUpdate: acp.UpdateTypeMessageQueue,
		SessionID:     sessionID,
		Messages:      QueuedMessagesWire(msgs),
		Version:       version,
	}
	sender := st.TurnSender()
	if sender == nil {
		sender = m.server
	}
	if sender != nil {
		_ = sender.SendSessionUpdate(sessionID, update)
	}

	m.queueObserverMu.Lock()
	fns := make([]func(acp.MessageQueueUpdate), 0, len(m.queueObservers))
	for _, fn := range m.queueObservers {
		fns = append(fns, fn)
	}
	m.queueObserverMu.Unlock()
	for _, fn := range fns {
		fn(update)
	}
}
