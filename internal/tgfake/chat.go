package tgfake

import (
	"sort"
	"strings"
	"time"
)

// chatState is one chat as the fake remembers it: who is in it, every
// message in order, the drafts being streamed and what the bot did around
// them.
type chatState struct {
	id        int64
	typ       string
	title     string
	user      *User // the person on the other side of a private chat
	nextMsgID int
	messages  []*storedMessage
	byID      map[int]*storedMessage
	drafts    map[int64]*draft
	lastTyped time.Time
	callbacks []CallbackAnswer
}

// storedMessage is a message plus what the transcript view wants to know
// about it.
type storedMessage struct {
	msg       Message
	fromBot   bool
	parseMode string
	edited    bool
	deleted   bool
	rich      bool
}

type draft struct {
	id        int64
	markdown  string
	updatedAt time.Time
	revisions int
}

// CallbackAnswer is one answerCallbackQuery the bot issued.
type CallbackAnswer struct {
	ID        string `json:"id"`
	Text      string `json:"text,omitempty"`
	ShowAlert bool   `json:"show_alert,omitempty"`
}

// ensureChatLocked returns the chat, creating it on first sight. A private
// chat created by the bot itself (a sendMessage to a chat nobody wrote from)
// has no user until somebody does. Caller holds s.mu.
func (s *Server) ensureChatLocked(id int64, typ, title string, user *User) *chatState {
	c := s.chats[id]
	if c == nil {
		if typ == "" {
			typ = "private"
			if id < 0 {
				typ = "group"
			}
		}
		c = &chatState{id: id, typ: typ, title: title, byID: map[int]*storedMessage{}, drafts: map[int64]*draft{}}
		s.chats[id] = c
	}
	if user != nil && c.typ == "private" {
		c.user = user
	}
	if title != "" && c.title == "" {
		c.title = title
	}
	return c
}

// wire is the Chat object messages of this chat carry.
func (c *chatState) wire() Chat {
	ch := Chat{ID: c.id, Type: c.typ}
	if c.typ == "private" {
		if c.user != nil {
			ch.FirstName = c.user.FirstName
			ch.Username = c.user.Username
		}
	} else {
		ch.Title = c.title
	}
	return ch
}

// appendLocked numbers a message and stores it. Caller holds s.mu.
func (c *chatState) appendLocked(msg *Message, fromBot bool, parseMode string, rich bool) *storedMessage {
	c.nextMsgID++
	msg.MessageID = c.nextMsgID
	st := &storedMessage{msg: *msg, fromBot: fromBot, parseMode: parseMode, rich: rich}
	c.messages = append(c.messages, st)
	c.byID[msg.MessageID] = st
	return st
}

// clone returns a copy of the message as the wire carries it.
func (m *storedMessage) clone() *Message {
	out := m.msg
	if m.msg.ReplyMarkup != nil {
		kb := &InlineKeyboardMarkup{InlineKeyboard: make([][]InlineKeyboardButton, len(m.msg.ReplyMarkup.InlineKeyboard))}
		for i, row := range m.msg.ReplyMarkup.InlineKeyboard {
			kb.InlineKeyboard[i] = append([]InlineKeyboardButton(nil), row...)
		}
		out.ReplyMarkup = kb
	}
	if m.msg.ReplyToMessage != nil {
		q := *m.msg.ReplyToMessage
		out.ReplyToMessage = &q
	}
	out.Entities = append([]MessageEntity(nil), m.msg.Entities...)
	return &out
}

// quoted is the shape a message takes inside reply_to_message: the message
// itself without its own quote.
func (m *storedMessage) quoted() *Message {
	q := m.clone()
	q.ReplyToMessage = nil
	return q
}

// button finds a keyboard button by its visible text, ignoring the check
// mark the gateway puts in front of the current choice.
func (m *storedMessage) button(label string) *InlineKeyboardButton {
	if m.msg.ReplyMarkup == nil {
		return nil
	}
	want := strings.TrimPrefix(strings.TrimSpace(label), "✓ ")
	for _, row := range m.msg.ReplyMarkup.InlineKeyboard {
		for i := range row {
			if strings.TrimPrefix(row[i].Text, "✓ ") == want {
				return &row[i]
			}
		}
	}
	return nil
}

// ChatView is a chat as the simulation API and the page show it.
type ChatView struct {
	ChatID    int64            `json:"chat_id"`
	Type      string           `json:"type"`
	Title     string           `json:"title,omitempty"`
	Typing    bool             `json:"typing"`
	Messages  []MessageView    `json:"messages"`
	Drafts    []DraftView      `json:"drafts"`
	Callbacks []CallbackAnswer `json:"callbacks"`
}

// FindButton looks a button up by its visible text, the way a person finds
// it: the newest message that still shows it wins, and the check mark the
// gateway puts in front of the current choice is ignored. It returns the
// message carrying the keyboard and the button's callback_data.
func (v ChatView) FindButton(label string) (messageID int, data string, ok bool) {
	want := strings.TrimPrefix(strings.TrimSpace(label), "✓ ")
	for i := len(v.Messages) - 1; i >= 0; i-- {
		m := v.Messages[i]
		if m.Deleted {
			continue
		}
		for _, row := range m.Keyboard {
			for _, b := range row {
				if strings.TrimPrefix(b.Text, "✓ ") == want {
					return m.MessageID, b.CallbackData, true
				}
			}
		}
	}
	return 0, "", false
}

