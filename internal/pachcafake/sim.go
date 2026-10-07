package pachcafake

import (
	"fmt"
	"slices"
	"strconv"
)

// Post is a message a person writes: who, where, what, and optionally the
// message it replies to (which must be in the same chat).
type Post struct {
	UserID          int64
	ChatID          int64
	Content         string
	ParentMessageID int64
}

// AddUser registers a person, or renames one already known.
func (s *Server) AddUser(id int64, nickname, firstName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.users[id]; u != nil {
		u.nickname, u.firstName = nickname, firstName
		return
	}
	s.users[id] = &user{id: id, nickname: nickname, firstName: firstName, createdAt: s.now()}
}

// ensureUserLocked returns a person, inventing one on first sight. Caller
// holds s.mu.
func (s *Server) ensureUserLocked(id int64) *user {
	u := s.users[id]
	if u == nil {
		u = &user{id: id, nickname: "user" + strconv.FormatInt(id, 10), firstName: "User " + strconv.FormatInt(id, 10), createdAt: s.now()}
		s.users[id] = u
	}
	return u
}

// AddGroupChat creates a group chat with the given id, the bot among its
// members, the people in it registered on first sight. Calling it again for
// the same id replaces the name and the members and keeps the history.
func (s *Server) AddGroupChat(id int64, name string, memberIDs ...int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	members := []int64{s.opts.BotUserID}
	for _, m := range memberIDs {
		s.ensureUserLocked(m)
		if !slices.Contains(members, m) {
			members = append(members, m)
		}
	}
	owner := s.opts.BotUserID
	if len(memberIDs) > 0 {
		owner = memberIDs[0]
	}
	if c := s.chats[id]; c != nil {
		c.name, c.members, c.ownerID = name, members, owner
		return
	}
	now := s.now()
	s.chats[id] = &chat{id: id, name: name, members: members, ownerID: owner, createdAt: now, lastAt: now}
}

// PersonalChat returns the id of the personal chat between the bot and a
// person, creating the chat (and the person) on demand.
func (s *Server) PersonalChat(userID int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureUserLocked(userID)
	return s.personalChatLocked(userID).id
}

// personalChatLocked returns the personal chat with a person, creating it.
// Caller holds s.mu.
func (s *Server) personalChatLocked(userID int64) *chat {
	if id, ok := s.personal[userID]; ok {
		return s.chats[id]
	}
	now := s.now()
	c := &chat{
		id:        s.newChatIDLocked(),
		name:      s.users[userID].firstName,
		personal:  true,
		members:   []int64{s.opts.BotUserID, userID},
		ownerID:   userID,
		createdAt: now,
		lastAt:    now,
	}
	s.chats[c.id] = c
	s.personal[userID] = c.id
	return c
}

// newChatIDLocked hands out the next free chat id, stepping over ids a test
// gave a group chat by hand. Caller holds s.mu.
func (s *Server) newChatIDLocked() int64 {
	for s.chats[s.nextChat] != nil {
		s.nextChat++
	}
	id := s.nextChat
	s.nextChat++
	return id
}

// StartThread opens a thread under a message, or returns the one already
// open: the thread id and the thread's own chat. A message posted in that
// chat is addressed to the thread and its webhook payload names the parent
// message and the parent's chat. It panics on an unknown message, a
// mistake in the test, not in the bot.
func (s *Server) StartThread(parentMessageID int64) (threadID, threadChatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent := s.messages[parentMessageID]
	if parent == nil {
		panic(fmt.Sprintf("pachcafake: StartThread: no message %d", parentMessageID))
	}
	if parent.Thread != nil {
		return parent.Thread.ID, parent.Thread.ChatID
	}
	parentChat := s.chats[parent.ChatID]
	now := s.now()
	t := &thread{id: s.nextThread, parentID: parent.ID, parentChatID: parent.ChatID}
	s.nextThread++
	c := &chat{
		id:        s.newChatIDLocked(),
		name:      "Thread " + strconv.FormatInt(t.id, 10),
		members:   slices.Clone(parentChat.members),
		ownerID:   parent.UserID,
		createdAt: now,
		lastAt:    now,
		thread:    t,
	}
	t.chatID = c.id
	s.chats[c.id] = c
	s.threads[t.id] = t
	parent.Thread = &MessageThread{ID: t.id, ChatID: c.id}
	return t.id, c.id
}

// UserPosts stores a message a person wrote and logs message_new. It panics
// on an unknown chat or a parent in another chat.
func (s *Server) UserPosts(p Post) Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chats[p.ChatID]
	if c == nil {
		panic(fmt.Sprintf("pachcafake: UserPosts: no chat %d", p.ChatID))
	}
	s.ensureUserLocked(p.UserID)
	var parent *int64
	if p.ParentMessageID != 0 {
		pm := s.messages[p.ParentMessageID]
		if pm == nil || pm.ChatID != c.id {
			panic(fmt.Sprintf("pachcafake: UserPosts: no message %d in chat %d", p.ParentMessageID, c.id))
		}
		id := p.ParentMessageID
		parent = &id
	}
	m := s.storeMessageLocked(c, p.UserID, p.Content, parent)
	s.logMessageEventLocked(m, "new")
	return m.clone()
}

// UserEdits changes a message's content as its author would and logs
// message_update. It panics on an unknown message.
func (s *Server) UserEdits(messageID int64, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.messages[messageID]
	if m == nil {
		panic(fmt.Sprintf("pachcafake: UserEdits: no message %d", messageID))
	}
	m.Content = content
	changed := formatTime(s.now())
	m.ChangedAt = &changed
	s.logMessageEventLocked(m, "update")
}

// UserClicks presses the button of a message that carries data and logs
// button_click. It fails when the message is unknown or has no such button.
func (s *Server) UserClicks(userID, messageID int64, data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.messages[messageID]
	if m == nil {
		return fmt.Errorf("pachcafake: no message %d", messageID)
	}
	found := false
	for _, row := range m.Buttons {
		for _, b := range row {
			if b.Data != "" && b.Data == data {
				found = true
			}
		}
	}
	if !found {
		return fmt.Errorf("pachcafake: message %d has no button with data %q", messageID, data)
	}
	s.ensureUserLocked(userID)
	s.logEventLocked("button_click", buttonPayload{
		Type:             "button",
		Event:            "click",
		MessageID:        messageID,
		TriggerID:        triggerID(),
		Data:             data,
		UserID:           userID,
		ChatID:           m.ChatID,
		WebhookTimestamp: s.now().Unix(),
	})
	return nil
}

// LogRaw appends an event with any payload, for shapes the helpers above do
// not produce, and returns its id; "" while the history is off.
func (s *Server) LogRaw(eventType string, payload any) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logEventLocked(eventType, payload)
}
