package pachcafake

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Button limits of the API: rows per message (the schema's maxItems),
// buttons per row, buttons per message, and the byte lengths of a text and a
// payload.
const (
	maxButtonRows    = 32
	maxButtonsPerRow = 8
	maxButtonsTotal  = 100
	maxButtonText    = 255
	maxButtonURL     = 1024
	maxButtonData    = 255
)

// createRequest is MessageCreateRequest, as much of it as the fake reads.
type createRequest struct {
	Message *struct {
		EntityType         string            `json:"entity_type"`
		EntityID           int64             `json:"entity_id"`
		Content            *string           `json:"content"`
		Files              []json.RawMessage `json:"files"`
		Buttons            [][]Button        `json:"buttons"`
		ParentMessageID    *int64            `json:"parent_message_id"`
		DisplayAvatarURL   *string           `json:"display_avatar_url"`
		DisplayName        *string           `json:"display_name"`
		SkipInviteMentions bool              `json:"skip_invite_mentions"`
	} `json:"message"`
	LinkPreview bool `json:"link_preview"`
}

// handleCreateMessage is POST /messages: resolve the target chat, validate,
// store, and log the bot's own message unless the bot ignores it.
func (s *Server) handleCreateMessage(_ *http.Request, _ string, body []byte, call *Call) reply {
	var req createRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return apiErrorReply(http.StatusBadRequest, "body", "", "invalid", "Malformed JSON: "+err.Error())
	}
	if req.Message == nil {
		return apiErrorReply(http.StatusBadRequest, "message", "", "required", "message is required")
	}
	m := req.Message
	call.SkipInviteMentions = m.SkipInviteMentions
	call.LinkPreview = req.LinkPreview

	entityType := m.EntityType
	if entityType == "" {
		entityType = "discussion"
	}
	c, out, ok := s.resolveTargetLocked(entityType, m.EntityID)
	if !ok {
		return out
	}
	content := ""
	if m.Content != nil {
		content = *m.Content
	}
	if strings.TrimSpace(content) == "" && len(m.Files) == 0 {
		return apiErrorReply(http.StatusUnprocessableEntity, "content", content, "blank", "Content can't be blank")
	}
	if out, ok := s.checkContentLength(content); !ok {
		return out
	}
	if out, ok := validateButtons(m.Buttons); !ok {
		return out
	}
	var parent *int64
	if m.ParentMessageID != nil {
		p := s.messages[*m.ParentMessageID]
		value := strconv.FormatInt(*m.ParentMessageID, 10)
		if p == nil {
			return apiErrorReply(http.StatusNotFound, "parent_message_id", value, "not_found", "Parent message not found")
		}
		if p.ChatID != c.id {
			return apiErrorReply(http.StatusUnprocessableEntity, "parent_message_id", value, "invalid", "Parent message belongs to another chat")
		}
		parent = cloneInt(m.ParentMessageID)
	}

	msg := s.storeMessageLocked(c, s.opts.BotUserID, content, parent)
	if len(m.Buttons) > 0 {
		msg.Buttons = cloneButtons(m.Buttons)
	}
	msg.Files = append(msg.Files, m.Files...)
	msg.DisplayAvatarURL = cloneString(m.DisplayAvatarURL)
	msg.DisplayName = cloneString(m.DisplayName)
	if !s.opts.IgnoreSelfMessages {
		s.logMessageEventLocked(msg, "new")
	}
	return dataReply(http.StatusCreated, msg.clone())
}

// resolveTargetLocked finds the chat a POST /messages lands in. Caller
// holds s.mu.
func (s *Server) resolveTargetLocked(entityType string, entityID int64) (*chat, reply, bool) {
	value := strconv.FormatInt(entityID, 10)
	switch entityType {
	case "discussion":
		if c := s.chats[entityID]; c != nil {
			return c, reply{}, true
		}
		return nil, apiErrorReply(http.StatusNotFound, "entity_id", value, "not_found", "Chat not found"), false
	case "thread":
		if t := s.threads[entityID]; t != nil {
			return s.chats[t.chatID], reply{}, true
		}
		return nil, apiErrorReply(http.StatusNotFound, "entity_id", value, "not_found", "Thread not found"), false
	case "user":
		if entityID == s.opts.BotUserID {
			return nil, apiErrorReply(http.StatusUnprocessableEntity, "entity_id", value, "personal_chat", "A bot cannot write to itself"), false
		}
		if s.users[entityID] == nil {
			return nil, apiErrorReply(http.StatusNotFound, "entity_id", value, "not_found", "User not found"), false
		}
		return s.personalChatLocked(entityID), reply{}, true
	default:
		return nil, apiErrorReply(http.StatusUnprocessableEntity, "entity_type", entityType, "inclusion", "entity_type must be discussion, thread or user"), false
	}
}

// checkContentLength applies Options.MaxContentRunes.
func (s *Server) checkContentLength(content string) (reply, bool) {
	if s.opts.MaxContentRunes > 0 && utf8.RuneCountInString(content) > s.opts.MaxContentRunes {
		return apiErrorReply(http.StatusUnprocessableEntity, "content", "", "too_long",
			"Content is too long (maximum is "+strconv.Itoa(s.opts.MaxContentRunes)+" characters)"), false
	}
	return reply{}, true
}

