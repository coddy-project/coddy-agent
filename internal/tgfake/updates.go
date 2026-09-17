package tgfake

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// IncomingMessage is a message a person types into a chat. Zero fields take
// the defaults of a private chat with one test user: user 4242 "alice", whose
// private chat carries her own id, as Telegram numbers them.
type IncomingMessage struct {
	ChatID    int64  `json:"chat_id,omitempty"`
	ChatType  string `json:"chat_type,omitempty"` // private (default), group, supergroup
	ChatTitle string `json:"chat_title,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	Text      string `json:"text"`
	// ReplyToMessageID makes the message a reply to one already in the chat.
	ReplyToMessageID int `json:"reply_to_message_id,omitempty"`
	// Mention prefixes the text with the bot's @username, which is how a
	// group message addresses the bot.
	Mention bool `json:"mention,omitempty"`
}

// IncomingCallback is a tap on an inline keyboard button. The button is
// named by its callback_data, or by Label, the visible text (a "✓ " prefix
// is ignored); with MessageID zero the newest message carrying a keyboard is
// the one tapped.
type IncomingCallback struct {
	ChatID    int64  `json:"chat_id,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	MessageID int    `json:"message_id,omitempty"`
	Data      string `json:"data,omitempty"`
	Label     string `json:"label,omitempty"`
}

const (
	defaultUserID    = int64(4242)
	defaultUsername  = "alice"
	defaultFirstName = "Alice"
	defaultGroupName = "Fake group"
)

// commandPattern is the leading token that becomes a bot_command entity.
var commandPattern = regexp.MustCompile(`^/[A-Za-z0-9_]+(?:@[A-Za-z0-9_]+)?`)

func (in *IncomingMessage) normalize() {
	if in.UserID == 0 {
		in.UserID = defaultUserID
	}
	if in.Username == "" && in.UserID == defaultUserID {
		in.Username = defaultUsername
	}
	if in.FirstName == "" {
		if in.Username != "" {
			first, rest := firstRune(in.Username)
			in.FirstName = strings.ToUpper(first) + rest
		} else {
			in.FirstName = defaultFirstName
		}
	}
	in.ChatType = strings.ToLower(strings.TrimSpace(in.ChatType))
	if in.ChatType == "" {
		in.ChatType = "private"
	}
	if in.ChatID == 0 {
		if in.ChatType == "private" {
			in.ChatID = in.UserID
		} else {
			in.ChatID = -100_000_000_000 - in.UserID
		}
	}
	if in.ChatTitle == "" && in.ChatType != "private" {
		in.ChatTitle = defaultGroupName
	}
}