// MessageView is one message of a ChatView.
type MessageView struct {
	MessageID        int                      `json:"message_id"`
	From             string                   `json:"from"` // "bot" or "user"
	Username         string                   `json:"username,omitempty"`
	Text             string                   `json:"text"`
	ParseMode        string                   `json:"parse_mode,omitempty"`
	ReplyToMessageID int                      `json:"reply_to_message_id,omitempty"`
	Edited           bool                     `json:"edited"`
	Deleted          bool                     `json:"deleted"`
	Rich             bool                     `json:"rich"`
	Keyboard         [][]InlineKeyboardButton `json:"keyboard,omitempty"`
}

// DraftView is one rich-message draft of a ChatView: Telegram shows only the
// latest revision, so does the fake, with a count of how many it received.
type DraftView struct {
	DraftID   int64     `json:"draft_id"`
	Markdown  string    `json:"markdown"`
	UpdatedAt time.Time `json:"updated_at"`
	Revisions int       `json:"revisions"`
}

// typingWindow is how long Telegram shows "typing…" after one chat action.
const typingWindow = 5 * time.Second

// Chat returns the transcript of a chat; an unknown chat is an empty one.
func (s *Server) Chat(id int64) ChatView {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chats[id]
	if c == nil {
		return ChatView{ChatID: id, Type: "private", Messages: []MessageView{}, Drafts: []DraftView{}, Callbacks: []CallbackAnswer{}}
	}
	return c.view(s.now())
}

func (c *chatState) view(now time.Time) ChatView {
	v := ChatView{
		ChatID:    c.id,
		Type:      c.typ,
		Title:     c.title,
		Typing:    !c.lastTyped.IsZero() && now.Sub(c.lastTyped) < typingWindow,
		Messages:  make([]MessageView, 0, len(c.messages)),
		Drafts:    make([]DraftView, 0, len(c.drafts)),
		Callbacks: append([]CallbackAnswer{}, c.callbacks...),
	}
	for _, m := range c.messages {
		mv := MessageView{
			MessageID: m.msg.MessageID,
			From:      "user",
			Text:      m.msg.Text,
			ParseMode: m.parseMode,
			Edited:    m.edited,
			Deleted:   m.deleted,
			Rich:      m.rich,
		}
		if m.fromBot {
			mv.From = "bot"
		} else if m.msg.From != nil {
			mv.Username = m.msg.From.Username
		}
		if m.msg.ReplyToMessage != nil {
			mv.ReplyToMessageID = m.msg.ReplyToMessage.MessageID
		}
		if m.msg.ReplyMarkup != nil {
			mv.Keyboard = m.clone().ReplyMarkup.InlineKeyboard
		}
		v.Messages = append(v.Messages, mv)
	}
	for _, d := range c.drafts {
		v.Drafts = append(v.Drafts, DraftView{DraftID: d.id, Markdown: d.markdown, UpdatedAt: d.updatedAt, Revisions: d.revisions})
	}
	sort.Slice(v.Drafts, func(i, j int) bool { return v.Drafts[i].DraftID < v.Drafts[j].DraftID })
	return v
}

// ChatSummary is one row of the chat list.
type ChatSummary struct {
	ChatID   int64  `json:"chat_id"`
	Type     string `json:"type"`
	Title    string `json:"title,omitempty"`
	Messages int    `json:"messages"`
}

// Chats lists the chats the fake knows, by id.
func (s *Server) Chats() []ChatSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ChatSummary, 0, len(s.chats))
	for _, c := range s.chats {
		title := c.title
		if c.typ == "private" && c.user != nil {
			title = "@" + c.user.Username
		}
		out = append(out, ChatSummary{ChatID: c.id, Type: c.typ, Title: title, Messages: len(c.messages)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChatID < out[j].ChatID })
	return out
}

// Text renders a chat as plain lines, one per message, keyboards and drafts
// indented under the message they belong to: what a shell script greps.
func (v ChatView) Text() string {
	var sb strings.Builder
	for _, m := range v.Messages {
		if m.Deleted {
			continue
		}
		who := m.From
		if m.From == "user" && m.Username != "" {
			who = m.Username
		}
		sb.WriteString("[" + itoa(m.MessageID) + "] " + who + ": " + strings.ReplaceAll(m.Text, "\n", "\n    ") + "\n")
		for _, row := range m.Keyboard {
			sb.WriteString("   ")
			for _, b := range row {
				sb.WriteString(" [" + b.Text + "]")
			}
			sb.WriteString("\n")
		}
	}
	for _, d := range v.Drafts {
		sb.WriteString("draft " + itoa(int(d.DraftID)) + " (rev " + itoa(d.Revisions) + "): " + strings.ReplaceAll(d.Markdown, "\n", "\n    ") + "\n")
	}
	if v.Typing {
		sb.WriteString("typing…\n")
	}
	return sb.String()
}