// validateButtons holds a keyboard to the limits of the API.
func validateButtons(rows [][]Button) (reply, bool) {
	bad := func(message string) (reply, bool) {
		return apiErrorReply(http.StatusUnprocessableEntity, "buttons", "", "invalid", message), false
	}
	if len(rows) > maxButtonRows {
		return bad("Too many button rows (maximum is " + strconv.Itoa(maxButtonRows) + ")")
	}
	total := 0
	for _, row := range rows {
		if len(row) > maxButtonsPerRow {
			return bad("Too many buttons in a row (maximum is " + strconv.Itoa(maxButtonsPerRow) + ")")
		}
		total += len(row)
		for _, b := range row {
			if n := utf8.RuneCountInString(b.Text); n == 0 || n > maxButtonText {
				return bad("Button text must be 1 to " + strconv.Itoa(maxButtonText) + " characters")
			}
			if (b.URL == "") == (b.Data == "") {
				return bad("A button carries exactly one of url or data")
			}
			if len(b.URL) > maxButtonURL {
				return bad("Button url is too long (maximum is " + strconv.Itoa(maxButtonURL) + ")")
			}
			if len(b.Data) > maxButtonData {
				return bad("Button data is too long (maximum is " + strconv.Itoa(maxButtonData) + " bytes)")
			}
		}
	}
	if total > maxButtonsTotal {
		return bad("Too many buttons (maximum is " + strconv.Itoa(maxButtonsTotal) + ")")
	}
	return reply{}, true
}

// handleGetMessage is GET /messages/{id}.
func (s *Server) handleGetMessage(_ *http.Request, id string, _ []byte, _ *Call) reply {
	m, out, ok := s.lookupMessageLocked(id)
	if !ok {
		return out
	}
	return dataReply(http.StatusOK, m.clone())
}

// handleUpdateMessage is PUT /messages/{id}: content and buttons of a
// message the bot wrote. Buttons absent keep the keyboard, [] or null
// remove it.
func (s *Server) handleUpdateMessage(_ *http.Request, id string, body []byte, _ *Call) reply {
	m, out, ok := s.lookupMessageLocked(id)
	if !ok {
		return out
	}
	var req struct {
		Message map[string]json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return apiErrorReply(http.StatusBadRequest, "body", "", "invalid", "Malformed JSON: "+err.Error())
	}
	if req.Message == nil {
		return apiErrorReply(http.StatusBadRequest, "message", "", "required", "message is required")
	}
	if m.UserID != s.opts.BotUserID {
		return apiErrorReply(http.StatusForbidden, "id", id, "not_authorized", "Only the author can edit a message")
	}

	content := m.Content
	if raw, ok := req.Message["content"]; ok {
		if err := json.Unmarshal(raw, &content); err != nil {
			return apiErrorReply(http.StatusBadRequest, "content", "", "invalid", "content must be a string")
		}
		if strings.TrimSpace(content) == "" && len(m.Files) == 0 {
			return apiErrorReply(http.StatusUnprocessableEntity, "content", content, "blank", "Content can't be blank")
		}
		if out, ok := s.checkContentLength(content); !ok {
			return out
		}
	}
	buttons := m.Buttons
	if raw, ok := req.Message["buttons"]; ok {
		var rows [][]Button
		if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if err := json.Unmarshal(raw, &rows); err != nil {
				return apiErrorReply(http.StatusBadRequest, "buttons", "", "invalid", "buttons must be an array of rows")
			}
		}
		if out, ok := validateButtons(rows); !ok {
			return out
		}
		buttons = nil
		if len(rows) > 0 {
			buttons = cloneButtons(rows)
		}
	}

	m.Content = content
	m.Buttons = buttons
	changed := formatTime(s.now())
	m.ChangedAt = &changed
	if !s.opts.IgnoreSelfMessages {
		s.logMessageEventLocked(m, "update")
	}
	return dataReply(http.StatusOK, m.clone())
}

// lookupMessageLocked parses a path id and finds the message. Caller holds
// s.mu.
func (s *Server) lookupMessageLocked(id string) (*Message, reply, bool) {
	n, err := strconv.ParseInt(id, 10, 64)
	m := s.messages[n]
	if err != nil || m == nil {
		return nil, apiErrorReply(http.StatusNotFound, "id", id, "not_found", "Message not found"), false
	}
	return m, reply{}, true
}

// storeMessageLocked appends a message to a chat and the index. Caller
// holds s.mu.
func (s *Server) storeMessageLocked(c *chat, userID int64, content string, parent *int64) *Message {
	now := s.now()
	id := s.nextMsg
	s.nextMsg++
	m := &Message{
		ID:              id,
		ChatID:          c.id,
		RootChatID:      c.id,
		Content:         content,
		UserID:          userID,
		CreatedAt:       formatTime(now),
		URL:             "https://app.pachca.com/chats/" + strconv.FormatInt(c.id, 10) + "?message=" + strconv.FormatInt(id, 10),
		Files:           []json.RawMessage{},
		ParentMessageID: parent,
	}
	m.EntityType, m.EntityID = s.entityOfLocked(c)
	if c.thread != nil {
		m.RootChatID = c.thread.parentChatID
	}
	c.messages = append(c.messages, m)
	c.lastAt = now
	s.messages[id] = m
	return m
}

// entityOfLocked is the entity a message in a chat is addressed to: the
// thread for a thread's chat, the person for a personal chat (from the bot's
// side), the chat itself otherwise. Caller holds s.mu.
func (s *Server) entityOfLocked(c *chat) (string, int64) {
	switch {
	case c.thread != nil:
		return "thread", c.thread.id
	case c.personal:
		for _, id := range c.members {
			if id != s.opts.BotUserID {
				return "user", id
			}
		}
		return "user", s.opts.BotUserID
	default:
		return "discussion", c.id
	}
}