// InjectMessage delivers a user message: it lands in the chat transcript and
// becomes the next update. It returns the update id and the message id.
func (s *Server) InjectMessage(in IncomingMessage) (updateID, messageID int) {
	in.normalize()
	from := &User{ID: in.UserID, FirstName: in.FirstName, Username: in.Username}
	text := in.Text
	var entities []MessageEntity
	if in.Mention {
		mention := "@" + s.opts.BotUsername
		text = mention + " " + text
		entities = append(entities, MessageEntity{Type: "mention", Offset: 0, Length: utf16Len(mention)})
	}
	if m := commandPattern.FindString(text); m != "" {
		entities = append(entities, MessageEntity{Type: "bot_command", Offset: 0, Length: utf16Len(m)})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.ensureChatLocked(in.ChatID, in.ChatType, in.ChatTitle, from)
	msg := &Message{
		From:     from,
		Chat:     chat.wire(),
		Date:     s.now().Unix(),
		Text:     text,
		Entities: entities,
	}
	if in.ReplyToMessageID != 0 {
		if target := chat.byID[in.ReplyToMessageID]; target != nil {
			msg.ReplyToMessage = target.quoted()
		}
	}
	stored := chat.appendLocked(msg, false, "", false)
	upd := s.pushUpdateLocked(Update{Message: stored.clone()})
	return upd, stored.msg.MessageID
}

// InjectCallback delivers a button tap. It fails when the chat, the message
// or the button cannot be found.
func (s *Server) InjectCallback(in IncomingCallback) (updateID int, callbackID string, err error) {
	if in.UserID == 0 {
		in.UserID = defaultUserID
	}
	if in.Username == "" && in.UserID == defaultUserID {
		in.Username = defaultUsername
	}
	if in.FirstName == "" {
		in.FirstName = defaultFirstName
	}
	if in.ChatID == 0 {
		in.ChatID = in.UserID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.chats[in.ChatID]
	if chat == nil {
		return 0, "", fmt.Errorf("chat %d has no messages", in.ChatID)
	}
	var target *storedMessage
	if in.MessageID != 0 {
		target = chat.byID[in.MessageID]
		if target == nil || target.deleted {
			return 0, "", fmt.Errorf("message %d not found in chat %d", in.MessageID, in.ChatID)
		}
	} else {
		for i := len(chat.messages) - 1; i >= 0; i-- {
			m := chat.messages[i]
			if !m.deleted && m.msg.ReplyMarkup != nil && len(m.msg.ReplyMarkup.InlineKeyboard) > 0 {
				target = m
				break
			}
		}
		if target == nil {
			return 0, "", fmt.Errorf("chat %d has no message with an inline keyboard", in.ChatID)
		}
	}
	data := in.Data
	if data == "" {
		btn := target.button(in.Label)
		if btn == nil {
			return 0, "", fmt.Errorf("message %d has no button %q", target.msg.MessageID, in.Label)
		}
		data = btn.CallbackData
	}
	s.nextCbq++
	id := "cbq-" + strconv.Itoa(s.nextCbq)
	s.cbqChat[id] = in.ChatID
	upd := s.pushUpdateLocked(Update{CallbackQuery: &CallbackQuery{
		ID:           id,
		From:         User{ID: in.UserID, FirstName: in.FirstName, Username: in.Username},
		Message:      target.clone(),
		ChatInstance: strconv.FormatInt(in.ChatID, 10),
		Data:         data,
	}})
	return upd, id, nil
}

// pushUpdateLocked numbers an update and queues it only if the subscription
// in force at creation includes its kind. Later polls cannot recover an
// excluded update or discard one that was already queued.
// Caller holds s.mu.
func (s *Server) pushUpdateLocked(u Update) int {
	u.UpdateID = s.nextUpdate
	s.nextUpdate++
	if !s.deliverableLocked(u) {
		return u.UpdateID
	}
	s.pending = append(s.pending, u)
	s.wakeLocked()
	return u.UpdateID
}

// wakeLocked releases every getUpdates request waiting for news. Caller
// holds s.mu.
func (s *Server) wakeLocked() {
	close(s.wake)
	s.wake = make(chan struct{})
}

// kind names an update the way allowed_updates does.
func (u Update) kind() string {
	switch {
	case u.CallbackQuery != nil:
		return "callback_query"
	default:
		return "message"
	}
}

// deliverableLocked reports whether the subscription in force includes an
// update. Caller holds s.mu.
func (s *Server) deliverableLocked(u Update) bool {
	if s.allowed == nil {
		return true
	}
	for _, k := range s.allowed {
		if k == u.kind() {
			return true
		}
	}
	return false
}

// takeUpdatesLocked confirms everything below offset and returns up to limit
// of what is left. A negative offset counts from the end of the queue, as on
// api.telegram.org: -1 keeps the newest update and forgets the rest.
// Subscription changes affect only new updates. Caller holds s.mu.
func (s *Server) takeUpdatesLocked(offset, limit int) []Update {
	if offset < 0 {
		if from := len(s.pending) + offset; from > 0 {
			offset = s.pending[from].UpdateID
		} else {
			offset = 0
		}
	}
	kept := s.pending[:0]
	for _, u := range s.pending {
		if u.UpdateID >= offset {
			kept = append(kept, u)
		}
	}
	for i := len(kept); i < len(s.pending); i++ {
		s.pending[i] = Update{}
	}
	s.pending = kept
	if limit <= 0 || limit > defaultPollLimit {
		limit = defaultPollLimit
	}
	n := min(len(s.pending), limit)
	out := make([]Update, n)
	copy(out, s.pending[:n])
	return out
}

// PendingUpdates counts the updates no poll has confirmed yet.
func (s *Server) PendingUpdates() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// firstRune splits a string after its first character, so a name that does
// not start with an ASCII letter is capitalised without cutting a rune.
func firstRune(s string) (first, rest string) {
	for i := range s {
		if i > 0 {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

// utf16Len is the length Telegram measures entities in.
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

func itoa(n int) string { return strconv.Itoa(n) }
