package pachcafake

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Event ids are 26 characters, the length of the ULIDs Pachca hands out: a
// fixed prefix and a zero-padded counter, so they sort lexicographically in
// the order the events were logged, however many share a millisecond.
const (
	eventIDPrefix  = "01PACHCAFAKE"
	eventIDDigits  = 14
	maxEventsLimit = 50
)

func eventID(n int64) string {
	return fmt.Sprintf("%s%0*d", eventIDPrefix, eventIDDigits, n)
}

// validEventID reports whether id has the shape eventID produces.
func validEventID(id string) bool {
	digits, ok := strings.CutPrefix(id, eventIDPrefix)
	if !ok || len(digits) != eventIDDigits {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// cursor is what a next_page carries: the event the page ended on. The next
// page continues strictly before it, so events logged in between never
// shift a reader that is walking back.
type cursor struct {
	ID  string `json:"id"`
	Dir string `json:"dir"`
}

func encodeCursor(id string) string {
	raw, _ := json.Marshal(cursor{ID: id, Dir: "desc"})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", false
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil || c.Dir != "desc" || !validEventID(c.ID) {
		return "", false
	}
	return c.ID, true
}

// Events returns the event log as it stands, newest first.
func (s *Server) Events() []WebhookEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WebhookEvent, 0, len(s.events))
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		e.Payload = append(json.RawMessage(nil), e.Payload...)
		out = append(out, e)
	}
	return out
}

// logEventLocked appends an event, or does nothing while the history is
// off; it returns the event id, "" when nothing was logged. Caller holds
// s.mu.
func (s *Server) logEventLocked(eventType string, payload any) string {
	if s.opts.HistoryDisabled {
		return ""
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte("null")
	}
	s.nextEvent++
	id := eventID(s.nextEvent)
	s.events = append(s.events, WebhookEvent{
		ID:        id,
		EventType: eventType,
		Payload:   raw,
		CreatedAt: formatTime(s.now()),
	})
	return id
}

// logMessageEventLocked logs message_new, message_update or message_delete
// for a stored message. Caller holds s.mu.
func (s *Server) logMessageEventLocked(m *Message, event string) {
	s.logEventLocked("message_"+event, s.messagePayloadLocked(m, event))
}

// messagePayloadLocked renders a message as MessageWebhookPayload. In a
// personal chat the entity is the sender, in a thread's chat the thread,
// elsewhere the chat. Caller holds s.mu.
func (s *Server) messagePayloadLocked(m *Message, event string) messagePayload {
	p := messagePayload{
		Type:             "message",
		ID:               m.ID,
		Event:            event,
		EntityType:       m.EntityType,
		EntityID:         m.EntityID,
		Content:          m.Content,
		UserID:           m.UserID,
		CreatedAt:        m.CreatedAt,
		ChangedAt:        cloneString(m.ChangedAt),
		URL:              m.URL,
		ChatID:           m.ChatID,
		ParentMessageID:  cloneInt(m.ParentMessageID),
		WebhookTimestamp: s.now().Unix(),
	}
	if m.EntityType == "user" {
		p.EntityID = m.UserID
	}
	if c := s.chats[m.ChatID]; c != nil && c.thread != nil {
		parent, parentChat := c.thread.parentID, c.thread.parentChatID
		p.Thread = &webhookThread{MessageID: &parent, MessageChatID: &parentChat}
	}
	return p
}

// handleListEvents is GET /webhooks/events: newest first, limit 1..50
// (default 50), an opaque cursor that continues older than the page it came
// from.
func (s *Server) handleListEvents(r *http.Request, _ string, _ []byte, _ *Call) reply {
	q := r.URL.Query()
	limit := maxEventsLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxEventsLimit {
			return apiErrorReply(http.StatusBadRequest, "limit", v, "invalid", "limit must be between 1 and 50")
		}
		limit = n
	}
	before := ""
	if v := q.Get("cursor"); v != "" {
		id, ok := decodeCursor(v)
		if !ok {
			return apiErrorReply(http.StatusBadRequest, "cursor", v, "invalid", "Invalid cursor")
		}
		before = id
	}

	page := make([]WebhookEvent, 0, limit)
	hasNext := false
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if before != "" && e.ID >= before {
			continue
		}
		if len(page) == limit {
			hasNext = true
			break
		}
		page = append(page, e)
	}

	// next_page is required by the schema, so a page with nothing after it
	// still carries one: the cursor of its last event, or the one it was
	// asked with, or the floor below every id on an empty log.
	next := before
	if len(page) > 0 {
		next = page[len(page)-1].ID
	}
	if next == "" {
		next = eventID(0)
	}
	return jsonReply(http.StatusOK, map[string]any{
		"data": slices.Clip(page),
		"meta": map[string]any{
			"paginate": map[string]any{
				"next_page": encodeCursor(next),
				"has_next":  hasNext,
			},
		},
	})
}

// handleDeleteEvent is DELETE /webhooks/events/{id}.
func (s *Server) handleDeleteEvent(_ *http.Request, id string, _ []byte, _ *Call) reply {
	i := slices.IndexFunc(s.events, func(e WebhookEvent) bool { return e.ID == id })
	if i < 0 {
		return apiErrorReply(http.StatusNotFound, "id", id, "not_found", "Event not found")
	}
	s.events = slices.Delete(s.events, i, i+1)
	return reply{status: http.StatusNoContent}
}

// triggerID is a random uuid-shaped string, what a button click carries.
func triggerID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
